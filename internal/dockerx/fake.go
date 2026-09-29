package dockerx

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
)

// Fake is a simple in-memory Docker implementation for tests.
type Fake struct {
	Containers  map[string]Container
	PSNames     []string
	RunErr      error
	ExecErr     error
	CPErr       error
	StartErr    error
	RemoveErr   error
	RemoveCalls []struct {
		Name  string
		Force bool
	}
	BuildErr             error
	BuildTag             string
	BuildContext         string
	BuildOpts            BuildOptions
	ImageExistsVal       bool
	ImageExistsErr       error
	ExecInteractiveErr   error
	ExecInteractiveCalls []struct {
		Name string
		Cmd  []string
	}
	ExecOutputOut   []byte
	ExecOutputErr   error
	LogsOut         []byte
	LogsErr         error
	ExecCalls       [][]string
	ExecOutputCalls [][]string
	LogsCalls       []struct {
		Name string
		Tail int
	}
	VolumeCreateCalls     []string
	VolumeCreateCreated   bool
	VolumeCreateErr       error
	CopyHostToVolumeCalls []struct {
		HostPath   string
		Volume     string
		VolumePath string
	}
	CopyVolumeToHostCalls []struct {
		Volume     string
		VolumePath string
		HostPath   string
	}
	TagImageCalls      [][2]string
	ImageRemoveCalls   []string
	ImageIDVal         string
	ImageIDErr         error
	ImageArchVal       string
	ImageArchErr       error
	LocalArchVal       string
	LocalArchErr       error
	Volumes            map[string]map[string]string
	VolumeExistsErr    error
	VolumeRemoveCalls  []string
	VolumeRemoveErr    error
	StopCalls          []string
	StopErr            error
	VolumeInUseVals    map[string]bool
	VolumeInUseErr     error
	VolumeCreateLabels map[string]map[string]string
	SaveImageFn        func(string, io.Writer) error
	LoadImageFn        func(io.Reader) error
	TarVolumeFn        func(string, string, io.Writer) error
	ExtractVolumeFn    func(string, string, io.Reader) error
	ExtractVolumeCalls []struct {
		Image  string
		Volume string
	}
}

func (f *Fake) Inspect(name string) (Container, error) {
	if c, ok := f.Containers[name]; ok {
		return c, nil
	}
	return Container{}, ErrNotFound(name)
}

func (f *Fake) PS(includeStopped bool) ([]string, error) {
	if len(f.PSNames) > 0 {
		return append([]string(nil), f.PSNames...), nil
	}
	names := make([]string, 0, len(f.Containers))
	for n := range f.Containers {
		names = append(names, n)
	}
	return names, nil
}

func (f *Fake) Run(args ...string) error { return f.RunErr }
func (f *Fake) Exec(args ...string) error {
	call := append([]string(nil), args...)
	f.ExecCalls = append(f.ExecCalls, call)
	return f.ExecErr
}
func (f *Fake) CP(src, dst string) error { return f.CPErr }
func (f *Fake) Start(name string) error  { return f.StartErr }
func (f *Fake) Remove(name string, force bool) error {
	f.RemoveCalls = append(f.RemoveCalls, struct {
		Name  string
		Force bool
	}{Name: name, Force: force})
	if f.RemoveErr == nil {
		delete(f.Containers, name)
	}
	return f.RemoveErr
}
func (f *Fake) ImageExists(tag string) (bool, error) { return f.ImageExistsVal, f.ImageExistsErr }
func (f *Fake) Build(tag, contextDir string, opts BuildOptions) error {
	f.BuildTag = tag
	f.BuildContext = contextDir
	f.BuildOpts = opts
	return f.BuildErr
}
func (f *Fake) ExecInteractive(name string, cmd []string, in io.Reader, out, errOut io.Writer) error {
	f.ExecInteractiveCalls = append(f.ExecInteractiveCalls, struct {
		Name string
		Cmd  []string
	}{Name: name, Cmd: append([]string(nil), cmd...)})
	return f.ExecInteractiveErr
}
func (f *Fake) ExecOutput(name string, cmd []string) ([]byte, error) {
	call := append([]string{name}, cmd...)
	f.ExecOutputCalls = append(f.ExecOutputCalls, call)
	return f.ExecOutputOut, f.ExecOutputErr
}

func (f *Fake) Logs(name string, tail int) ([]byte, error) {
	f.LogsCalls = append(f.LogsCalls, struct {
		Name string
		Tail int
	}{Name: name, Tail: tail})
	return f.LogsOut, f.LogsErr
}

func (f *Fake) VolumeCreate(name string) (bool, error) {
	f.VolumeCreateCalls = append(f.VolumeCreateCalls, name)
	return f.VolumeCreateCreated, f.VolumeCreateErr
}

func (f *Fake) TagImage(source, target string) error {
	f.TagImageCalls = append(f.TagImageCalls, [2]string{source, target})
	return nil
}
func (f *Fake) ImageRemove(tag string) error {
	f.ImageRemoveCalls = append(f.ImageRemoveCalls, tag)
	return nil
}
func (f *Fake) ImageID(tag string) (string, error) { return f.ImageIDVal, f.ImageIDErr }
func (f *Fake) ImageArch(tag string) (string, error) {
	if f.ImageArchVal == "" {
		return "arm64", f.ImageArchErr
	}
	return f.ImageArchVal, f.ImageArchErr
}
func (f *Fake) LocalArch() (string, error) {
	if f.LocalArchVal == "" {
		return "arm64", f.LocalArchErr
	}
	return f.LocalArchVal, f.LocalArchErr
}
func (f *Fake) VolumeExists(name string) (bool, error) {
	if f.VolumeExistsErr != nil {
		return false, f.VolumeExistsErr
	}
	_, ok := f.Volumes[name]
	return ok, nil
}
func (f *Fake) VolumeRemove(name string) error {
	f.VolumeRemoveCalls = append(f.VolumeRemoveCalls, name)
	if f.VolumeRemoveErr != nil {
		return f.VolumeRemoveErr
	}
	delete(f.Volumes, name)
	return nil
}
func (f *Fake) VolumeCreateLabeled(name string, labels map[string]string) (bool, error) {
	if f.VolumeCreateLabels == nil {
		f.VolumeCreateLabels = map[string]map[string]string{}
	}
	copyLabels := map[string]string{}
	for k, v := range labels {
		copyLabels[k] = v
	}
	f.VolumeCreateLabels[name] = copyLabels
	if f.Volumes == nil {
		f.Volumes = map[string]map[string]string{}
	}
	if _, exists := f.Volumes[name]; exists {
		return false, nil
	}
	f.Volumes[name] = copyLabels
	return true, nil
}
func (f *Fake) VolumeInUse(name string) (bool, error) {
	return f.VolumeInUseVals[name], f.VolumeInUseErr
}
func (f *Fake) VolumeLabels(name string) (map[string]string, error) {
	labels := map[string]string{}
	for k, v := range f.Volumes[name] {
		labels[k] = v
	}
	return labels, nil
}
func (f *Fake) Stop(name string) error {
	f.StopCalls = append(f.StopCalls, name)
	return f.StopErr
}
func (f *Fake) SaveImageTo(tag string, dst io.Writer) error {
	if f.SaveImageFn != nil {
		return f.SaveImageFn(tag, dst)
	}
	_, err := io.WriteString(dst, "fake docker image")
	return err
}
func (f *Fake) LoadImage(src io.Reader) error {
	if f.LoadImageFn != nil {
		return f.LoadImageFn(src)
	}
	_, err := io.Copy(io.Discard, src)
	return err
}
func (f *Fake) TarVolumeTo(image, volume string, dst io.Writer) error {
	if f.TarVolumeFn != nil {
		return f.TarVolumeFn(image, volume, dst)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	data := []byte("fake volume: " + volume)
	if err := tw.WriteHeader(&tar.Header{Name: "data", Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	_, err := io.Copy(dst, &archive)
	if err != nil {
		return fmt.Errorf("write fake volume archive: %w", err)
	}
	return nil
}
func (f *Fake) TarVolumeToContext(ctx context.Context, image, volume string, dst io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.TarVolumeTo(image, volume, dst)
}
func (f *Fake) ExtractVolumeFrom(image, volume string, src io.Reader) error {
	f.ExtractVolumeCalls = append(f.ExtractVolumeCalls, struct {
		Image  string
		Volume string
	}{Image: image, Volume: volume})
	if f.ExtractVolumeFn != nil {
		return f.ExtractVolumeFn(image, volume, src)
	}
	_, err := io.Copy(io.Discard, src)
	return err
}

func (f *Fake) CopyHostToVolume(hostPath, volume, volumePath string) error {
	f.CopyHostToVolumeCalls = append(f.CopyHostToVolumeCalls, struct {
		HostPath   string
		Volume     string
		VolumePath string
	}{HostPath: hostPath, Volume: volume, VolumePath: volumePath})
	return nil
}

func (f *Fake) CopyVolumeToHost(volume, volumePath, hostPath string) error {
	f.CopyVolumeToHostCalls = append(f.CopyVolumeToHostCalls, struct {
		Volume     string
		VolumePath string
		HostPath   string
	}{Volume: volume, VolumePath: volumePath, HostPath: hostPath})
	return nil
}

// ErrNotFound is a minimal error type to simulate missing container.
type ErrNotFound string

func (e ErrNotFound) Error() string { return "no such container: " + string(e) }
