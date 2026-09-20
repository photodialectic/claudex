package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/photodialectic/claudex/internal/buildctx"
	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/harness"
)

// Harness implements `claudex harness <list|push|pull>`.
func Harness(args []string) error {
	if len(args) == 0 {
		return harnessUsage()
	}
	switch args[0] {
	case "list":
		return harnessList(args[1:])
	case "push":
		return harnessPush(args[1:])
	case "pull":
		return harnessPull(args[1:])
	case "update":
		return harnessUpdate(args[1:])
	default:
		return harnessUsage()
	}
}

func harnessUsage() error {
	fmt.Println(`Usage: claudex harness <command> [<name> ...]

Commands:
  list               List supported harnesses and their config paths/volumes
  push [<name>...]   Sync host config -> shared volume (all if no name)
  pull [<name>...]   Back up shared volume -> ~/.claudex/backups/<name>/<ts>/
  update [<name>...] Rebuild image tool layer(s); all if no name (--no-cache for full)`)
	return nil
}

func harnessList(args []string) error {
	if len(args) > 0 {
		return harnessUsage()
	}
	fmt.Printf("%-10s %-26s %-26s %-6s\n", "HARNESS", "HOST", "VOLUME", "KIND")
	for _, h := range harness.Registry() {
		for _, m := range h.Mounts {
			kind := "dir"
			if m.Kind == harness.KindFile {
				kind = "file"
			}
			fmt.Printf("%-10s %-26s %-26s %-6s\n", h.Name, "~/"+m.HostRel, m.Volume, kind)
		}
	}
	return nil
}

func harnessPush(args []string) error {
	names, err := targetHarnessNames(args)
	if err != nil {
		return err
	}
	dx := &dockerx.CLI{}
	if err := ensureImage(dx); err != nil {
		return err
	}
	for _, h := range harness.Registry() {
		if !nameMatches(names, h.Name) {
			continue
		}
		for _, m := range h.Mounts {
			hp := m.HostPath()
			if _, err := os.Stat(hp); err != nil {
				fmt.Printf("Skip %s: host path %s not found\n", h.Name, hp)
				continue
			}
			if h.Merge != nil && m.Kind == harness.KindFile {
				if err := pushFileMerge(dx, h.Merge, hp, m); err != nil {
					return err
				}
				fmt.Printf("Merged %s -> %s\n", hp, m.Volume)
				continue
			}
			if err := dx.CopyHostToVolume(hp, m.Volume, m.VolumePath); err != nil {
				return err
			}
			fmt.Printf("Pushed %s -> %s\n", hp, m.Volume)
		}
	}
	return nil
}

func pushFileMerge(dx dockerx.Docker, merge harness.MergeFunc, hostPath string, m harness.Mount) error {
	tmp, err := os.MkdirTemp("", "claudex-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	cur := filepath.Join(tmp, m.VolumePath)
	_ = dx.CopyVolumeToHost(m.Volume, m.VolumePath, tmp)
	dst, _ := os.ReadFile(cur)

	src, err := os.ReadFile(hostPath)
	if err != nil {
		return err
	}
	merged, err := merge(dst, src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cur, merged, 0600); err != nil {
		return err
	}
	return dx.CopyHostToVolume(cur, m.Volume, m.VolumePath)
}

func harnessPull(args []string) error {
	names, err := targetHarnessNames(args)
	if err != nil {
		return err
	}
	dx := &dockerx.CLI{}
	if err := ensureImage(dx); err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	for _, h := range harness.Registry() {
		if !nameMatches(names, h.Name) {
			continue
		}
		base := filepath.Join(backupsRoot(), h.Name, ts)
		for _, m := range h.Mounts {
			target := filepath.Join(base, filepath.FromSlash(m.HostRel))
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if m.Kind == harness.KindFile {
				f, err := os.Create(target)
				if err != nil {
					return err
				}
				_ = f.Close()
				if err := dx.CopyVolumeToHost(m.Volume, m.VolumePath, target); err != nil {
					return err
				}
			} else if err := dx.CopyVolumeToHost(m.Volume, "", target); err != nil {
				return err
			}
			fmt.Printf("Backed up %s -> %s\n", m.Volume, target)
		}
	}
	return nil
}

func targetHarnessNames(args []string) ([]string, error) {
	var names []string
	for _, a := range args {
		if a == "" {
			continue
		}
		if _, ok := harness.ByName(a); !ok {
			return nil, fmt.Errorf("unknown harness %q", a)
		}
		names = append(names, a)
	}
	return names, nil
}

func nameMatches(names []string, name string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func backupsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claudex/backups"
	}
	return filepath.Join(home, ".claudex", "backups")
}

func harnessUpdate(args []string) error {
	return harnessUpdateWithDocker(&dockerx.CLI{}, args)
}

func harnessUpdateWithDocker(dx dockerx.Docker, args []string) error {
	var noCache bool
	var names []string
	for _, a := range args {
		switch a {
		case "--no-cache":
			noCache = true
		default:
			names = append(names, a)
		}
	}

	targetNames, err := resolveUpdateTargets(names)
	if err != nil {
		return err
	}

	fmt.Println("Preparing build context...")
	ctxDir, cleanup, err := buildctx.PrepareBuildContext()
	if err != nil {
		return err
	}
	defer cleanup()

	token := fmt.Sprintf("%d", time.Now().Unix())
	buildArgs := map[string]string{}
	for _, n := range targetNames {
		buildArgs[harness.ToolBuildArg(n)] = token
	}

	if noCache {
		fmt.Printf("Rebuilding %s tool layer(s) with --no-cache...\n", strings.Join(targetNames, ", "))
	} else {
		fmt.Printf("Refreshing %s tool layer(s) in image 'claudex'...\n", strings.Join(targetNames, ", "))
	}
	options := dockerx.BuildOptions{NoCache: noCache, BuildArgs: buildArgs}
	if err := dx.Build("claudex", ctxDir, options); err != nil {
		return err
	}
	fmt.Println("✅ Update complete")
	return nil
}

// resolveUpdateTargets maps requested harness names (empty = all tools) to the
// set installed in the image.
func resolveUpdateTargets(names []string) ([]string, error) {
	if len(names) == 0 {
		var out []string
		for _, h := range harness.Registry() {
			if len(h.Install) > 0 {
				out = append(out, h.Name)
			}
		}
		return out, nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		h, ok := harness.ByName(n)
		if !ok {
			return nil, fmt.Errorf("unknown harness %q", n)
		}
		if len(h.Install) == 0 {
			return nil, fmt.Errorf("harness %q has no install command", n)
		}
		out = append(out, h.Name)
	}
	return out, nil
}

func ensureImage(dx dockerx.Docker) error {
	present, err := dx.ImageExists("claudex")
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	fmt.Println("Building image 'claudex' (first run)...")
	ctxDir, cleanup, err := buildctx.PrepareBuildContext()
	if err != nil {
		return err
	}
	defer cleanup()
	return dx.Build("claudex", ctxDir, dockerx.BuildOptions{})
}
