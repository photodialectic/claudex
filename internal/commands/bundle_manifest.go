package commands

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/photodialectic/claudex/internal/harness"
	"github.com/photodialectic/claudex/internal/run"
)

const bundleFormatVersion = 1

type bundleArtifact struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type bundleManifest struct {
	FormatVersion  int                       `json:"format_version"`
	BundleID       string                    `json:"bundle_id"`
	ClaudexVersion string                    `json:"claudex_version"`
	ImageID        string                    `json:"image_id"`
	ImageTag       string                    `json:"image_tag"`
	Architecture   string                    `json:"architecture"`
	Volumes        []string                  `json:"volumes"`
	ImageArchive   string                    `json:"image_archive"`
	HostArchive    string                    `json:"host_archive,omitempty"`
	VolumeArchives map[string]string         `json:"volume_archives"`
	Files          map[string]bundleArtifact `json:"files"`
}

func selectedVolumeNames(names []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, h := range harness.Registry() {
		for _, m := range h.Mounts {
			allowed[m.Volume] = true
		}
	}
	if len(names) == 0 {
		out := make([]string, 0, len(allowed))
		for n := range allowed {
			out = append(out, n)
		}
		sort.Strings(out)
		return out, nil
	}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !allowed[name] {
			return nil, fmt.Errorf("unknown harness volume %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate volume %q", name)
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func canonicalVolumeArchivePath(volume string) string { return "volumes/" + volume + ".tar.gz" }

func volumeArchivePath(m bundleManifest, volume string) string {
	if path := m.VolumeArchives[volume]; path != "" {
		return path
	}
	return canonicalVolumeArchivePath(volume)
}

func knownBundleFiles() map[string]bool {
	known := map[string]bool{}
	for _, platform := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"} {
		known["bin/claudex-"+platform] = true
	}
	return known
}

func validateManifest(m bundleManifest) error {
	if m.FormatVersion != bundleFormatVersion {
		return fmt.Errorf("unsupported bundle format version %d", m.FormatVersion)
	}
	if len(m.BundleID) != 32 {
		return fmt.Errorf("invalid bundle ID")
	}
	if m.BundleID != strings.ToLower(m.BundleID) {
		return fmt.Errorf("bundle ID must be lowercase hexadecimal")
	}
	if _, err := hex.DecodeString(m.BundleID); err != nil {
		return fmt.Errorf("invalid bundle ID: %w", err)
	}
	if m.ImageTag != run.BundleImageTag(m.BundleID) {
		return fmt.Errorf("invalid bundle image tag")
	}
	if m.ImageID == "" || m.Architecture == "" {
		return fmt.Errorf("manifest is missing image identity or architecture")
	}
	if !versionedArtifactPath(m.ImageArchive, "image/claudex-", ".tar.gz") {
		return fmt.Errorf("manifest is missing a valid image archive path")
	}
	if _, ok := m.Files[m.ImageArchive]; !ok {
		return fmt.Errorf("manifest image archive is not checksummed")
	}
	if m.HostArchive != "" {
		if !versionedArtifactPath(m.HostArchive, "host/claudex-home-", ".tar.gz") {
			return fmt.Errorf("invalid host archive path")
		}
		if _, ok := m.Files[m.HostArchive]; !ok {
			return fmt.Errorf("manifest host archive is not checksummed")
		}
	}
	known := knownBundleFiles()
	known[m.ImageArchive] = true
	if m.HostArchive != "" {
		known[m.HostArchive] = true
	}
	for volume, path := range m.VolumeArchives {
		if !containsString(m.Volumes, volume) || !versionedArtifactPath(path, "volumes/"+volume+"-", ".tar.gz") {
			return fmt.Errorf("invalid volume archive mapping for %q", volume)
		}
		known[path] = true
	}
	for path, artifact := range m.Files {
		binaryOK := false
		for binary := range knownBundleFiles() {
			if strings.HasPrefix(path, binary+"-") && versionedArtifactPath(path, binary+"-", "") {
				binaryOK = true
			}
		}
		if (!known[path] && !binaryOK) || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "..") {
			return fmt.Errorf("invalid bundle file path %q", path)
		}
		if artifact.Size < 0 || len(artifact.SHA256) != 64 {
			return fmt.Errorf("invalid artifact record for %q", path)
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return fmt.Errorf("invalid checksum for %q", path)
		}
		if suffix := artifactPathHash(path); suffix == "" || !strings.HasPrefix(artifact.SHA256, suffix) {
			return fmt.Errorf("artifact path checksum does not match record for %q", path)
		}
	}
	allowed := map[string]bool{}
	for _, name := range allHarnessVolumes() {
		allowed[name] = true
	}
	seenVolumes := map[string]bool{}
	for _, volume := range m.Volumes {
		if !allowed[volume] || seenVolumes[volume] {
			return fmt.Errorf("invalid or duplicate volume entry %q", volume)
		}
		seenVolumes[volume] = true
		archive, hasArchive := m.VolumeArchives[volume]
		if !hasArchive || archive == "" {
			return fmt.Errorf("manifest volume %q has no archive", volume)
		}
		if _, ok := m.Files[archive]; !ok {
			return fmt.Errorf("manifest volume %q archive is not checksummed", volume)
		}
	}
	if len(m.VolumeArchives) != len(m.Volumes) {
		return fmt.Errorf("manifest volume archive inventory does not match volumes")
	}
	return nil
}

func versionedArtifactPath(path, prefix, suffix string) bool {
	if filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path || !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	checksum := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return len(checksum) == 12 && isHex(checksum)
}

func artifactPathHash(path string) string {
	base := filepath.Base(filepath.FromSlash(path))
	base = strings.TrimSuffix(base, ".tar.gz")
	index := strings.LastIndex(base, "-")
	if index < 0 || len(base)-index-1 != 12 {
		return ""
	}
	value := base[index+1:]
	if !isHex(value) {
		return ""
	}
	return value
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func allHarnessVolumes() []string {
	volumes, _ := selectedVolumeNames(nil)
	return volumes
}

func readManifest(bundleDir string) (bundleManifest, error) {
	if err := checkBundleFile(bundleDir, "manifest.json"); err != nil {
		return bundleManifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(bundleDir, "manifest.json"))
	if err != nil {
		return bundleManifest{}, err
	}
	var m bundleManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if err := validateManifest(m); err != nil {
		return m, err
	}
	for path := range m.Files {
		if err := checkBundleFile(bundleDir, path); err != nil {
			return m, err
		}
	}
	return m, nil
}

func ensureBundleDirectory(root, relative string) error {
	if relative == "." || relative == "" {
		return nil
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid bundle directory %q", relative)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("bundle path component %s is not a real directory", current)
		}
	}
	return nil
}

func checkBundleFile(root, relative string) error {
	if filepath.IsAbs(relative) || filepath.Clean(filepath.FromSlash(relative)) != filepath.FromSlash(relative) || strings.HasPrefix(relative, "..") {
		return fmt.Errorf("invalid bundle path %q", relative)
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("bundle file %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle path %s contains a symlink", relative)
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("bundle path %s is not a regular file", relative)
			}
		} else if !info.IsDir() {
			return fmt.Errorf("bundle path component %s is not a directory", current)
		}
	}
	return nil
}

func verifyBundle(bundleDir string, m bundleManifest) error {
	paths := make([]string, 0, len(m.Files))
	for path := range m.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		want := m.Files[path]
		got, err := hashFileWithProgress(filepath.Join(bundleDir, filepath.FromSlash(path)), path, want.Size)
		if err != nil {
			return fmt.Errorf("verify %s: %w", path, err)
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 {
			return fmt.Errorf("bundle checksum or size mismatch for %s", path)
		}
	}
	return nil
}
