package run

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"claudex/internal/version"
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
