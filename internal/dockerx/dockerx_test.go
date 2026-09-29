package dockerx

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// stubExecCommand replaces the execCommand seam with a shell script that
// stands in for the docker binary, returning a restore function.
func stubExecCommand(script string) func() {
	old := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", script)
	}
	return func() { execCommand = old }
}

func TestRunIncludesDockerOutputInError(t *testing.T) {
	restore := stubExecCommand("echo 'docker: Error response from daemon: simulated failure' >&2; exit 125")
	defer restore()

	err := (&CLI{}).Run("run", "--name", "c", "claudex", "tail", "-f", "/dev/null")
	if err == nil {
		t.Fatal("expected an error from a failing docker command")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected a wrapped *exec.ExitError, got %T: %v", err, err)
	}
	msg := err.Error()
	for _, want := range []string{"docker run --name c claudex", "exit status 125", "simulated failure"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestRunReturnsNilForSuccessfulCommand(t *testing.T) {
	restore := stubExecCommand("exit 0")
	defer restore()

	if err := (&CLI{}).Run("run", "--name", "c", "claudex"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
