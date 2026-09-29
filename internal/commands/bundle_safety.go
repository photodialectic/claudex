package commands

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/photodialectic/claudex/internal/harness"
)

func validateHostArchive(path string) error {
	f, task, src, err := openProgressFile(path, filepath.Base(path))
	if err != nil {
		return err
	}
	defer f.Close()
	success := false
	defer func() { task.finish(success) }()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			success = true
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if name == "." || name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported entry type for %q", h.Name)
		}
		if h.Size < 0 {
			return fmt.Errorf("invalid entry size for %q", h.Name)
		}
		total += h.Size
		if total > 1<<30 {
			return fmt.Errorf("host archive exceeds 1 GiB uncompressed limit")
		}
	}
}

func validateVolumeArchive(path, volume string) error {
	f, task, src, err := openProgressFile(path, filepath.Base(path))
	if err != nil {
		return err
	}
	defer f.Close()
	success := false
	defer func() { task.finish(success) }()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	mountpoint := volumeMountpoint(volume)
	linkEntries := map[string]bool{}
	seenEntries := map[string]bool{}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			success = true
			return nil
		}
		if err != nil {
			return err
		}
		archiveName := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")
		if h.Typeflag == tar.TypeDir {
			archiveName = strings.TrimSuffix(archiveName, "/")
		}
		name := archiveName
		name = filepath.FromSlash(name)
		if name != "" && name != "." && (filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator))) {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		if seenEntries[name] {
			return fmt.Errorf("duplicate archive path %q", h.Name)
		}
		for parent := filepath.Dir(name); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			if linkEntries[parent] {
				return fmt.Errorf("archive path %q traverses link %q", h.Name, parent)
			}
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeRegA:
		case tar.TypeSymlink:
			if archiveHasDescendant(seenEntries, name) {
				return fmt.Errorf("link %q replaces an existing archive directory", h.Name)
			}
			if err := validateArchiveLink(name, h.Linkname, false, volume, mountpoint); err != nil {
				return fmt.Errorf("unsafe symlink %q: %w", h.Name, err)
			}
			linkEntries[name] = true
		case tar.TypeLink:
			if archiveHasDescendant(seenEntries, name) {
				return fmt.Errorf("link %q replaces an existing archive directory", h.Name)
			}
			if err := validateArchiveLink(name, h.Linkname, true, volume, mountpoint); err != nil {
				return fmt.Errorf("unsafe hard link %q: %w", h.Name, err)
			}
			linkEntries[name] = true
		default:
			return fmt.Errorf("special files are not allowed (%q)", h.Name)
		}
		if h.Mode&06000 != 0 || h.Size < 0 {
			return fmt.Errorf("unsafe mode or size for %q", h.Name)
		}
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			total += h.Size
		}
		seenEntries[name] = true
		if total > 8<<30 {
			return fmt.Errorf("volume archive exceeds 8 GiB uncompressed limit")
		}
	}
}

func archiveHasDescendant(entries map[string]bool, name string) bool {
	prefix := name + string(filepath.Separator)
	for existing := range entries {
		if strings.HasPrefix(existing, prefix) {
			return true
		}
	}
	return false
}

func validateArchiveLink(entry, target string, hardLink bool, volume, mountpoint string) error {
	link := filepath.FromSlash(target)
	if link == "" {
		return fmt.Errorf("link target must not be empty")
	}
	if filepath.IsAbs(link) {
		if hardLink || (!pathIsWithin(mountpoint, filepath.Clean(link)) && !allowedImageSymlink(volume, entry, filepath.Clean(link))) {
			return fmt.Errorf("absolute target %q is outside volume mount %q and approved image paths", link, mountpoint)
		}
		return nil
	}
	resolved := filepath.Clean(link)
	if !hardLink {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(entry), link))
	}
	if resolved == ".." || filepath.IsAbs(resolved) || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("relative target %q escapes the volume root", link)
	}
	return nil
}

// Codex stores a sandbox executable shim in its config volume as an absolute
// symlink to the matching binary shipped in the image. Permit only that
// specific link shape and the known packaged Linux executable roots.
func allowedImageSymlink(volume, entry, target string) bool {
	if volume != "claudex-codex" {
		return false
	}
	parts := strings.Split(filepath.ToSlash(entry), "/")
	if len(parts) != 4 || parts[0] != "tmp" || (parts[1] != "arg0" && parts[1] != "path") || !strings.HasPrefix(parts[2], "codex-arg0") || parts[3] == "" {
		return false
	}
	const npmCodexRoot = "/usr/local/share/npm-global/lib/node_modules/@openai/codex/node_modules/@openai"
	if pathIsWithin(npmCodexRoot, target) {
		relative, err := filepath.Rel(npmCodexRoot, target)
		if err != nil {
			return false
		}
		packageParts := strings.Split(filepath.ToSlash(relative), "/")
		if len(packageParts) == 5 && (packageParts[0] == "codex-linux-arm64" || packageParts[0] == "codex-linux-amd64" || packageParts[0] == "codex-linux-x64") && packageParts[1] == "vendor" && codexExecutablePath(packageParts[2:]) {
			return true
		}
	}
	const installedCodexRoot = "/usr/local/share/npm-global/lib/node_modules/@openai/codex"
	if !pathIsWithin(installedCodexRoot, target) {
		return false
	}
	relative, err := filepath.Rel(installedCodexRoot, target)
	if err != nil {
		return false
	}
	parts = strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 4 || parts[0] != "vendor" {
		return false
	}
	return codexExecutablePath(parts[1:])
}

func codexExecutablePath(parts []string) bool {
	if len(parts) != 3 || parts[1] != "codex" || parts[2] != "codex" {
		return false
	}
	switch parts[0] {
	case "aarch64-unknown-linux-musl", "x86_64-unknown-linux-musl", "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu":
		return true
	default:
		return false
	}
}

func volumeMountpoint(volume string) string {
	for _, h := range harness.Registry() {
		for _, mount := range h.Mounts {
			if mount.Volume == volume {
				return filepath.Clean(mount.Container)
			}
		}
	}
	return ""
}

func pathIsWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
