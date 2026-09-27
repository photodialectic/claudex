package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/photodialectic/claudex/internal/dockerx"
)

func TestProgressDisplayAggregatesConcurrentStreams(t *testing.T) {
	var output bytes.Buffer
	display := newProgressDisplay(&output, true)
	first := display.start("image.tar.gz", 200<<20)
	second := display.start("volumes/codex.tar.gz", 0)
	first.add(100 << 20)
	second.add(20 << 20)
	first.finish(true)
	second.finish(false)
	text := output.String()
	if !strings.Contains(text, "streams") || !strings.Contains(text, "image.tar.gz") || !strings.Contains(text, "✓") || !strings.Contains(text, "✗") {
		t.Fatalf("progress output did not show aggregate and completion states: %q", text)
	}
}

func TestProgressDisplayLogsMilestonesWithoutTerminal(t *testing.T) {
	var output bytes.Buffer
	display := newProgressDisplay(&output, false)
	task := display.start("large.tar.gz", 0)
	task.add(progressTick + 1)
	task.finish(true)
	if !strings.Contains(output.String(), "128.0 MiB written") {
		t.Fatalf("expected a byte milestone for non-terminal output, got %q", output.String())
	}
}

func TestArchiveVolumesUsesBoundedConcurrency(t *testing.T) {
	volumes := allHarnessVolumes()[:4]
	available := map[string]map[string]string{}
	for _, volume := range volumes {
		available[volume] = map[string]string{}
	}
	var active, maximum atomic.Int32
	dx := &dockerx.Fake{Volumes: available}
	dx.TarVolumeFn = func(_, volume string, dst io.Writer) error {
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		err := writeTestVolumeTar(dst, []byte(volume))
		active.Add(-1)
		return err
	}
	root := t.TempDir()
	results, err := archiveVolumes(root, "claudex-pack:test", volumes, dx, 3)
	if err != nil {
		t.Fatalf("archive volumes: %v", err)
	}
	if len(results) != len(volumes) {
		t.Fatalf("archived %d volumes, want %d", len(results), len(volumes))
	}
	if got := maximum.Load(); got < 2 || got > 3 {
		t.Fatalf("observed %d simultaneous exports, want between 2 and 3", got)
	}
	for _, result := range results {
		if err := validateVolumeArchive(filepath.Join(root, filepath.FromSlash(result.path)), result.volume); err != nil {
			t.Fatalf("archive for %s is invalid: %v", result.volume, err)
		}
	}
}

func TestArchiveVolumesStopsSchedulingAfterFatalError(t *testing.T) {
	volumes := allHarnessVolumes()
	available := map[string]map[string]string{}
	for _, volume := range volumes {
		available[volume] = map[string]string{}
	}
	var archived []string
	dx := &dockerx.Fake{Volumes: available}
	dx.TarVolumeFn = func(_, volume string, dst io.Writer) error {
		archived = append(archived, volume)
		if volume == volumes[0] {
			return errors.New("fatal archive error")
		}
		return writeTestVolumeTar(dst, []byte(volume))
	}
	_, err := archiveVolumes(t.TempDir(), "claudex-pack:test", volumes, dx, 1)
	if err == nil {
		t.Fatal("expected a fatal archive error")
	}
	if len(archived) != 1 || archived[0] != volumes[0] {
		t.Fatalf("exports continued after fatal error: %v", archived)
	}
}

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
