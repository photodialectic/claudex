package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateVolumeArchiveAcceptsDockerTarDirectoryNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker-volume.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "./plans/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "./plans/todo.md", Typeflag: tar.TypeReg, Mode: 0600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateVolumeArchive(path, "claudex-claude"); err != nil {
		t.Fatalf("valid Docker tar directory names rejected: %v", err)
	}
}

func TestValidateVolumeArchiveAllowsAbsoluteLinkWithinMountOnly(t *testing.T) {
	archive := func(entry, target string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "link.tar.gz")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(file)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: entry, Linkname: target, Typeflag: tar.TypeSymlink, Mode: 0777}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	internal := archive("./debug/latest", "/home/node/.claude/debug/session.log")
	if err := validateVolumeArchive(internal, "claudex-claude"); err != nil {
		t.Fatalf("absolute symlink inside mounted volume rejected: %v", err)
	}
	external := archive("./debug/latest", "/etc/passwd")
	if err := validateVolumeArchive(external, "claudex-claude"); err == nil {
		t.Fatal("absolute symlink outside mounted volume accepted")
	}
	codexShim := archive("./tmp/arg0/codex-arg0test/applypatch", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/codex/codex")
	if err := validateVolumeArchive(codexShim, "claudex-codex"); err != nil {
		t.Fatalf("Codex's image-binary shim was rejected: %v", err)
	}
	execveShim := archive("./tmp/arg0/codex-arg0oPZG79/codex-execve-wrapper", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/codex/codex")
	if err := validateVolumeArchive(execveShim, "claudex-codex"); err != nil {
		t.Fatalf("Codex's execve shim was rejected: %v", err)
	}
	codexVendorShim := archive("./tmp/path/codex-arg0X2OzGS/codex-linux-sandbox", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/vendor/aarch64-unknown-linux-musl/codex/codex")
	if err := validateVolumeArchive(codexVendorShim, "claudex-codex"); err != nil {
		t.Fatalf("Codex's installed vendor-binary shim was rejected: %v", err)
	}
	unapprovedCodexDir := archive("./tmp/unrelated/codex-arg0X2OzGS/codex-linux-sandbox", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/vendor/aarch64-unknown-linux-musl/codex/codex")
	if err := validateVolumeArchive(unapprovedCodexDir, "claudex-codex"); err == nil {
		t.Fatal("Codex shim in an unexpected directory was accepted")
	}
	unapprovedCodexLink := archive("./credentials", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-linux-arm64/vendor/secret")
	if err := validateVolumeArchive(unapprovedCodexLink, "claudex-codex"); err == nil {
		t.Fatal("absolute Codex-package link with an unapproved entry path was accepted")
	}
	unapprovedBinary := archive("./tmp/path/codex-arg0X2OzGS/codex-linux-sandbox", "/usr/local/share/npm-global/lib/node_modules/@openai/codex/vendor/aarch64-unknown-linux-musl/codex/other")
	if err := validateVolumeArchive(unapprovedBinary, "claudex-codex"); err == nil {
		t.Fatal("Codex shim pointing to a non-Codex executable was accepted")
	}
}

func TestValidateHostArchiveRejectsTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malicious.tar.gz")
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../outside", Mode: 0600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateHostArchive(path); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestValidateVolumeArchiveRejectsEscapingSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malicious-volume.tar.gz")
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "config/link", Linkname: "../../outside", Typeflag: tar.TypeSymlink, Mode: 0777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateVolumeArchive(path, "claudex-claude"); err == nil {
		t.Fatal("expected escaping symlink to be rejected")
	}
}
