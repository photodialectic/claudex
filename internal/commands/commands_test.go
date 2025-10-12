package commands

import (
	"errors"
	"strings"
	"testing"

	"claudex/internal/dockerx"
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

func TestUpdateWithDockerSetsRefreshToken(t *testing.T) {
	f := &dockerx.Fake{}
	if err := updateWithDocker(f, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.BuildTag != "claudex" {
		t.Fatalf("expected tag 'claudex', got %q", f.BuildTag)
	}
	token, ok := f.BuildOpts.BuildArgs[cliRefreshArg]
	if !ok || token == "" {
		t.Fatalf("expected refresh token, got map %+v", f.BuildOpts.BuildArgs)
	}
	if f.BuildOpts.NoCache {
		t.Fatalf("expected NoCache to be false")
	}
}

func TestUpdateWithDockerNoCacheFlag(t *testing.T) {
	f := &dockerx.Fake{}
	if err := updateWithDocker(f, []string{"--no-cache"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.BuildOpts.NoCache {
		t.Fatalf("expected NoCache to be true")
	}
}

func TestUpdateWithDockerUnknownFlag(t *testing.T) {
	f := &dockerx.Fake{}
	if err := updateWithDocker(f, []string{"--bogus"}); err == nil || !strings.Contains(err.Error(), "unknown arg") {
		t.Fatalf("expected unknown arg error, got %v", err)
	}
}
