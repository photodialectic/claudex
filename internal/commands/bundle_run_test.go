package commands

import (
	"reflect"
	"strings"
	"testing"

	"github.com/photodialectic/claudex/internal/run"
)

func TestParseBundleRunArgs(t *testing.T) {
	args := []string{"/volumes/claudex", "--refresh", "--write-back", "--no-verify", "--host-network", "--network", "claudexnet", "/work", "--name", "session", "--ca-cert", "/tmp/ca.pem", "--no-git", "--firewall", "--replace", "--parallel", "--strict-mounts"}
	o, err := parseBundleRunArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.bundleDir != "/volumes/claudex" {
		t.Fatalf("bundleDir = %q", o.bundleDir)
	}
	if !o.refresh || !o.writeBack || !o.noVerify {
		t.Fatalf("bundle run flags not parsed: %+v", o)
	}
	want := []string{"--host-network", "--network", "claudexnet", "/work", "--name", "session", "--ca-cert", "/tmp/ca.pem", "--no-git", "--firewall", "--replace", "--parallel", "--strict-mounts"}
	if !reflect.DeepEqual(o.runArgs, want) {
		t.Fatalf("runArgs = %v, want %v", o.runArgs, want)
	}
}

func TestParseBundleRunArgsErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing bundle dir", nil, "usage: claudex bundle run"},
		{"run flag before bundle dir", []string{"--host-network", "/volumes/claudex"}, "bundle directory must be the first bundle run argument"},
		{"unknown option", []string{"/volumes/claudex", "--bogus"}, "unknown bundle run option"},
		{"missing option value", []string{"/volumes/claudex", "--network"}, "requires a value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBundleRunArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parse(%v) error = %v, want %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestMaterializeBundleVolumeRejectsForeignVolume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	original := manifest.Volumes[0]
	name := run.BundleVolumeName(manifest.BundleID, original)
	dx.Volumes[name] = map[string]string{"unrelated": "label"}

	err = materializeBundleVolume(dx, bundle, manifest, original, name, false)
	if err == nil {
		t.Fatal("expected materialize to reject a volume owned by another bundle")
	}
	if !strings.Contains(err.Error(), "not owned by bundle") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dx.VolumeCreateLabels) != 0 {
		t.Fatalf("foreign volume was replaced: %v", dx.VolumeCreateLabels)
	}
	if len(dx.VolumeRemoveCalls) != 0 {
		t.Fatalf("foreign volume was removed: %v", dx.VolumeRemoveCalls)
	}
}
