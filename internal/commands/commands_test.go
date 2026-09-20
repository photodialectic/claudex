package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/photodialectic/claudex/internal/dockerx"
)

func TestPickRunning_ByNameAndStatus(t *testing.T) {
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{}}
	// running container
	f.Containers["r1"] = dockerx.Container{Name: "r1", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}}
	// stopped container
	f.Containers["s1"] = dockerx.Container{Name: "s1", Status: "exited", Labels: map[string]string{"com.claudex.signature": "x"}}

	if name, err := pickRunning(f, "r1"); err != nil || name != "r1" {
		t.Fatalf("expected r1, got %q err=%v", name, err)
	}
	if _, err := pickRunning(f, "s1"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected not running error, got %v", err)
	}
}

func TestPickRunning_AutoSelectionCases(t *testing.T) {
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{}}

	// No running containers
	if _, err := pickRunning(f, ""); err == nil || !strings.Contains(err.Error(), "no running claudex containers") {
		t.Fatalf("expected no running error, got %v", err)
	}

	// One running container
	f.Containers["only"] = dockerx.Container{Name: "only", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}}
	if name, err := pickRunning(f, ""); err != nil || name != "only" {
		t.Fatalf("expected auto-pick 'only', got %q err=%v", name, err)
	}

	// Multiple running containers
	f.Containers["another"] = dockerx.Container{Name: "another", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}}
	if _, err := pickRunning(f, ""); err == nil || !strings.Contains(err.Error(), "multiple running claudex containers") {
		t.Fatalf("expected multiple running error, got %v", err)
	}

	_ = errors.New // avoid unused import if assertions change
}

func TestResolveUpdateTargetsAll(t *testing.T) {
	names, err := resolveUpdateTargets(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(names, "codex") || !containsStr(names, "opencode") || !containsStr(names, "claude") {
		t.Fatalf("expected all tool harnesses, got %v", names)
	}
	if containsStr(names, "claudex") {
		t.Fatalf("claudex (no install) should not be a target, got %v", names)
	}
}

func TestResolveUpdateTargetsNamed(t *testing.T) {
	names, err := resolveUpdateTargets([]string{"codex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 1 || names[0] != "codex" {
		t.Fatalf("expected [codex], got %v", names)
	}
}

func TestResolveUpdateTargetsUnknown(t *testing.T) {
	if _, err := resolveUpdateTargets([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("expected unknown harness error, got %v", err)
	}
}

func TestResolveUpdateTargetsNoInstall(t *testing.T) {
	if _, err := resolveUpdateTargets([]string{"claudex"}); err == nil || !strings.Contains(err.Error(), "no install command") {
		t.Fatalf("expected no install command error, got %v", err)
	}
}

func TestHarnessUpdateSetsBuildArgs(t *testing.T) {
	f := &dockerx.Fake{}
	if err := harnessUpdateWithDocker(f, []string{"codex"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.BuildTag != "claudex" {
		t.Fatalf("expected tag 'claudex', got %q", f.BuildTag)
	}
	if len(f.BuildOpts.BuildArgs) != 1 {
		t.Fatalf("expected one build arg, got %+v", f.BuildOpts.BuildArgs)
	}
	if token := f.BuildOpts.BuildArgs["CLAUDEX_TOOL_codex"]; token == "" {
		t.Fatalf("expected codex build arg token, got %+v", f.BuildOpts.BuildArgs)
	}
	if f.BuildOpts.NoCache {
		t.Fatalf("expected NoCache false")
	}
}

func TestHarnessUpdateNoCache(t *testing.T) {
	f := &dockerx.Fake{}
	if err := harnessUpdateWithDocker(f, []string{"--no-cache"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.BuildOpts.NoCache {
		t.Fatalf("expected NoCache true")
	}
	if len(f.BuildOpts.BuildArgs) == 0 {
		t.Fatalf("expected build args for all tools")
	}
}

func containsStr(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
