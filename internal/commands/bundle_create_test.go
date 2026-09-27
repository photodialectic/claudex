package commands

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/photodialectic/claudex/internal/dockerx"
)

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
	results, err := archiveVolumes(root, "claudex-bundle:test", volumes, dx, 3)
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
	_, err := archiveVolumes(t.TempDir(), "claudex-bundle:test", volumes, dx, 1)
	if err == nil {
		t.Fatal("expected a fatal archive error")
	}
	if len(archived) != 1 || archived[0] != volumes[0] {
		t.Fatalf("exports continued after fatal error: %v", archived)
	}
}

func TestCrossCompileEnvOverridesToolchainVars(t *testing.T) {
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("CLAUDEX_TEST_PASSTHROUGH", "keep")
	env := crossCompileEnv("darwin", "arm64")
	goos, goarch, cgo := "", "", ""
	passthrough := false
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "GOOS="):
			goos = entry
		case strings.HasPrefix(entry, "GOARCH="):
			goarch = entry
		case strings.HasPrefix(entry, "CGO_ENABLED="):
			cgo = entry
		case entry == "CLAUDEX_TEST_PASSTHROUGH=keep":
			passthrough = true
		}
	}
	if goos != "GOOS=darwin" || goarch != "GOARCH=arm64" || cgo != "CGO_ENABLED=0" {
		t.Fatalf("toolchain variables not overridden: %v", env)
	}
	if !passthrough {
		t.Fatalf("unrelated environment variables were dropped: %v", env)
	}
}
