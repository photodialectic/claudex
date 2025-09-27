package buildctx

import (
    "embed"
    "fmt"
    "os"
    "path/filepath"
)

//go:embed Dockerfile init-firewall.sh CLAUDEX.md
var dockerContextFS embed.FS

// PrepareBuildContext writes embedded files to a temp directory and returns its path
// together with a cleanup function that removes the directory.
func PrepareBuildContext() (string, func() error, error) {
    tmpDir, err := os.MkdirTemp("", "claudex-build-")
    if err != nil {
        return "", nil, fmt.Errorf("cannot create temp build dir: %w", err)
    }
    files := []string{"Dockerfile", "init-firewall.sh", "CLAUDEX.md"}
    for _, name := range files {
        data, err := dockerContextFS.ReadFile(name)
        if err != nil {
            os.RemoveAll(tmpDir)
            return "", nil, fmt.Errorf("cannot read embedded %s: %w", name, err)
        }
        outPath := filepath.Join(tmpDir, name)
        if err := os.WriteFile(outPath, data, 0644); err != nil {
            os.RemoveAll(tmpDir)
            return "", nil, fmt.Errorf("cannot write %s to temp dir: %w", name, err)
        }
    }
    cleanup := func() error { return os.RemoveAll(tmpDir) }
    return tmpDir, cleanup, nil
}

