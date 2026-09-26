package buildctx

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/photodialectic/claudex/internal/harness"
)

//go:embed Dockerfile init-firewall.sh CLAUDEX.md .tmux.conf .vimrc
var dockerContextFS embed.FS

// PrepareBuildContext writes embedded files to a temp directory and returns its path
// together with a cleanup function that removes the directory.
func PrepareBuildContext() (string, func() error, error) {
	tmpDir, err := os.MkdirTemp("", "claudex-build-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create temp build dir: %w", err)
	}
	files := []string{"Dockerfile", "init-firewall.sh", "CLAUDEX.md", ".tmux.conf", ".vimrc"}
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

	// Inject generated per-tool install layers into the Dockerfile template.
	if err := injectHarnessLayers(tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	cleanup := func() error { return os.RemoveAll(tmpDir) }
	return tmpDir, cleanup, nil
}

// injectHarnessLayers replaces the Dockerfile markers with the generated
// per-tool install layers and the harness config directory directive.
func injectHarnessLayers(tmpDir string) error {
	path := filepath.Join(tmpDir, "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read generated Dockerfile: %w", err)
	}
	rendered := string(data)
	rendered = strings.Replace(rendered, harness.DockerfileMarker, harness.RenderDockerfileLayers(), 1)
	rendered = strings.Replace(rendered, harness.DockerfileMountDirsMarker, harness.RenderDockerfileMountDirs(), 1)
	for _, marker := range []string{harness.DockerfileMarker, harness.DockerfileMountDirsMarker} {
		if strings.Contains(rendered, marker) {
			return fmt.Errorf("Dockerfile marker %q not found", marker)
		}
	}
	if err := os.WriteFile(path, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("cannot write generated Dockerfile: %w", err)
	}
	return nil
}
