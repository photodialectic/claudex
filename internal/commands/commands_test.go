package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/run"
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

func TestParseBashArgs(t *testing.T) {
	name, err := parseBashArgs([]string{"r1"})
	if err != nil || name != "r1" {
		t.Fatalf("expected r1, got %q err=%v", name, err)
	}
	if name, err := parseBashArgs(nil); err != nil || name != "" {
		t.Fatalf("expected empty name, got %q err=%v", name, err)
	}
	if _, err := parseBashArgs([]string{"--name", "r1"}); err == nil || !strings.Contains(err.Error(), "--name is not supported") {
		t.Fatalf("expected --name hint, got %v", err)
	}
	if _, err := parseBashArgs([]string{"--bogus"}); err == nil || !strings.Contains(err.Error(), "unknown arg") {
		t.Fatalf("expected unknown arg error, got %v", err)
	}
	if _, err := parseBashArgs([]string{"r1", "r2"}); err == nil || !strings.Contains(err.Error(), "unexpected arg") {
		t.Fatalf("expected unexpected arg error, got %v", err)
	}
}

func TestBashWithDocker_ExecsBashInTarget(t *testing.T) {
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{
		"r1": {Name: "r1", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}},
	}}
	if err := bashWithDocker(f, "r1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.ExecInteractiveCalls) != 1 {
		t.Fatalf("expected one exec call, got %d", len(f.ExecInteractiveCalls))
	}
	call := f.ExecInteractiveCalls[0]
	if call.Name != "r1" || len(call.Cmd) != 1 || call.Cmd[0] != "bash" {
		t.Fatalf("unexpected exec call %+v", call)
	}
}

func TestBashWithDocker_AutoPicksSingleRunning(t *testing.T) {
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{
		"only": {Name: "only", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}},
	}}
	if err := bashWithDocker(f, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.ExecInteractiveCalls) != 1 || f.ExecInteractiveCalls[0].Name != "only" {
		t.Fatalf("expected auto-pick of 'only', got %+v", f.ExecInteractiveCalls)
	}
}

func TestBashWithDocker_StatusErrors(t *testing.T) {
	// No running containers
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{}}
	if err := bashWithDocker(f, ""); err == nil || !strings.Contains(err.Error(), "no running claudex containers") {
		t.Fatalf("expected no running error, got %v", err)
	}
	if len(f.ExecInteractiveCalls) != 0 {
		t.Fatalf("expected no exec calls, got %+v", f.ExecInteractiveCalls)
	}

	// Named container not running
	f2 := &dockerx.Fake{Containers: map[string]dockerx.Container{
		"s1": {Name: "s1", Status: "exited", Labels: map[string]string{"com.claudex.signature": "x"}},
	}}
	if err := bashWithDocker(f2, "s1"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected not running error, got %v", err)
	}
	if len(f2.ExecInteractiveCalls) != 0 {
		t.Fatalf("expected no exec calls, got %+v", f2.ExecInteractiveCalls)
	}

	// Multiple running containers without --name (non-interactive stdin)
	f3 := &dockerx.Fake{Containers: map[string]dockerx.Container{
		"a": {Name: "a", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}},
		"b": {Name: "b", Status: "running", Labels: map[string]string{"com.claudex.signature": "x"}},
	}}
	if err := bashWithDocker(f3, ""); err == nil || !strings.Contains(err.Error(), "multiple running claudex containers") {
		t.Fatalf("expected multiple running error, got %v", err)
	}
	if len(f3.ExecInteractiveCalls) != 0 {
		t.Fatalf("expected no exec calls, got %+v", f3.ExecInteractiveCalls)
	}
}

func TestCallbackUsageErrors(t *testing.T) {
	if err := Callback(nil); err == nil || !strings.Contains(err.Error(), "usage: claudex callback") {
		t.Fatalf("expected usage error, got %v", err)
	}
	if err := Callback([]string{"--name"}); err == nil || !strings.Contains(err.Error(), "--name requires a value") {
		t.Fatalf("expected missing value error, got %v", err)
	}
	if err := Callback([]string{"--container", "x", "http://localhost/"}); err == nil || !strings.Contains(err.Error(), "--container is not supported") {
		t.Fatalf("expected --container hint, got %v", err)
	}
	if err := Callback([]string{"--bogus", "http://localhost/"}); err == nil || !strings.Contains(err.Error(), "unknown arg") {
		t.Fatalf("expected unknown arg error, got %v", err)
	}
	if err := Callback([]string{"://bad"}); err == nil || !strings.Contains(err.Error(), "invalid callback URL") {
		t.Fatalf("expected invalid URL error, got %v", err)
	}
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

func TestFilterContainers(t *testing.T) {
	cons := []dockerx.Container{
		{Name: "claudex-bundle-aaa-project", Labels: map[string]string{run.BundleLabel: "aaa", "com.claudex.signature": "sig1", "com.claudex.slug": "project"}},
		{Name: "claudex-project", Labels: map[string]string{"com.claudex.signature": "sig2", "com.claudex.slug": "project"}},
		{Name: "claudex-bundle-bbb-project", Labels: map[string]string{run.BundleLabel: "bbb", "com.claudex.signature": "sig1"}},
	}
	cases := []struct {
		name    string
		filters map[string]string
		want    []string
		wantErr bool
	}{
		{"no filters keep all", nil, []string{"claudex-bundle-aaa-project", "claudex-project", "claudex-bundle-bbb-project"}, false},
		{"bundle id", map[string]string{"bundle": "aaa"}, []string{"claudex-bundle-aaa-project"}, false},
		{"bundle id without match", map[string]string{"bundle": "zzz"}, nil, false},
		{"name glob", map[string]string{"name": "claudex-bundle-*"}, []string{"claudex-bundle-aaa-project", "claudex-bundle-bbb-project"}, false},
		{"signature", map[string]string{"signature": "sig1"}, []string{"claudex-bundle-aaa-project", "claudex-bundle-bbb-project"}, false},
		{"slug glob", map[string]string{"slug": "proj*"}, []string{"claudex-bundle-aaa-project", "claudex-project"}, false},
		{"empty name filter drops all", map[string]string{"name": ""}, nil, false},
		{"invalid name pattern", map[string]string{"name": "["}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := filterContainers(cons, tc.filters)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			var names []string
			for _, c := range got {
				names = append(names, c.Name)
			}
			if len(names) != len(tc.want) {
				t.Fatalf("got %v, want %v", names, tc.want)
			}
			for i := range names {
				if names[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", names, tc.want)
				}
			}
		})
	}
}
