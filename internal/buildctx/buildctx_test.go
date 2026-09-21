package buildctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBuildContextInjectsHarnessLayers(t *testing.T) {
	dir, cleanup, err := PrepareBuildContext()
	if err != nil {
		t.Fatalf("PrepareBuildContext: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	rendered := string(data)

	if strings.Contains(rendered, "# CLAUDEX_HARNESS_LAYERS") {
		t.Fatalf("marker was not replaced:\n%s", rendered)
	}
	for _, arg := range []string{"CLAUDEX_TOOL_claude", "CLAUDEX_TOOL_codex", "CLAUDEX_TOOL_opencode"} {
		if !strings.Contains(rendered, arg) {
			t.Fatalf("missing generated ARG %s in Dockerfile:\n%s", arg, rendered)
		}
	}
	if strings.Contains(rendered, "CLAUDEX_BUILD_VERSION") {
		t.Fatalf("old monolithic build arg should be gone:\n%s", rendered)
	}
	if strings.Contains(rendered, "# CLAUDEX_HARNESS_DIRS") {
		t.Fatalf("mount dirs marker was not replaced:\n%s", rendered)
	}
	for _, dir := range []string{"/home/node/.pi", "/home/node/.config/opencode", "/home/node/.local/share/opencode"} {
		if !strings.Contains(rendered, dir) {
			t.Fatalf("missing generated mount dir %s in Dockerfile:\n%s", dir, rendered)
		}
	}
}
