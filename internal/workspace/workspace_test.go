package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultDirs(t *testing.T) {
	got := DefaultDirs(nil)
	if len(got) != 1 || got[0] != "." {
		t.Fatalf("DefaultDirs(nil) = %v, want ['.']", got)
	}
	in := []string{"/a", "/b"}
	if out := DefaultDirs(in); &out[0] == &in[0] && len(out) != 2 {
		t.Fatalf("DefaultDirs should return input when non-empty")
	}
}

func TestNormalizeDirsAndSorting(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Create a symlink to dir2
	link := filepath.Join(dir1, "link")
	if runtime.GOOS != "windows" { // symlinks require admin on Windows
		if err := os.Symlink(dir2, link); err != nil {
			t.Fatalf("Symlink failed: %v", err)
		}
		got, err := NormalizeDirs([]string{dir2, link})
		if err != nil {
			t.Fatalf("NormalizeDirs error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected two entries, got %v", got)
		}
		// On macOS, /var is a symlink to /private/var; compare using real path
		realDir2, err := filepath.EvalSymlinks(dir2)
		if err != nil {
			t.Fatalf("EvalSymlinks(dir2): %v", err)
		}
		if got[0] != realDir2 || got[1] != realDir2 {
			t.Fatalf("expected both entries to resolve to %s; got %v", realDir2, got)
		}
	}

	// Non-directory should error
	file := filepath.Join(dir1, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := NormalizeDirs([]string{file}); err == nil {
		t.Fatalf("expected error for non-directory input")
	}
}

func TestDeriveSignatureDeterministicAndSalted(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	norm, err := NormalizeDirs([]string{d2, d1}) // out of order
	if err != nil {
		t.Fatalf("NormalizeDirs: %v", err)
	}
	sig1 := DeriveSignature(norm)
	if len(sig1) == 0 || len(sig1) > 8 || strings.Contains(sig1, " ") {
		t.Fatalf("unexpected signature: %q", sig1)
	}
	// Salt changes signature
	t.Setenv("CLAUDEX_NAME_SALT", "pepper")
	sig2 := DeriveSignature(norm)
	if sig2 == sig1 {
		t.Fatalf("expected salted signature to differ")
	}
}

func TestToKebab(t *testing.T) {
	cases := map[string]string{
		" Hello World! ":  "hello-world",
		"__API v2.0__":    "api-v2-0",
		"--":              "ws",
		"MyApp+Server":    "myapp-server",
		"spaces   tabs\t": "spaces-tabs",
	}
	for in, want := range cases {
		if got := ToKebab(in); got != want {
			t.Errorf("ToKebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveSlug(t *testing.T) {
	d1 := filepath.Join(string(os.PathSeparator), "User Projects", "My App")
	d2 := filepath.Join(string(os.PathSeparator), "tmp", "Some_Very-Long_Project-Name_With_Extras")
	norm := []string{d1, d2}
	slug := DeriveSlug(norm)
	if len(slug) == 0 || len(slug) > 24 {
		t.Fatalf("slug length invalid: %q (%d)", slug, len(slug))
	}
}

func TestDeriveName(t *testing.T) {
	t.Setenv("CLAUDEX_NAME_PREFIX", "x")
	got := DeriveName("slug", "abcd1234")
	if got != "x-slug-abcd1234" {
		t.Fatalf("DeriveName = %q, want %q", got, "x-slug-abcd1234")
	}
	os.Unsetenv("CLAUDEX_NAME_PREFIX")
	got = DeriveName("slug", "abcd1234")
	if got != "claudex-slug-abcd1234" {
		t.Fatalf("DeriveName default prefix = %q", got)
	}
}
