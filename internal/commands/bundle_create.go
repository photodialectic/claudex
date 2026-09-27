package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/run"
	"github.com/photodialectic/claudex/internal/version"
)

type bundleCreateOptions struct {
	dest         string
	volumes      []string
	noBinaries   bool
	noHostConfig bool
	force        bool
	jobs         int
}

type volumeArchiveResult struct {
	volume   string
	path     string
	artifact bundleArtifact
	exists   bool
	err      error
}

// BundleCreate creates or refreshes a portable bundle.
func BundleCreate(args []string) error { return bundleCreateWithDocker(args, &dockerx.CLI{}) }

func bundleCreateWithDocker(args []string, dx dockerx.Docker) error {
	opts, err := parseBundleCreateArgs(args)
	if err != nil {
		return err
	}
	if err := validateBundleDestination(opts.dest, opts.force); err != nil {
		return err
	}
	selected, err := selectedVolumeNames(opts.volumes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.dest, 0700); err != nil {
		return err
	}
	unlock, err := lockBundle(opts.dest)
	if err != nil {
		return err
	}
	defer unlock()
	manifestPath := filepath.Join(opts.dest, "manifest.json")
	manifest := bundleManifest{FormatVersion: bundleFormatVersion, ClaudexVersion: version.Version, ImageTag: "claudex", Files: map[string]bundleArtifact{}}
	if _, err := os.Lstat(manifestPath); err == nil {
		if err := checkBundleFile(opts.dest, "manifest.json"); err != nil {
			return err
		}
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("read existing bundle manifest: %w", err)
		}
		if err := validateManifest(manifest); err != nil {
			return fmt.Errorf("existing bundle is invalid: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		manifest.BundleID, err = newBundleID()
		if err != nil {
			return err
		}
	}
	if err := ensureImage(dx); err != nil {
		return err
	}
	imageID, err := dx.ImageID("claudex")
	if err != nil {
		return err
	}
	arch, err := dx.ImageArch("claudex")
	if err != nil {
		return err
	}
	imageTag := run.BundleImageTag(manifest.BundleID)
	if err := dx.TagImage("claudex", imageTag); err != nil {
		return fmt.Errorf("tag bundle image: %w", err)
	}
	taggedImageID, err := dx.ImageID(imageTag)
	if err != nil {
		return err
	}
	if taggedImageID != imageID {
		return fmt.Errorf("bundle image tag %s resolves to %s, expected %s", imageTag, taggedImageID, imageID)
	}
	manifest.ImageID, manifest.ImageTag, manifest.Architecture = imageID, imageTag, arch
	manifest.ClaudexVersion = version.Version
	oldFiles := manifest.Files
	files := make(map[string]bundleArtifact)
	imageRelative, imageArtifact, err := writeVersionedArtifact(opts.dest, "image/claudex", ".tar.gz", func(dst io.Writer) error {
		gz := gzip.NewWriter(dst)
		if err := dx.SaveImageTo(imageTag, gz); err != nil {
			_ = gz.Close()
			return err
		}
		return gz.Close()
	})
	if err != nil {
		return fmt.Errorf("save image: %w", err)
	}
	files[imageRelative] = imageArtifact
	manifest.ImageArchive = imageRelative

	manifestVolumes := make([]string, 0, len(selected))
	volumeArchives := make(map[string]string, len(selected))
	volumeResults, err := archiveVolumes(opts.dest, imageTag, selected, dx, opts.jobs)
	if err != nil {
		return err
	}
	for _, result := range volumeResults {
		if !result.exists {
			fmt.Printf("Skip missing volume %s\n", result.volume)
			continue
		}
		files[result.path] = result.artifact
		volumeArchives[result.volume] = result.path
		manifestVolumes = append(manifestVolumes, result.volume)
	}
	manifest.Volumes = manifestVolumes
	manifest.VolumeArchives = volumeArchives

	manifest.HostArchive = ""
	if !opts.noHostConfig {
		if home, err := os.UserHomeDir(); err == nil {
			hostDir := filepath.Join(home, ".claudex")
			if info, statErr := os.Stat(hostDir); statErr == nil && info.IsDir() {
				relative, artifact, err := writeVersionedArtifact(opts.dest, "host/claudex-home", ".tar.gz", func(dst io.Writer) error {
					return writeHostArchive(hostDir, dst)
				})
				if err != nil {
					return fmt.Errorf("archive host config: %w", err)
				}
				files[relative] = artifact
				manifest.HostArchive = relative
			}
		}
	}
	if opts.noHostConfig {
		manifest.HostArchive = ""
	}

	if !opts.noBinaries {
		binaryFiles, err := buildBundleBinaries(opts.dest)
		if err != nil {
			return err
		}
		for name, artifact := range binaryFiles {
			files[name] = artifact
		}
	}
	manifest.Files = files
	if err := writeManifestAtomic(manifestPath, manifest); err != nil {
		return err
	}
	pruneUnreferencedArtifacts(opts.dest, oldFiles, files)
	fmt.Printf("Portable bundle %s written to %s\n", manifest.BundleID, opts.dest)
	return nil
}

func archiveVolumes(root, image string, volumes []string, dx dockerx.Docker, jobs int) ([]volumeArchiveResult, error) {
	if len(volumes) == 0 {
		return nil, nil
	}
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(volumes) {
		jobs = len(volumes)
	}
	work := make(chan string)
	results := make(chan volumeArchiveResult, len(volumes))
	fatal := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopForError := func(err error) {
		select {
		case fatal <- err:
		default:
		}
		cancel()
	}
	var workers sync.WaitGroup
	for i := 0; i < jobs; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				var volume string
				var ok bool
				select {
				case <-ctx.Done():
					return
				case volume, ok = <-work:
					if !ok {
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				result := volumeArchiveResult{volume: volume}
				exists, err := dx.VolumeExists(volume)
				if err != nil {
					result.err = fmt.Errorf("check volume %s: %w", volume, err)
					stopForError(result.err)
					results <- result
					continue
				}
				result.exists = exists
				if !exists {
					results <- result
					continue
				}
				result.path, result.artifact, err = writeVersionedArtifact(root, "volumes/"+volume, ".tar.gz", func(dst io.Writer) error {
					return dx.TarVolumeToContext(ctx, image, volume, dst)
				})
				if err != nil {
					result.err = fmt.Errorf("archive volume %s: %w", volume, err)
					stopForError(result.err)
					results <- result
					return
				}
				if err := validateVolumeArchive(filepath.Join(root, filepath.FromSlash(result.path)), volume); err != nil {
					result.err = fmt.Errorf("volume %s produced an unsafe archive: %w", volume, err)
					stopForError(result.err)
				}
				results <- result
				if result.err != nil {
					return
				}
			}
		}()
	}

feed:
	for _, volume := range volumes {
		select {
		case <-ctx.Done():
			break feed
		case work <- volume:
		}
	}
	close(work)
	workers.Wait()
	close(results)
	out := make([]volumeArchiveResult, 0, len(volumes))
	var failures []error
	for result := range results {
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		out = append(out, result)
	}
	select {
	case err := <-fatal:
		return nil, err
	default:
	}
	if err := errors.Join(failures...); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].volume < out[j].volume })
	return out, nil
}

func parseBundleCreateArgs(args []string) (bundleCreateOptions, error) {
	o := bundleCreateOptions{jobs: 2}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--volumes":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--volumes requires comma-separated names")
			}
			o.volumes = strings.Split(args[i+1], ",")
			i++
		case "--no-binaries":
			o.noBinaries = true
		case "--no-host-config":
			o.noHostConfig = true
		case "--force":
			o.force = true
		case "--jobs":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--jobs requires a number from 1 to 16")
			}
			jobs, err := strconv.Atoi(args[i+1])
			if err != nil || jobs < 1 || jobs > 16 {
				return o, fmt.Errorf("--jobs must be between 1 and 16")
			}
			o.jobs = jobs
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return o, fmt.Errorf("unknown bundle create option %q", args[i])
			}
			if o.dest != "" {
				return o, fmt.Errorf("unexpected argument %q", args[i])
			}
			o.dest = args[i]
		}
	}
	if o.dest == "" {
		return o, fmt.Errorf("usage: claudex bundle create <dest-dir> [--volumes <n1,n2>] [--jobs <1-16>] [--no-binaries] [--no-host-config] [--force]")
	}
	return o, nil
}

func validateBundleDestination(dest string, force bool) error {
	info, err := os.Stat(dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("bundle destination %s is not a directory", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dest, "manifest.json")); err == nil {
		return nil
	}
	if !force {
		return fmt.Errorf("refusing non-empty non-bundle directory %s (use --force)", dest)
	}
	return nil
}

func writeHostArchive(root string, dst io.Writer) error {
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == "backups" || strings.HasPrefix(rel, "backups"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Uid, h.Gid = 0, 0
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeTarErr := tw.Close()
	closeGzipErr := gz.Close()
	if err != nil {
		return err
	}
	if closeTarErr != nil {
		return closeTarErr
	}
	return closeGzipErr
}

func buildBundleBinaries(bundleDir string) (map[string]bundleArtifact, error) {
	source := findSourceRoot()
	if source == "" {
		fmt.Fprintln(os.Stderr, "Warning: skipping CLI binaries (source tree not found; set CLAUDEX_SRC)")
		return nil, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: skipping CLI binaries (go is not on PATH)")
		return nil, nil
	}
	result := map[string]bundleArtifact{}
	for _, target := range []struct{ os, arch string }{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		name := fmt.Sprintf("bin/claudex-%s-%s", target.os, target.arch)
		path := filepath.Join(bundleDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".claudex-tmp-*")
		if err != nil {
			return nil, err
		}
		tmpName := tmp.Name()
		_ = tmp.Close()
		cmd := exec.Command("go", "build", "-trimpath", "-o", tmpName, "./cmd/claudex")
		cmd.Dir = source
		cmd.Env = crossCompileEnv(target.os, target.arch)
		out, err := cmd.CombinedOutput()
		if err != nil {
			_ = os.Remove(tmpName)
			return nil, fmt.Errorf("cross-compile %s: %w: %s", name, err, strings.TrimSpace(string(out)))
		}
		if err := os.Chmod(tmpName, 0700); err != nil {
			_ = os.Remove(tmpName)
			return nil, err
		}
		artifact, err := hashFile(tmpName)
		if err != nil {
			_ = os.Remove(tmpName)
			return nil, err
		}
		relative := name + "-" + artifact.SHA256[:12]
		targetPath := filepath.Join(bundleDir, filepath.FromSlash(relative))
		if existing, statErr := os.Stat(targetPath); statErr == nil {
			_ = os.Remove(tmpName)
			got, hashErr := hashFile(targetPath)
			if hashErr != nil || got != artifact || existing.IsDir() {
				return nil, fmt.Errorf("content-addressed binary collision at %s", relative)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			_ = os.Remove(tmpName)
			return nil, statErr
		} else if err := os.Rename(tmpName, targetPath); err != nil {
			_ = os.Remove(tmpName)
			return nil, err
		}
		result[relative] = artifact
	}
	return result, nil
}

func crossCompileEnv(goos, goarch string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOOS=") || strings.HasPrefix(entry, "GOARCH=") || strings.HasPrefix(entry, "CGO_ENABLED=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
}

func findSourceRoot() string {
	if source := os.Getenv("CLAUDEX_SRC"); source != "" && isSourceRoot(source) {
		abs, _ := filepath.Abs(source)
		return abs
	}
	starts := []string{}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		for dir := start; ; dir = filepath.Dir(dir) {
			if isSourceRoot(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return ""
}

func isSourceRoot(path string) bool {
	info, err := os.Stat(filepath.Join(path, "go.mod"))
	if err != nil || info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "cmd", "claudex", "main.go"))
	return err == nil
}
