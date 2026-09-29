package commands

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func pruneUnreferencedArtifacts(root string, old, current map[string]bundleArtifact) {
	for relative := range old {
		if _, ok := current[relative]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(relative)))
	}
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

func newBundleID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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
