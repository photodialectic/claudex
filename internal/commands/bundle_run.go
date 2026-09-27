package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/run"
)

type bundleRunOptions struct {
	bundleDir string
	noVerify  bool
	refresh   bool
	writeBack bool
	runArgs   []string
}

func BundleRun(args []string) error { return bundleRunWithDocker(args, &dockerx.CLI{}) }

func bundleRunWithDocker(args []string, dx dockerx.Docker) error {
	o, err := parseBundleRunArgs(args)
	if err != nil {
		return err
	}
	m, err := readManifest(o.bundleDir)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	if !o.noVerify {
		if err := verifyBundle(o.bundleDir, m); err != nil {
			return err
		}
	}
	all, _ := selectedVolumeNames(nil)
	if len(m.Volumes) != len(all) {
		return fmt.Errorf("bundle run requires a complete bundle: expected %d registry volumes, found %d", len(all), len(m.Volumes))
	}
	for _, original := range all {
		if err := validateVolumeArchive(filepath.Join(o.bundleDir, filepath.FromSlash(volumeArchivePath(m, original))), original); err != nil {
			return fmt.Errorf("unsafe volume archive %s: %w", original, err)
		}
	}
	if err := checkLocalArchitecture(dx, m.Architecture); err != nil {
		return err
	}
	if err := ensureBundleImage(dx, o.bundleDir, m); err != nil {
		return err
	}
	volumeNames := make(map[string]string, len(all))
	for _, original := range all {
		volumeNames[original] = run.BundleVolumeName(m.BundleID, original)
		if err := materializeBundleVolume(dx, o.bundleDir, m, original, volumeNames[original], o.refresh); err != nil {
			return err
		}
	}
	runOptions, err := run.ParseArgs(o.runArgs)
	if err != nil {
		return err
	}
	runOptions.BundleID = m.BundleID
	runOptions.Image = m.ImageTag
	runOptions.BundleImageID = m.ImageID
	runOptions.VolumeNames = volumeNames
	if err := runOptions.Derive(); err != nil {
		return err
	}
	err = run.RunPrepared(runOptions, os.Stdin, os.Stdout, os.Stderr, dx)
	if err != nil {
		return err
	}
	if o.writeBack {
		if err := dx.Stop(runOptions.Name); err != nil {
			return fmt.Errorf("stop bundle session before write-back: %w", err)
		}
		unlock, err := lockBundle(o.bundleDir)
		if err != nil {
			return err
		}
		defer unlock()
		current, err := readManifest(o.bundleDir)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, m) {
			return fmt.Errorf("bundle changed during the session; refusing to write back stale bundle volumes")
		}
		if !o.noVerify {
			if err := verifyBundle(o.bundleDir, current); err != nil {
				return err
			}
		}
		return writeBackBundleVolumes(dx, o.bundleDir, current, volumeNames)
	}
	return nil
}

func parseBundleRunArgs(args []string) (bundleRunOptions, error) {
	var o bundleRunOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-verify":
			o.noVerify = true
		case "--refresh":
			o.refresh = true
		case "--write-back":
			o.writeBack = true
		default:
			if o.bundleDir == "" && !strings.HasPrefix(args[i], "-") {
				o.bundleDir = args[i]
				continue
			}
			if o.bundleDir == "" {
				return o, fmt.Errorf("bundle directory must be the first bundle run argument")
			}
			if strings.HasPrefix(args[i], "-") {
				switch args[i] {
				case "--host-network", "--no-git", "--firewall", "--replace", "--parallel", "--strict-mounts":
					o.runArgs = append(o.runArgs, args[i])
				case "--network", "--name", "--ca-cert":
					if i+1 >= len(args) {
						return o, fmt.Errorf("%s requires a value", args[i])
					}
					o.runArgs = append(o.runArgs, args[i], args[i+1])
					i++
				default:
					return o, fmt.Errorf("unknown bundle run option %q", args[i])
				}
				continue
			}
			o.runArgs = append(o.runArgs, args[i])
		}
	}
	if o.bundleDir == "" {
		return o, fmt.Errorf("usage: claudex bundle run <bundle> [--refresh] [--write-back] [--no-verify] [DIR...] [run-flags]")
	}
	return o, nil
}

func bundleVolumeStatePath(bundleID, volume string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claudex", "bundle-volumes", bundleID, volume+".sha256"), nil
}

func materializeBundleVolume(dx dockerx.Docker, bundleDir string, m bundleManifest, original, volume string, refresh bool) error {
	exists, err := dx.VolumeExists(volume)
	if err != nil {
		return err
	}
	archive := m.Files[volumeArchivePath(m, original)]
	statePath, err := bundleVolumeStatePath(m.BundleID, original)
	if err != nil {
		return err
	}
	if exists {
		labels, err := dx.VolumeLabels(volume)
		if err != nil {
			return err
		}
		if labels[run.BundleLabel] != m.BundleID {
			return fmt.Errorf("volume %s exists but is not owned by bundle %s", volume, m.BundleID)
		}
		if !refresh {
			state, err := os.ReadFile(statePath)
			if err != nil {
				return fmt.Errorf("cannot verify cached bundle volume %s; use --refresh: %w", volume, err)
			}
			if strings.TrimSpace(string(state)) != archive.SHA256 {
				return fmt.Errorf("bundle volume %s changed since this volume was materialized; use --refresh", original)
			}
			return nil
		}
		inUse, err := dx.VolumeInUse(volume)
		if err != nil {
			return err
		}
		if inUse {
			return fmt.Errorf("cannot refresh volume %s while a container uses it", volume)
		}
		if err := dx.VolumeRemove(volume); err != nil {
			return err
		}
	}
	_, err = dx.VolumeCreateLabeled(volume, map[string]string{run.BundleLabel: m.BundleID})
	if err != nil {
		return err
	}
	if err := extractVolumeArchive(dx, bundleDir, m, original, volume); err != nil {
		_ = dx.VolumeRemove(volume)
		return err
	}
	return writeChecksumState(statePath, archive.SHA256)
}

func writeChecksumState(path, checksum string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, err := writeArtifact(path, func(w io.Writer) error { _, err := io.WriteString(w, checksum+"\n"); return err })
	return err
}

func writeBackBundleVolumes(dx dockerx.Docker, bundleDir string, m bundleManifest, names map[string]string) error {
	oldFiles := make(map[string]bundleArtifact, len(m.Files))
	for path, artifact := range m.Files {
		oldFiles[path] = artifact
	}
	for _, original := range m.Volumes {
		volume := names[original]
		relative, artifact, err := writeVersionedArtifact(bundleDir, "volumes/"+original, ".tar.gz", func(dst io.Writer) error {
			return dx.TarVolumeTo(m.ImageTag, volume, dst)
		})
		if err != nil {
			return err
		}
		if err := validateVolumeArchive(filepath.Join(bundleDir, filepath.FromSlash(relative)), original); err != nil {
			return fmt.Errorf("volume %s produced an unsafe write-back archive: %w", original, err)
		}
		previous := m.VolumeArchives[original]
		m.Files[relative] = artifact
		if previous != relative {
			delete(m.Files, previous)
		}
		m.VolumeArchives[original] = relative
	}
	if err := writeManifestAtomic(filepath.Join(bundleDir, "manifest.json"), m); err != nil {
		return err
	}
	for _, original := range m.Volumes {
		statePath, err := bundleVolumeStatePath(m.BundleID, original)
		if err != nil {
			return err
		}
		if err := writeChecksumState(statePath, m.Files[m.VolumeArchives[original]].SHA256); err != nil {
			return err
		}
	}
	pruneUnreferencedArtifacts(bundleDir, oldFiles, m.Files)
	return nil
}
