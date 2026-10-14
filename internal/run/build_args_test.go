package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/photodialectic/claudex/internal/version"
)

func TestBuildRunArgsLabelsAndMounts(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()

	o := Options{Normalized: []string{d1, d2}, Signature: "abcd1234", Slug: "slug", Name: "claudex-slug-abcd1234"}
	args, err := o.BuildRunArgs()
	if err != nil {
		t.Fatalf("BuildRunArgs: %v", err)
	}
	// Must include volume mounts for both directories
	m1 := d1 + ":/workspace/" + filepath.Base(d1)
	m2 := d2 + ":/workspace/" + filepath.Base(d2)
	if !contains(args, m1) || !contains(args, m2) {
		t.Fatalf("missing mounts in args: %v", args)
	}
	// Must include labels for signature, slug, and version
	if !contains(args, "com.claudex.signature="+o.Signature) || !contains(args, "com.claudex.slug="+o.Slug) || !contains(args, "com.claudex.version="+version.Version) {
		t.Fatalf("missing labels in args: %v", args)
	}
	// Mounts label should be JSON of normalized dirs
	b, _ := json.Marshal(o.Normalized)
	if !contains(args, "com.claudex.mounts="+string(b)) {
		t.Fatalf("missing mounts label in args: %v", args)
	}
	// Final command should be tail -f /dev/null to keep container running
	if !(len(args) >= 4 && args[len(args)-4] == "claudex" && args[len(args)-3] == "tail" && args[len(args)-2] == "-f" && args[len(args)-1] == "/dev/null") {
		t.Fatalf("expected trailing [claudex tail -f /dev/null], got %v", args[max(0, len(args)-4):])
	}
}

func TestBuildRunArgsShadowsEnvFilesAndHostState(t *testing.T) {
	ws := t.TempDir()
	base := filepath.Base(ws)
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "sub", ".env.local"), []byte("SECRET=2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".env.example"), []byte("FOO=bar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "node_modules", ".env"), []byte("SECRET=3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := Options{Normalized: []string{ws}, Signature: "abcd1234", Slug: "slug", Name: "claudex-slug-abcd1234"}
	args, err := o.BuildRunArgs()
	if err != nil {
		t.Fatalf("BuildRunArgs: %v", err)
	}

	// .git and node_modules are covered with empty anonymous volumes...
	if !contains(args, "/workspace/"+base+"/.git") || !contains(args, "/workspace/"+base+"/node_modules") {
		t.Fatalf("missing empty-volume mounts for .git/node_modules: %v", args)
	}
	// ...and their contents are not walked further
	if contains(args, "/workspace/"+base+"/node_modules/left-pad") {
		t.Fatalf("node_modules must not be descended into: %v", args)
	}
	if mountSource(args, "/workspace/"+base+"/node_modules/.env:ro") != "" {
		t.Fatalf(".env inside node_modules must not be shadowed: %v", args)
	}

	// .env and nested .env.local are shadowed read-only with one shared empty file
	src1 := mountSource(args, "/workspace/"+base+"/.env:ro")
	src2 := mountSource(args, "/workspace/"+base+"/sub/.env.local:ro")
	if src1 == "" || src1 != src2 {
		t.Fatalf("expected one shared empty shadow source for both env files: %v", args)
	}
	if !strings.HasSuffix(src1, filepath.Join("claudex", "empty")) {
		t.Fatalf("unexpected shadow source %q", src1)
	}

	// .env.example stays visible
	for _, a := range args {
		if strings.Contains(a, ".env.example") {
			t.Fatalf(".env.example must not be hidden: %v", args)
		}
	}
}

func TestBuildRunArgsSkipsUnreadableEntries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: unreadable directories are still readable")
	}
	ws := t.TempDir()
	base := filepath.Base(ws)
	locked := filepath.Join(ws, "locked")
	if err := os.MkdirAll(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	o := Options{Normalized: []string{ws}, Signature: "abcd1234", Slug: "slug", Name: "claudex-slug-abcd1234"}
	args, err := o.BuildRunArgs()
	if err != nil {
		t.Fatalf("BuildRunArgs must not fail on unreadable entries: %v", err)
	}
	// The readable .env is still shadowed...
	if mountSource(args, "/workspace/"+base+"/.env:ro") == "" {
		t.Fatalf("missing .env shadow: %v", args)
	}
	// ...and the unreadable directory is hidden whole rather than exposed.
	if !contains(args, "/workspace/"+base+"/locked") {
		t.Fatalf("unreadable directory must be hidden: %v", args)
	}
}

// mountSource returns the host source of the -v argument whose target spec is
// dst (e.g. "/workspace/ws/.env:ro"), or "" when no such mount exists.
func mountSource(args []string, dst string) string {
	for i, a := range args {
		if a == "-v" && i+1 < len(args) {
			parts := strings.SplitN(args[i+1], ":", 2)
			if len(parts) == 2 && parts[1] == dst {
				return parts[0]
			}
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
