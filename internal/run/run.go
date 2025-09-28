package run

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"claudex/internal/buildctx"
	"claudex/internal/containers"
	"claudex/internal/dockerx"
	"claudex/internal/version"
	"claudex/internal/workspace"
)

type Options struct {
	UseHostNetwork bool
	NameOverride   string
	ForceReplace   bool
	AlwaysParallel bool
	StrictMounts   bool
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
			o.UseHostNetwork = true
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
	args = append(args, "run", "--name", o.Name, "-d", "-e", "OPENAI_API_KEY", "-e", "AI_API_MK", "-e", "GEMINI_API_KEY", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW")
	if o.UseHostNetwork {
		args = append(args, "--network", "host")
	}
	// docker sock mount if present
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		args = append(args, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}
	// config dirs
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	claudeJson := filepath.Join(home, ".claude.json")
	if fi, err := os.Stat(claudeJson); err == nil && !fi.IsDir() {
		args = append(args, "-v", fmt.Sprintf("%s:/home/node/.claude.json", claudeJson))
	}
	for _, dir := range []string{"claude", "codex", "gemini"} {
		configDir := filepath.Join(home, "."+dir)
		if fi, err := os.Stat(configDir); err == nil && fi.IsDir() {
			args = append(args, "-v", fmt.Sprintf("%s:/home/node/.%s", configDir, dir))
		}
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
		if err := dx.Build("claudex", ctxDir, false); err != nil {
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
			fmt.Fprintln(out, "Initializing firewall...")
			if err := dx.Exec(o.Name, "bash", "-c", "sudo /usr/local/bin/init-firewall.sh"); err != nil {
				fmt.Fprintf(errOut, "Warning: init-firewall failed: %v\n", err)
			}
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
	fmt.Fprintln(out, "Initializing firewall...")
	if err := dx.Exec(o.Name, "bash", "-c", "sudo /usr/local/bin/init-firewall.sh"); err != nil {
		fmt.Fprintf(errOut, "Warning: init-firewall failed: %v\n", err)
	}
	fmt.Fprintln(out, "Attaching shell. Type 'exit' to leave.")
	return dx.ExecInteractive(o.Name, []string{"bash"}, in, out, errOut)
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
