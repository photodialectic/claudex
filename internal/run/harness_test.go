package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/harness"
)

func TestConfigMountArgsCoversAllVolumes(t *testing.T) {
	args := configMountArgs()
	if !contains(args, "claudex-claude:/home/node/.claude") {
		t.Fatalf("missing claude dir mount: %v", args)
	}
	if !contains(args, "claudex-claude-json:/home/node/.claude-state") {
		t.Fatalf("missing claude json mount: %v", args)
	}
	if !contains(args, "claudex-opencode-config:/home/node/.config/opencode") {
		t.Fatalf("missing opencode config mount: %v", args)
	}
}

func TestConfigEnvArgsOnlySetVars(t *testing.T) {
	for _, k := range harness.EnvVars() {
		os.Unsetenv(k)
	}
	t.Setenv("OPENAI_API_KEY", "sk-test")
	injected := configEnvArgs()
	if !contains(injected, "-e") || !contains(injected, "OPENAI_API_KEY") {
		t.Fatalf("expected -e OPENAI_API_KEY, got %v", injected)
	}
	if len(injected) != 2 {
		t.Fatalf("expected exactly one -e pair, got %v", injected)
	}
	if contains(injected, "GEMINI_API_KEY") {
		t.Fatalf("did not expect unset GEMINI_API_KEY, got %v", injected)
	}
}

func TestPrepareConfigVolumesSeedsOnlyNewWithHostSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	f := &dockerx.Fake{VolumeCreateCreated: true}
	seeds, err := prepareConfigVolumes(f)
	if err != nil {
		t.Fatalf("prepareConfigVolumes: %v", err)
	}

	// Only .claude (dir) and .claude.json (file) have host sources.
	if len(seeds) != 2 {
		t.Fatalf("expected 2 seeds, got %d: %v", len(seeds), seeds)
	}

	var out, errOut bytes.Buffer
	seedVolumes(f, seeds, &out, &errOut)
	if len(f.CopyHostToVolumeCalls) != 2 {
		t.Fatalf("expected 2 copies, got %d", len(f.CopyHostToVolumeCalls))
	}
}
