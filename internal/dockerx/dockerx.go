package dockerx

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "os/exec"
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
    Build(tag, contextDir string, noCache bool) error
    ExecInteractive(name string, cmd []string, in io.Reader, out, errOut io.Writer) error
    ExecOutput(name string, cmd []string) ([]byte, error)
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

func (CLI) Build(tag, contextDir string, noCache bool) error {
    args := []string{"build", "-t", tag}
    if noCache {
        args = append(args, "--no-cache")
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
