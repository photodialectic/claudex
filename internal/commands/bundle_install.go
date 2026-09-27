package commands

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/photodialectic/claudex/internal/dockerx"
)

type bundleInstallOptions struct {
	source         string
	volumes        []string
	noVerify       bool
	withHostConfig bool
	replace        bool
}

func BundleInstall(args []string) error { return bundleInstallWithDocker(args, &dockerx.CLI{}) }

func bundleInstallWithDocker(args []string, dx dockerx.Docker) error {
	o, err := parseBundleInstallArgs(args)
	if err != nil {
		return err
	}
	m, err := readManifest(o.source)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	if !o.noVerify {
		if err := verifyBundle(o.source, m); err != nil {
			return err
		}
	}
	volumes, err := chooseBundleVolumes(m, o.volumes)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		if err := validateVolumeArchive(filepath.Join(o.source, filepath.FromSlash(volumeArchivePath(m, volume))), volume); err != nil {
			return fmt.Errorf("unsafe volume archive %s: %w", volume, err)
		}
	}
	if o.withHostConfig {
		if m.HostArchive == "" {
			return fmt.Errorf("bundle does not contain host config archive")
		}
		hostArchive := filepath.Join(o.source, filepath.FromSlash(m.HostArchive))
		if err := validateHostArchive(hostArchive); err != nil {
			return fmt.Errorf("unsafe host config archive: %w", err)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(home, ".claudex")); err == nil && !o.replace {
			return fmt.Errorf("%s already exists; host config restore does not overwrite it (use --replace)", filepath.Join(home, ".claudex"))
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := checkLocalArchitecture(dx, m.Architecture); err != nil {
		return err
	}
	if o.replace {
		for _, volume := range volumes {
			exists, err := dx.VolumeExists(volume)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			inUse, err := dx.VolumeInUse(volume)
			if err != nil {
				return err
			}
			if inUse {
				return fmt.Errorf("cannot replace volume %s while a container uses it", volume)
			}
		}
	}
	if err := ensureBundleImage(dx, o.source, m); err != nil {
		return err
	}
	tagID, err := dx.ImageID("claudex")
	if err == nil && tagID != m.ImageID {
		fmt.Fprintln(os.Stderr, "Warning: replacing a different local 'claudex' image")
	}
	if err := dx.TagImage(m.ImageTag, "claudex"); err != nil {
		return fmt.Errorf("adopt bundle image as claudex: %w", err)
	}
	for _, volume := range volumes {
		exists, err := dx.VolumeExists(volume)
		if err != nil {
			return err
		}
		if exists && !o.replace {
			fmt.Printf("Skip existing volume %s (use --replace to restore)\n", volume)
			continue
		}
		if exists {
			inUse, err := dx.VolumeInUse(volume)
			if err != nil {
				return err
			}
			if inUse {
				return fmt.Errorf("cannot replace volume %s while a container uses it", volume)
			}
			if err := dx.VolumeRemove(volume); err != nil {
				return err
			}
		}
		if _, err := dx.VolumeCreate(volume); err != nil {
			return err
		}
		if err := extractVolumeArchive(dx, o.source, m, volume, volume); err != nil {
			_ = dx.VolumeRemove(volume)
			return fmt.Errorf("restore volume %s failed; removed partial volume: %w", volume, err)
		}
	}
	if o.withHostConfig {
		hostArchive := filepath.Join(o.source, filepath.FromSlash(m.HostArchive))
		if err := restoreHostConfig(hostArchive, o.replace); err != nil {
			return err
		}
	}
	return nil
}

func parseBundleInstallArgs(args []string) (bundleInstallOptions, error) {
	var o bundleInstallOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--replace":
			o.replace = true
		case "--volumes":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--volumes requires comma-separated names")
			}
			o.volumes = strings.Split(args[i+1], ",")
			i++
		case "--no-verify":
			o.noVerify = true
		case "--with-host-config":
			o.withHostConfig = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return o, fmt.Errorf("unknown bundle install option %q", args[i])
			}
			if o.source != "" {
				return o, fmt.Errorf("unexpected argument %q", args[i])
			}
			o.source = args[i]
		}
	}
	if o.source == "" {
		return o, fmt.Errorf("usage: claudex bundle install <src-dir> [--replace] [--volumes <n1,n2>] [--no-verify] [--with-host-config]")
	}
	return o, nil
}

func checkLocalArchitecture(dx dockerx.Docker, imageArch string) error {
	local, err := dx.LocalArch()
	if err != nil {
		return err
	}
	if local != imageArch {
		return fmt.Errorf("bundle image architecture %s does not match Docker server architecture %s; cross-architecture execution is not supported", imageArch, local)
	}
	return nil
}

func ensureBundleImage(dx dockerx.Docker, bundleDir string, m bundleManifest) error {
	exists, err := dx.ImageExists(m.ImageTag)
	if err != nil {
		return err
	}
	if exists {
		id, err := dx.ImageID(m.ImageTag)
		if err != nil {
			return err
		}
		if id != m.ImageID {
			return fmt.Errorf("image tag %s points to %s, expected %s", m.ImageTag, id, m.ImageID)
		}
		return nil
	}
	path := filepath.Join(bundleDir, filepath.FromSlash(m.ImageArchive))
	f, task, src, err := openProgressFile(path, m.ImageArchive)
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
	loadErr := dx.LoadImage(gz)
	closeErr := gz.Close()
	if loadErr != nil {
		return loadErr
	}
	if closeErr != nil {
		return closeErr
	}
	id, err := dx.ImageID(m.ImageTag)
	if err != nil {
		return fmt.Errorf("bundle image did not load with expected tag %s: %w", m.ImageTag, err)
	}
	if id != m.ImageID {
		return fmt.Errorf("loaded image ID %s does not match manifest %s", id, m.ImageID)
	}
	success = true
	return nil
}

func chooseBundleVolumes(m bundleManifest, requested []string) ([]string, error) {
	available := map[string]bool{}
	for _, name := range m.Volumes {
		available[name] = true
	}
	if len(requested) == 0 {
		return append([]string(nil), m.Volumes...), nil
	}
	selected, err := selectedVolumeNames(requested)
	if err != nil {
		return nil, err
	}
	for _, name := range selected {
		if !available[name] {
			return nil, fmt.Errorf("volume %s is not present in this bundle", name)
		}
	}
	return selected, nil
}

func extractVolumeArchive(dx dockerx.Docker, bundleDir string, m bundleManifest, volume, target string) error {
	path := filepath.Join(bundleDir, filepath.FromSlash(volumeArchivePath(m, volume)))
	f, task, src, err := openProgressFile(path, filepath.Base(path))
	if err != nil {
		return err
	}
	defer f.Close()
	success := false
	defer func() { task.finish(success) }()
	if err := dx.ExtractVolumeFrom(m.ImageTag, target, src); err != nil {
		return fmt.Errorf("restore volume %s: %w", volume, err)
	}
	success = true
	return nil
}

func restoreHostConfig(archive string, replace bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dest := filepath.Join(home, ".claudex")
	_, statErr := os.Lstat(dest)
	destExists := statErr == nil
	if destExists && !replace {
		return fmt.Errorf("%s already exists; host config restore does not overwrite it (use --replace)", dest)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	tmp, err := os.MkdirTemp(home, ".claudex-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractSafeHostArchive(archive, tmp); err != nil {
		return err
	}
	if replace && destExists {
		backup, err := os.MkdirTemp(home, ".claudex-backup-*")
		if err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Rename(backup, dest)
			return err
		}
		return os.RemoveAll(backup)
	}
	return os.Rename(tmp, dest)
}

func extractSafeHostArchive(path, dest string) error {
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
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
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
			return fmt.Errorf("unsafe host archive path %q", h.Name)
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported host archive entry type for %q", h.Name)
		}
		target := filepath.Join(root, name)
		if !strings.HasPrefix(target+string(filepath.Separator), root+string(filepath.Separator)) {
			return fmt.Errorf("host archive path escapes destination: %q", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if h.Size < 0 {
			return fmt.Errorf("invalid host archive entry size for %q", h.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, h.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}
