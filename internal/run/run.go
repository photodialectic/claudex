package run

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/photodialectic/claudex/internal/buildctx"
	"github.com/photodialectic/claudex/internal/containers"
	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/harness"
	"github.com/photodialectic/claudex/internal/version"
	"github.com/photodialectic/claudex/internal/workspace"
)

type Options struct {
	Network        string
	NameOverride   string
	ForceReplace   bool
	AlwaysParallel bool
	StrictMounts   bool
	SkipGit        bool
	Firewall       bool
	CACertFile     string
	Workdirs       []string

	// Derived
	Normalized []string
	Signature  string
	Slug       string
	Name       string
}

func ParseArgs(args []string) (Options, error) {
	var o Options
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--host-network":
			o.Network = "host"
		case "--network":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--network requires a value")
			}
			o.Network = args[i+1]
			i++
		case "--no-git":
			o.SkipGit = true
		case "--firewall":
			o.Firewall = true
		case "--name":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--name requires a value")
			}
			o.NameOverride = args[i+1]
			i++
		case "--replace":
			o.ForceReplace = true
		case "--parallel":
			o.AlwaysParallel = true
		case "--strict-mounts":
			o.StrictMounts = true
		case "--ca-cert":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--ca-cert requires a path")
			}
			o.CACertFile = args[i+1]
			i++
		default:
			o.Workdirs = append(o.Workdirs, a)
		}
	}
	return o, nil
}

// Derive fills in normalized dirs and name components.
func (o *Options) Derive() error {
	norm, err := workspace.NormalizeDirs(workspace.DefaultDirs(o.Workdirs))
	if err != nil {
		return err
	}
	o.Normalized = norm
	o.Signature = workspace.DeriveSignature(norm)
	o.Slug = workspace.DeriveSlug(norm)
	name := workspace.DeriveName(o.Slug, o.Signature)
	if o.NameOverride != "" {
		name = o.NameOverride
	}
	if o.AlwaysParallel {
		name = fmt.Sprintf("%s-%d", name, time.Now().Unix())
	}
	o.Name = name
	return nil
}

// BuildRunArgs builds docker run args array based on options and env.
func (o Options) BuildRunArgs() ([]string, error) {
	var args []string
	args = append(args, "run", "--name", o.Name, "-d")

	args = append(args, configEnvArgs()...)

	args = append(args, "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW")

	if o.Network != "" {
		args = append(args, "--network", o.Network)
	}

	// docker sock mount if present
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		args = append(args, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}

	// harness config volumes
	args = append(args, configMountArgs()...)

	if o.CACertFile != "" {
		abs, err := filepath.Abs(o.CACertFile)
		if err != nil {
			return nil, err
		}
		if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
			return nil, fmt.Errorf("--ca-cert: %s is not a readable file", o.CACertFile)
		}
		args = append(args, "-v", fmt.Sprintf("%s:/usr/local/share/ca-certificates/claudex-custom.crt:ro", abs))
	}

	// workspace mounts
	for _, abs := range o.Normalized {
		base := filepath.Base(abs)
		args = append(args, "-v", fmt.Sprintf("%s:/workspace/%s", abs, base))
	}
	// labels
	b, _ := json.Marshal(o.Normalized)
	mountsLabel := string(b)
	args = append(args, "--label", "com.claudex.signature="+o.Signature, "--label", "com.claudex.version="+version.Version, "--label", "com.claudex.slug="+o.Slug, "--label", "com.claudex.mounts="+mountsLabel)
	// Image and a keepalive command to prevent immediate exit
	// Use a very portable command
	args = append(args, "claudex", "tail", "-f", "/dev/null")
	return args, nil
}

// Run orchestrates the container lifecycle (ensure image, reuse or create, attach shell).
func Run(args []string, in io.Reader, out, errOut io.Writer, dx dockerx.Docker) error {
	o, err := ParseArgs(args)
	if err != nil {
		return err
	}
	if err := o.Derive(); err != nil {
		return err
	}
	// Ensure image exists, build if missing using embedded context
	fmt.Fprintln(out, "Ensuring image 'claudex' exists...")
	present, err := dx.ImageExists("claudex")
	if err != nil {
		return err
	}
	if !present {
		fmt.Fprintln(out, "Building image 'claudex' (first run)...")
		ctxDir, cleanup, err := buildctx.PrepareBuildContext()
		if err != nil {
			return err
		}
		defer cleanup()
		if err := dx.Build("claudex", ctxDir, dockerx.BuildOptions{}); err != nil {
			return fmt.Errorf("docker build failed: %w", err)
		}
	}

	// Check existing container
	exists, running, info, _ := containers.Exists(dx, o.Name)
	if exists && !o.ForceReplace {
		fmt.Fprintf(out, "Reusing container %s\n", o.Name)
		if o.StrictMounts {
			if err := containers.WarnOrErrorOnMountMismatch(info, o.Normalized, true, o.Name); err != nil {
				return err
			}
		}
		if !running {
			fmt.Fprintf(out, "Starting container %s...\n", o.Name)
			if err := dx.Start(o.Name); err != nil {
				return fmt.Errorf("failed to start container: %w", err)
			}
			if ok := waitRunning(dx, o.Name, 5*time.Second); !ok {
				if logs, lerr := dx.Logs(o.Name, 50); lerr == nil && len(logs) > 0 {
					fmt.Fprintln(errOut, "Recent container logs:")
					fmt.Fprintln(errOut, string(logs))
				}
				fmt.Fprintln(errOut, "Container failed to stay running; recreating...")
				_ = dx.Remove(o.Name, true)
				exists = false
			}
		}
		if exists {
			maybeInitGit(o.SkipGit, dx, o.Name, out, errOut)
			maybeInitFirewall(o.Firewall, dx, o.Name, out, errOut)
			maybeInstallCA(o.CACertFile, dx, o.Name, out, errOut)
			fmt.Fprintln(out, "Attaching shell. Type 'exit' to leave.")
			return dx.ExecInteractive(o.Name, []string{"bash"}, in, out, errOut)
		}
	}
	if exists && o.ForceReplace {
		fmt.Fprintf(out, "Replacing existing container %s...\n", o.Name)
		_ = dx.Remove(o.Name, true)
		exists = false
	}

	if !exists {
		return createAndAttach(o, in, out, errOut, dx)
	}
	// Should not reach here; safeguard
	return fmt.Errorf("unexpected state; please retry with --replace")
}

func createAndAttach(o Options, in io.Reader, out, errOut io.Writer, dx dockerx.Docker) error {
	fmt.Fprintf(out, "Creating container %s...\n", o.Name)
	seeds, err := prepareConfigVolumes(dx)
	if err != nil {
		return err
	}
	runArgs, err := o.BuildRunArgs()
	if err != nil {
		return err
	}
	if err := dx.Run(runArgs...); err != nil {
		return fmt.Errorf("docker run failed: %w", err)
	}
	if ok := waitRunning(dx, o.Name, 5*time.Second); !ok {
		if logs, lerr := dx.Logs(o.Name, 50); lerr == nil && len(logs) > 0 {
			fmt.Fprintln(errOut, "Recent container logs:")
			fmt.Fprintln(errOut, string(logs))
		}
		return fmt.Errorf("container %s did not stay running after creation; inspect logs and retry with --replace", o.Name)
	}
	seedVolumes(dx, seeds, out, errOut)
	maybeInitGit(o.SkipGit, dx, o.Name, out, errOut)
	maybeInitFirewall(o.Firewall, dx, o.Name, out, errOut)
	maybeInstallCA(o.CACertFile, dx, o.Name, out, errOut)
	fmt.Fprintln(out, "Attaching shell. Type 'exit' to leave.")
	return dx.ExecInteractive(o.Name, []string{"bash"}, in, out, errOut)
}

func maybeInitGit(skip bool, dx dockerx.Docker, name string, out, errOut io.Writer) {
	if skip {
		return
	}
	if _, err := dx.ExecOutput(name, []string{"bash", "-c", "test -d /workspace/.git"}); err == nil {
		return
	}
	fmt.Fprintln(out, "Initializing Git repository in /workspace...")
	if err := dx.Exec(name, "bash", "-c", "cd /workspace && git init --quiet"); err != nil {
		fmt.Fprintf(errOut, "Warning: git init failed: %v\n", err)
		return
	}
	if err := dx.Exec(name, "bash", "-c", "cd /workspace && { [ -f .gitignore ] || printf '/*.md\n' > .gitignore; }"); err != nil {
		fmt.Fprintf(errOut, "Warning: unable to write .gitignore: %v\n", err)
	}
	if err := dx.Exec(name, "bash", "-c", "cd /workspace && git add -A"); err != nil {
		fmt.Fprintf(errOut, "Warning: git add failed: %v\n", err)
		return
	}
	fmt.Fprintln(out, "Initialized Git repository in /workspace and staged current contents")
}

func maybeInstallCA(caFile string, dx dockerx.Docker, name string, out, errOut io.Writer) {
	if caFile == "" {
		return
	}
	fmt.Fprintln(out, "Installing custom CA certificate...")
	if err := dx.Exec(name, "bash", "-c", "sudo update-ca-certificates"); err != nil {
		fmt.Fprintf(errOut, "Warning: update-ca-certificates failed: %v\n", err)
	}
}

func maybeInitFirewall(enable bool, dx dockerx.Docker, name string, out, errOut io.Writer) {
	if !enable {
		return
	}
	fmt.Fprintln(out, "Initializing firewall...")
	if err := dx.Exec(name, "bash", "-c", "sudo /usr/local/bin/init-firewall.sh"); err != nil {
		fmt.Fprintf(errOut, "Warning: init-firewall failed: %v\n", err)
	}
}

func waitRunning(dx dockerx.Docker, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, running, _, _ := containers.Exists(dx, name)
		if running {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// configMountArgs returns the -v mount arguments for every harness volume.
func configMountArgs() []string {
	var args []string
	for _, h := range harness.Registry() {
		for _, m := range h.Mounts {
			args = append(args, "-v", m.Volume+":"+m.Container)
		}
	}
	return args
}

// configEnvArgs returns the -e arguments for every forwarded env var that is set.
func configEnvArgs() []string {
	var args []string
	for _, k := range harness.EnvVars() {
		if os.Getenv(k) != "" {
			args = append(args, "-e", k)
		}
	}
	return args
}

// prepareConfigVolumes ensures every harness volume exists and returns the
// mounts whose volumes were newly created and therefore need seeding from the
// host (when a matching host path exists).
func prepareConfigVolumes(dx dockerx.Docker) ([]harness.Mount, error) {
	var seeds []harness.Mount
	for _, h := range harness.Registry() {
		for _, m := range h.Mounts {
			created, err := dx.VolumeCreate(m.Volume)
			if err != nil {
				return nil, err
			}
			if !created {
				continue
			}
			hp := m.HostPath()
			if hp == "" {
				continue
			}
			if _, err := os.Stat(hp); err != nil {
				continue
			}
			seeds = append(seeds, m)
		}
	}
	return seeds, nil
}

// seedVolumes copies host config into freshly created (empty) volumes.
func seedVolumes(dx dockerx.Docker, seeds []harness.Mount, out, errOut io.Writer) {
	for _, m := range seeds {
		fmt.Fprintf(out, "Seeding %s volume from %s...\n", m.Volume, m.HostPath())
		if err := dx.CopyHostToVolume(m.HostPath(), m.Volume, m.VolumePath); err != nil {
			fmt.Fprintf(errOut, "Warning: failed to seed %s: %v\n", m.Volume, err)
		}
	}
}
