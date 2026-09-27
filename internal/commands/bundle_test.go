package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/photodialectic/claudex/internal/dockerx"
)

const testImageID = "sha256:bundle-test-image"

func newBundleTestDocker() *dockerx.Fake {
	volumes := map[string]map[string]string{}
	for _, name := range allHarnessVolumes() {
		volumes[name] = map[string]string{}
	}
	return &dockerx.Fake{
		ImageExistsVal: true,
		ImageIDVal:     testImageID,
		ImageArchVal:   "arm64",
		LocalArchVal:   "arm64",
		Volumes:        volumes,
	}
}

func createTestBundle(t *testing.T, dx *dockerx.Fake, args ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	fullArgs := append([]string{dir, "--no-binaries", "--no-host-config"}, args...)
	if err := bundleCreateWithDocker(fullArgs, dx); err != nil {
		t.Fatalf("bundle create: %v", err)
	}
	return dir
}

func TestBundleManifestVerifyAndSelectiveInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx)

	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.BundleID == "" || manifest.ImageID != testImageID {
		t.Fatalf("unexpected image identity: %+v", manifest)
	}
	if got, want := len(manifest.Volumes), len(allHarnessVolumes()); got != want {
		t.Fatalf("created %d volume archives, want %d", got, want)
	}
	if err := verifyBundle(bundle, manifest); err != nil {
		t.Fatalf("verify bundle: %v", err)
	}

	archive := filepath.Join(bundle, filepath.FromSlash(volumeArchivePath(manifest, "claudex-claude")))
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, append(data, 'x'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBundle(bundle, manifest); err == nil {
		t.Fatal("expected tampering to fail checksum verification")
	}
	if err := os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}

	installer := &dockerx.Fake{
		ImageExistsVal: true,
		ImageIDVal:     testImageID,
		ImageArchVal:   "arm64",
		LocalArchVal:   "arm64",
	}
	if err := bundleInstallWithDocker([]string{bundle, "--volumes", "claudex-claude"}, installer); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(installer.ExtractVolumeCalls) != 1 || installer.ExtractVolumeCalls[0].Volume != "claudex-claude" {
		t.Fatalf("selective install extracted unexpected volumes: %+v", installer.ExtractVolumeCalls)
	}
}

func TestPartialBundleRejectedByRunBeforeImageChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx, "--volumes", "claudex-claude")
	initialTags := len(dx.TagImageCalls)
	if err := bundleRunWithDocker([]string{bundle}, dx); err == nil {
		t.Fatal("expected bundle run to reject a partial bundle")
	}
	if len(dx.TagImageCalls) != initialTags {
		t.Fatalf("partial bundle modified image tags: %v", dx.TagImageCalls)
	}
	if len(dx.VolumeCreateLabels) != 0 {
		t.Fatalf("partial bundle created volumes: %v", dx.VolumeCreateLabels)
	}
}

func TestBundlesWithSameImageGetIndependentIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	first := createTestBundle(t, dx, "--volumes", "claudex-claude")
	second := createTestBundle(t, dx, "--volumes", "claudex-claude")
	firstManifest, err := readManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := readManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstManifest.ImageID != secondManifest.ImageID {
		t.Fatalf("test bundles unexpectedly used different images: %q and %q", firstManifest.ImageID, secondManifest.ImageID)
	}
	if firstManifest.BundleID == secondManifest.BundleID || firstManifest.ImageTag == secondManifest.ImageTag {
		t.Fatalf("bundles with the same image shared identity: %+v %+v", firstManifest, secondManifest)
	}
}

func TestInstallArchitectureMismatchDoesNotAdoptImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bundleDocker := newBundleTestDocker()
	bundle := createTestBundle(t, bundleDocker, "--volumes", "claudex-claude")
	installer := &dockerx.Fake{
		ImageExistsVal: true,
		ImageIDVal:     testImageID,
		ImageArchVal:   "arm64",
		LocalArchVal:   "amd64",
	}
	if err := bundleInstallWithDocker([]string{bundle}, installer); err == nil {
		t.Fatal("expected architecture mismatch")
	}
	if len(installer.TagImageCalls) != 0 {
		t.Fatalf("architecture mismatch mutated image tags: %v", installer.TagImageCalls)
	}
}

func TestBundleRunMaterializesNamespacedVolumeAndDetectsStaleBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dx := newBundleTestDocker()
	dx.TarVolumeFn = func(_ string, volume string, dst io.Writer) error {
		return writeTestVolumeTar(dst, []byte("first:"+volume))
	}
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	original := "claudex-claude"
	namespaced := "claudex-pack-" + manifest.BundleID + "-" + original
	if err := materializePackVolume(dx, bundle, manifest, original, namespaced, false); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got := dx.ExtractVolumeCalls[len(dx.ExtractVolumeCalls)-1].Volume; got != namespaced {
		t.Fatalf("archive extracted into %q, want namespaced volume %q", got, namespaced)
	}

	dx.TarVolumeFn = func(_ string, volume string, dst io.Writer) error {
		return writeTestVolumeTar(dst, []byte("second:"+volume))
	}
	if err := bundleCreateWithDocker([]string{bundle, "--no-binaries", "--no-host-config"}, dx); err != nil {
		t.Fatalf("refresh source bundle: %v", err)
	}
	manifest, err = readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializePackVolume(dx, bundle, manifest, original, namespaced, false); err == nil {
		t.Fatal("expected changed archive to be detected as stale")
	}
	if err := materializePackVolume(dx, bundle, manifest, original, namespaced, true); err != nil {
		t.Fatalf("refresh namespaced volume: %v", err)
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

func TestBundleDestroyRemovesOnlyStoppedOwnedResources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var namespaced []string
	for _, original := range allHarnessVolumes() {
		name := "claudex-pack-" + manifest.BundleID + "-" + original
		namespaced = append(namespaced, name)
		_, err := dx.VolumeCreateLabeled(name, map[string]string{packLabel: manifest.BundleID})
		if err != nil {
			t.Fatal(err)
		}
	}
	containerName := "claudex-pack-" + manifest.BundleID + "-test"
	volumes := append([]string(nil), namespaced...)
	dx.Containers = map[string]dockerx.Container{
		containerName: {
			Name:    containerName,
			Image:   manifest.ImageTag,
			ImageID: testImageID,
			Status:  "exited",
			Labels:  map[string]string{packLabel: manifest.BundleID},
			Volumes: volumes,
		},
	}
	dx.PSNames = []string{containerName}
	if err := bundleDestroy([]string{bundle}, dx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(dx.RemoveCalls) != 1 || dx.RemoveCalls[0].Name != containerName {
		t.Fatalf("expected only owned stopped container removal, got %+v", dx.RemoveCalls)
	}
	for _, name := range namespaced {
		if _, ok := dx.Volumes[name]; ok {
			t.Fatalf("owned bundle volume %s was not removed", name)
		}
	}
	for _, original := range allHarnessVolumes() {
		if _, ok := dx.Volumes[original]; !ok {
			t.Fatalf("ordinary host volume %s was removed", original)
		}
	}
}

func TestWriteBackFailurePreservesCurrentManifestAndArchives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	dx.TarVolumeFn = func(string, string, io.Writer) error { return errors.New("simulated archive failure") }
	names := map[string]string{}
	for _, volume := range manifest.Volumes {
		names[volume] = "claudex-pack-" + manifest.BundleID + "-" + volume
	}
	if err := writeBackBundleVolumes(dx, bundle, manifest, names); err == nil {
		t.Fatal("expected write-back failure")
	}
	got, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, manifestBytes) {
		t.Fatal("failed write-back changed the published manifest")
	}
	if err := verifyBundle(bundle, manifest); err != nil {
		t.Fatalf("old bundle artifacts were not preserved: %v", err)
	}
}

func TestWriteBackPublishesContentAddressedArchive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	previous := manifest.VolumeArchives["claudex-claude"]
	dx.TarVolumeFn = func(_ string, volume string, dst io.Writer) error {
		return writeTestVolumeTar(dst, []byte("updated:"+volume))
	}
	names := map[string]string{}
	for _, volume := range manifest.Volumes {
		names[volume] = "claudex-pack-" + manifest.BundleID + "-" + volume
	}
	if err := writeBackBundleVolumes(dx, bundle, manifest, names); err != nil {
		t.Fatalf("write-back: %v", err)
	}
	updated, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VolumeArchives["claudex-claude"] == previous {
		t.Fatal("write-back did not publish a new content-addressed archive")
	}
	if _, err := os.Stat(filepath.Join(bundle, filepath.FromSlash(previous))); !os.IsNotExist(err) {
		t.Fatalf("old archive was not pruned after manifest commit: %v", err)
	}
	if err := verifyBundle(bundle, updated); err != nil {
		t.Fatalf("updated bundle failed verification: %v", err)
	}
}

func TestWriteBackPreservesUnchangedArchiveReference(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dx := newBundleTestDocker()
	dx.TarVolumeFn = func(_, _ string, dst io.Writer) error {
		return writeTestVolumeTar(dst, []byte("same"))
	}
	bundle := createTestBundle(t, dx)
	manifest, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	previous := manifest.VolumeArchives["claudex-claude"]
	names := map[string]string{}
	for _, volume := range manifest.Volumes {
		names[volume] = "claudex-pack-" + manifest.BundleID + "-" + volume
	}
	if err := writeBackBundleVolumes(dx, bundle, manifest, names); err != nil {
		t.Fatalf("write-back: %v", err)
	}
	updated, err := readManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VolumeArchives["claudex-claude"] != previous {
		t.Fatalf("unchanged archive path changed from %q to %q", previous, updated.VolumeArchives["claudex-claude"])
	}
	if err := verifyBundle(bundle, updated); err != nil {
		t.Fatalf("unchanged bundle failed verification: %v", err)
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

func writeTestVolumeTar(dst io.Writer, content []byte) error {
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "config", Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
