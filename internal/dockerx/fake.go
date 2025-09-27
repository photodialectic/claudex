package dockerx

import "io"

// Fake is a simple in-memory Docker implementation for tests.
type Fake struct {
    Containers map[string]Container
    PSNames    []string
    RunErr     error
    ExecErr    error
    CPErr      error
    StartErr   error
    RemoveErr  error
    BuildErr   error
    ImageExistsVal bool
    ImageExistsErr error
    ExecInteractiveErr error
    ExecOutputOut []byte
    ExecOutputErr error
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

func (f *Fake) Run(args ...string) error  { return f.RunErr }
func (f *Fake) Exec(args ...string) error { return f.ExecErr }
func (f *Fake) CP(src, dst string) error  { return f.CPErr }
func (f *Fake) Start(name string) error { return f.StartErr }
func (f *Fake) Remove(name string, force bool) error { return f.RemoveErr }
func (f *Fake) ImageExists(tag string) (bool, error) { return f.ImageExistsVal, f.ImageExistsErr }
func (f *Fake) Build(tag, contextDir string, noCache bool) error { return f.BuildErr }
func (f *Fake) ExecInteractive(name string, cmd []string, in io.Reader, out, errOut io.Writer) error {
    return f.ExecInteractiveErr
}
func (f *Fake) ExecOutput(name string, cmd []string) ([]byte, error) { return f.ExecOutputOut, f.ExecOutputErr }

// ErrNotFound is a minimal error type to simulate missing container.
type ErrNotFound string

func (e ErrNotFound) Error() string { return "no such container: " + string(e) }
