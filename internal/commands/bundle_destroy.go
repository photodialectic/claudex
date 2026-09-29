package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/run"
)

func destroyBundleResources(dx dockerx.Docker, m bundleManifest) error {
	if err := destroyBundleContainers(dx, m); err != nil {
		return err
	}
	volumes, _ := selectedVolumeNames(nil)
	var failures []string
	for _, original := range volumes {
		name := run.BundleVolumeName(m.BundleID, original)
		exists, err := dx.VolumeExists(name)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !exists {
			continue
		}
		labels, err := dx.VolumeLabels(name)
		if err != nil || labels[run.BundleLabel] != m.BundleID {
			failures = append(failures, fmt.Sprintf("refusing to remove unowned volume %s", name))
			continue
		}
		inUse, err := dx.VolumeInUse(name)
		if err != nil || inUse {
			failures = append(failures, fmt.Sprintf("cannot remove volume %s while in use", name))
			continue
		}
		if err := dx.VolumeRemove(name); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if exists, err := dx.ImageExists(m.ImageTag); err != nil {
		failures = append(failures, err.Error())
	} else if exists {
		id, err := dx.ImageID(m.ImageTag)
		if err != nil || id != m.ImageID {
			failures = append(failures, "refusing to remove bundle image tag because its image ID does not match the manifest")
		} else if err := dx.ImageRemove(m.ImageTag); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("bundle destroy incomplete: %s", strings.Join(failures, "; "))
	}
	stateDir, err := bundleVolumeStatePath(m.BundleID, "x")
	if err == nil {
		_ = os.RemoveAll(filepath.Dir(stateDir))
	}
	fmt.Printf("Removed resources for bundle %s\n", m.BundleID)
	return nil
}

func destroyBundleContainers(dx dockerx.Docker, m bundleManifest) error {
	names, err := dx.PS(true)
	if err != nil {
		return err
	}
	expectedVolumes := map[string]bool{}
	for _, original := range allHarnessVolumes() {
		expectedVolumes[run.BundleVolumeName(m.BundleID, original)] = true
	}
	var victims []string
	for _, name := range names {
		container, err := dx.Inspect(name)
		if err != nil {
			continue
		}
		if container.Labels[run.BundleLabel] != m.BundleID {
			continue
		}
		if container.Status == "running" {
			return fmt.Errorf("cannot destroy bundle %s while session container %s is running", m.BundleID, name)
		}
		if container.Image != m.ImageTag {
			return fmt.Errorf("refusing to remove bundle-labeled container %s with unexpected image", name)
		}
		if len(container.Volumes) != len(expectedVolumes) {
			return fmt.Errorf("refusing to remove bundle-labeled container %s with unverified volume mounts", name)
		}
		seenVolumes := map[string]bool{}
		for _, volume := range container.Volumes {
			if !expectedVolumes[volume] || seenVolumes[volume] {
				return fmt.Errorf("refusing to remove bundle-labeled container %s with unexpected volume %s", name, volume)
			}
			seenVolumes[volume] = true
		}
		victims = append(victims, name)
	}
	for _, name := range victims {
		if err := dx.Remove(name, true); err != nil {
			return fmt.Errorf("remove stopped bundle container %s: %w", name, err)
		}
	}
	return nil
}

func bundleDestroy(args []string, dx dockerx.Docker) error {
	bundleDir := ""
	noVerify := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-verify":
			noVerify = true
		case "-h", "--help":
			fmt.Println("Usage: claudex bundle destroy <bundle> [--no-verify]")
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown bundle destroy option %q", args[i])
			}
			if bundleDir != "" {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
			bundleDir = args[i]
		}
	}
	if bundleDir == "" {
		return fmt.Errorf("usage: claudex bundle destroy <bundle> [--no-verify]")
	}
	manifest, err := readManifest(bundleDir)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	if !noVerify {
		if err := verifyBundle(bundleDir, manifest); err != nil {
			return err
		}
	}
	return destroyBundleResources(dx, manifest)
}
