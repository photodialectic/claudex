package buildctx

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed Dockerfile init-firewall.sh CLAUDEX.md google-docs-mcp/**
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

	// Copy embedded MCP server files/directories
	err = fs.WalkDir(dockerContextFS, "google-docs-mcp", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(tmpDir, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := dockerContextFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read embedded %s: %w", path, err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return fmt.Errorf("cannot write %s to temp dir: %w", path, err)
		}
		return nil
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	cleanup := func() error { return os.RemoveAll(tmpDir) }
	return tmpDir, cleanup, nil
}
