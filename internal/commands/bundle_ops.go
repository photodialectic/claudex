package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/harness"
	"github.com/photodialectic/claudex/internal/run"
	"github.com/photodialectic/claudex/internal/version"
)

const bundleFormatVersion = 1

const (
	packLabel = "com.claudex.pack"
)

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
	imageTag := run.PackImageTag(manifest.BundleID)
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

func pruneUnreferencedArtifacts(root string, old, current map[string]bundleArtifact) {
	for relative := range old {
		if _, ok := current[relative]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(relative)))
	}
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

func lockBundle(bundleDir string) (func(), error) {
	path := filepath.Join(bundleDir, ".claudex-bundle.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("bundle is locked by another create/refresh/write-back operation: %s", path)
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
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

func newBundleID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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

func writeArtifact(path string, write func(io.Writer) error) (bundleArtifact, error) {
	return writeArtifactWithProgress(path, filepath.Base(path), false, write)
}

func writeArtifactWithProgress(path, label string, showProgress bool, write func(io.Writer) error) (bundleArtifact, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return bundleArtifact{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claudex-tmp-*")
	if err != nil {
		return bundleArtifact{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return bundleArtifact{}, err
	}
	h := sha256.New()
	var task *progressTask
	var w io.Writer = io.MultiWriter(tmp, h)
	if showProgress {
		task = archiveProgress.start(label, 0)
		w = progressWriter{dst: w, task: task}
	}
	success := false
	if task != nil {
		defer func() { task.finish(success) }()
	}
	if err := write(w); err != nil {
		_ = tmp.Close()
		return bundleArtifact{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return bundleArtifact{}, err
	}
	if err := tmp.Close(); err != nil {
		return bundleArtifact{}, err
	}
	info, err := os.Stat(tmpName)
	if err != nil {
		return bundleArtifact{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return bundleArtifact{}, err
	}
	success = true
	return bundleArtifact{Size: info.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// Versioned artifact names let the manifest publish a complete refresh with a
// single atomic rename. An interrupted refresh leaves the previous manifest's
// referenced files untouched.
func writeVersionedArtifact(root, prefix, suffix string, write func(io.Writer) error) (string, bundleArtifact, error) {
	dirRel := filepath.Dir(filepath.FromSlash(prefix))
	if err := ensureBundleDirectory(root, dirRel); err != nil {
		return "", bundleArtifact{}, err
	}
	dir := filepath.Join(root, dirRel)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", bundleArtifact{}, err
	}
	stage, err := os.CreateTemp(dir, ".claudex-stage-*")
	if err != nil {
		return "", bundleArtifact{}, err
	}
	stagePath := stage.Name()
	_ = stage.Close()
	_ = os.Remove(stagePath)
	label := filepath.Base(filepath.FromSlash(prefix)) + suffix
	artifact, err := writeArtifactWithProgress(stagePath, label, true, write)
	if err != nil {
		return "", bundleArtifact{}, err
	}
	base := filepath.Base(filepath.FromSlash(prefix)) + "-" + artifact.SHA256[:12] + suffix
	relative := filepath.ToSlash(filepath.Join(dirRel, base))
	target := filepath.Join(root, filepath.FromSlash(relative))
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			_ = os.Remove(stagePath)
			return "", bundleArtifact{}, fmt.Errorf("bundle artifact target %s is not a regular file", relative)
		}
		got, hashErr := hashFile(target)
		_ = os.Remove(stagePath)
		if hashErr != nil || got != artifact {
			return "", bundleArtifact{}, fmt.Errorf("content-addressed artifact collision at %s", relative)
		}
		return relative, artifact, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(stagePath)
		return "", bundleArtifact{}, err
	}
	if err := os.Rename(stagePath, target); err != nil {
		_ = os.Remove(stagePath)
		return "", bundleArtifact{}, err
	}
	return relative, artifact, nil
}

func writeManifestAtomic(path string, m bundleManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = writeArtifact(path, func(w io.Writer) error { _, err := w.Write(data); return err })
	return err
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

func hashFile(path string) (bundleArtifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return bundleArtifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return bundleArtifact{}, err
	}
	return bundleArtifact{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func hashFileWithProgress(path, label string, size int64) (bundleArtifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return bundleArtifact{}, err
	}
	defer f.Close()
	task := archiveProgress.start(label, size)
	success := false
	defer func() { task.finish(success) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(h, progressWriter{dst: io.Discard, task: task}), f)
	if err != nil {
		return bundleArtifact{}, err
	}
	success = true
	return bundleArtifact{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
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
	if m.ImageTag != run.PackImageTag(m.BundleID) {
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
		volumeNames[original] = run.PackVolumeName(m.BundleID, original)
		if err := materializePackVolume(dx, o.bundleDir, m, original, volumeNames[original], o.refresh); err != nil {
			return err
		}
	}
	runOptions, err := run.ParseArgs(o.runArgs)
	if err != nil {
		return err
	}
	runOptions.PackID = m.BundleID
	runOptions.Image = m.ImageTag
	runOptions.PackImageID = m.ImageID
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
			return fmt.Errorf("stop pack session before write-back: %w", err)
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
			return fmt.Errorf("bundle changed during the session; refusing to write back stale pack volumes")
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
	return filepath.Join(home, ".claudex", "pack-volumes", bundleID, volume+".sha256"), nil
}

func materializePackVolume(dx dockerx.Docker, bundleDir string, m bundleManifest, original, volume string, refresh bool) error {
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
		if labels[packLabel] != m.BundleID {
			return fmt.Errorf("volume %s exists but is not owned by bundle %s", volume, m.BundleID)
		}
		if !refresh {
			state, err := os.ReadFile(statePath)
			if err != nil {
				return fmt.Errorf("cannot verify cached pack volume %s; use --refresh: %w", volume, err)
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
	_, err = dx.VolumeCreateLabeled(volume, map[string]string{packLabel: m.BundleID})
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

func destroyBundleResources(dx dockerx.Docker, m bundleManifest) error {
	if err := destroyBundleContainers(dx, m); err != nil {
		return err
	}
	volumes, _ := selectedVolumeNames(nil)
	var failures []string
	for _, original := range volumes {
		name := run.PackVolumeName(m.BundleID, original)
		exists, err := dx.VolumeExists(name)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !exists {
			continue
		}
		labels, err := dx.VolumeLabels(name)
		if err != nil || labels[packLabel] != m.BundleID {
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
		expectedVolumes[run.PackVolumeName(m.BundleID, original)] = true
	}
	var victims []string
	for _, name := range names {
		container, err := dx.Inspect(name)
		if err != nil {
			continue
		}
		if container.Labels[packLabel] != m.BundleID {
			continue
		}
		if container.Status == "running" {
			return fmt.Errorf("cannot destroy bundle %s while session container %s is running", m.BundleID, name)
		}
		if container.Image != m.ImageTag {
			return fmt.Errorf("refusing to remove pack-labeled container %s with unexpected image", name)
		}
		if len(container.Volumes) != len(expectedVolumes) {
			return fmt.Errorf("refusing to remove pack-labeled container %s with unverified volume mounts", name)
		}
		seenVolumes := map[string]bool{}
		for _, volume := range container.Volumes {
			if !expectedVolumes[volume] || seenVolumes[volume] {
				return fmt.Errorf("refusing to remove pack-labeled container %s with unexpected volume %s", name, volume)
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
