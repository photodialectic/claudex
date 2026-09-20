package dockerx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Docker abstracts docker operations for testability.
type Docker interface {
	Inspect(name string) (Container, error)
	PS(includeStopped bool) ([]string, error)
	Run(args ...string) error
	Exec(args ...string) error
	CP(src, dst string) error
	Start(name string) error
	Remove(name string, force bool) error
	ImageExists(tag string) (bool, error)
	Build(tag, contextDir string, opts BuildOptions) error
	ExecInteractive(name string, cmd []string, in io.Reader, out, errOut io.Writer) error
	ExecOutput(name string, cmd []string) ([]byte, error)
	Logs(name string, tail int) ([]byte, error)
	VolumeCreate(name string) (bool, error)
	CopyHostToVolume(hostPath, volume, volumePath string) error
	CopyVolumeToHost(volume, volumePath, hostPath string) error
}

// BuildOptions configures docker build behaviour.
type BuildOptions struct {
	NoCache   bool
	BuildArgs map[string]string
}

type Container struct {
	ID        string
	Name      string
	Image     string
	Status    string
	CreatedAt time.Time
	Labels    map[string]string
}

// CLI implements Docker using the local docker CLI.
type CLI struct{}

func dockerOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	return cmd.CombinedOutput()
}

func (CLI) Run(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = bytes.NewBuffer(nil)
	cmd.Stderr = bytes.NewBuffer(nil)
	return cmd.Run()
}

func (CLI) Exec(args ...string) error { return (&CLI{}).Run(append([]string{"exec"}, args...)...) }

func (CLI) CP(src, dst string) error { return (&CLI{}).Run("cp", src, dst) }

func runDocker(args ...string) error {
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

// VolumeCreate ensures a named volume exists, returning true when it was
// newly created (i.e. it did not already exist).
func (CLI) VolumeCreate(name string) (bool, error) {
	out, err := dockerOutput("volume", "ls", "-q")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Fields(string(out)) {
		if line == name {
			return false, nil
		}
	}
	if err := runDocker("volume", "create", name); err != nil {
		return false, err
	}
	return true, nil
}

// CopyHostToVolume copies a host path into a named volume using a throwaway
// helper container. When volumePath is empty the host path is a directory and
// its contents are copied into the volume root; otherwise it is treated as a
// single file copied to volumePath within the volume.
func (CLI) CopyHostToVolume(hostPath, volume, volumePath string) error {
	if volumePath == "" {
		return runDocker("run", "--rm", "-v", volume+":/data", "-v", hostPath+":/src", "claudex", "sh", "-c", "cp -a /src/. /data/")
	}
	return runDocker("run", "--rm", "-v", volume+":/data", "-v", hostPath+":/src", "claudex", "sh", "-c", "cp /src /data/"+volumePath)
}

// CopyVolumeToHost copies from a named volume to a host path.
func (CLI) CopyVolumeToHost(volume, volumePath, hostPath string) error {
	if volumePath == "" {
		return runDocker("run", "--rm", "-v", volume+":/data", "-v", hostPath+":/src", "claudex", "sh", "-c", "cp -a /data/. /src/")
	}
	return runDocker("run", "--rm", "-v", volume+":/data", "-v", hostPath+":/src", "claudex", "sh", "-c", "cp /data/"+volumePath+" /src")
}

func (CLI) Start(name string) error { return (&CLI{}).Run("start", name) }

func (CLI) Remove(name string, force bool) error {
	if force {
		return (&CLI{}).Run("rm", "-f", name)
	}
	return (&CLI{}).Run("rm", name)
}

func (CLI) ImageExists(tag string) (bool, error) {
	out, err := dockerOutput("images", "-q", tag)
	if err != nil {
		return false, fmt.Errorf("docker images check failed: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

func (CLI) Build(tag, contextDir string, opts BuildOptions) error {
	args := []string{"build", "-t", tag}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if len(opts.BuildArgs) > 0 {
		keys := make([]string, 0, len(opts.BuildArgs))
		for k := range opts.BuildArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, opts.BuildArgs[k]))
		}
	}
	args = append(args, contextDir)
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (CLI) ExecInteractive(name string, cmdArgs []string, in io.Reader, out, errOut io.Writer) error {
	args := append([]string{"exec", "-it", name}, cmdArgs...)
	cmd := exec.Command("docker", args...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}

func (CLI) ExecOutput(name string, cmdArgs []string) ([]byte, error) {
	args := append([]string{"exec", name}, cmdArgs...)
	return dockerOutput(args...)
}

func (CLI) Logs(name string, tail int) ([]byte, error) {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tail))
	}
	args = append(args, name)
	return dockerOutput(args...)
}

func (CLI) PS(includeStopped bool) ([]string, error) {
	args := []string{"ps", "--format", "{{.Names}}"}
	if includeStopped {
		args = append(args, "-a")
	}
	out, err := dockerOutput(args...)
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %v: %s", err, string(out))
	}
	lines := strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' || r == '\r' })
	var res []string
	for _, n := range lines {
		n = strings.TrimSpace(n)
		if n != "" {
			res = append(res, n)
		}
	}
	return res, nil
}

func (CLI) Inspect(name string) (Container, error) {
	out, err := dockerOutput("inspect", name)
	if err != nil {
		return Container{}, fmt.Errorf("docker inspect %s failed: %v: %s", name, err, string(out))
	}
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err != nil {
		return Container{}, err
	}
	if len(arr) == 0 {
		return Container{}, fmt.Errorf("no such container: %s", name)
	}
	raw := arr[0]
	var state string
	if st, ok := raw["State"].(map[string]any); ok {
		if run, ok := st["Running"].(bool); ok {
			if run {
				state = "running"
			} else {
				state = "exited"
			}
		}
	}
	var createdAt time.Time
	if s, ok := raw["Created"].(string); ok {
		t, _ := time.Parse(time.RFC3339Nano, s)
		createdAt = t
	}
	labels := map[string]string{}
	if cfg, ok := raw["Config"].(map[string]any); ok {
		if l, ok := cfg["Labels"].(map[string]any); ok {
			for k, v := range l {
				if s, ok := v.(string); ok {
					labels[k] = s
				}
			}
		}
	}
	image := ""
	if c, ok := raw["Config"].(map[string]any); ok {
		if s, ok := c["Image"].(string); ok {
			image = s
		}
	}
	id := ""
	if s, ok := raw["Id"].(string); ok {
		id = s
	}
	return Container{ID: id, Name: name, Image: image, Status: state, CreatedAt: createdAt, Labels: labels}, nil
}
