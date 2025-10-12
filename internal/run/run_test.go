package run

import (
	"bytes"
	"errors"
	"testing"

	"claudex/internal/dockerx"
)

func TestParseArgsAndDerive(t *testing.T) {
	args := []string{"--host-network", "--name", "X", "--parallel", "--strict-mounts", "--replace", "--no-git", "."}
	o, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !o.UseHostNetwork || !o.StrictMounts || !o.ForceReplace || !o.AlwaysParallel || !o.SkipGit {
		t.Fatalf("flags not parsed correctly: %+v", o)
	}
	if len(o.Workdirs) != 1 {
		t.Fatalf("expected 1 workdir, got %v", o.Workdirs)
	}
	if err := o.Derive(); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if o.Name == "" || o.Signature == "" || o.Slug == "" || len(o.Normalized) == 0 {
		t.Fatalf("missing derived fields: %+v", o)
	}
}

func TestMaybeInitGitSkipsWhenFlag(t *testing.T) {
	f := &dockerx.Fake{}
	var out, err bytes.Buffer
	maybeInitGit(true, f, "c", &out, &err)
	if len(f.ExecCalls) != 0 || len(f.ExecOutputCalls) != 0 {
		t.Fatalf("expected no docker calls, got exec=%v execOutput=%v", f.ExecCalls, f.ExecOutputCalls)
	}
}

func TestMaybeInitGitInitializesWhenMissing(t *testing.T) {
	f := &dockerx.Fake{ExecOutputErr: errors.New("missing")}
	var out, err bytes.Buffer
	maybeInitGit(false, f, "c", &out, &err)
	if len(f.ExecOutputCalls) == 0 {
		t.Fatalf("expected ExecOutput check, got none")
	}
	if len(f.ExecCalls) != 3 {
		t.Fatalf("expected three exec calls (init, gitignore, add), got %v", f.ExecCalls)
	}
	initCall := f.ExecCalls[0]
	if len(initCall) < 4 || initCall[0] != "c" || initCall[1] != "bash" || initCall[2] != "-c" || initCall[3] != "cd /workspace && git init --quiet" {
		t.Fatalf("unexpected init call: %v", initCall)
	}
	if !bytes.Contains(out.Bytes(), []byte("staged current contents")) {
		t.Fatalf("expected staging message, got %q", out.String())
	}
}

func TestMaybeInitGitNoopWhenExists(t *testing.T) {
	f := &dockerx.Fake{}
	var out, err bytes.Buffer
	maybeInitGit(false, f, "c", &out, &err)
	if len(f.ExecOutputCalls) != 1 {
		t.Fatalf("expected single ExecOutput probe, got %v", f.ExecOutputCalls)
	}
	if len(f.ExecCalls) != 0 {
		t.Fatalf("expected no exec calls, got %v", f.ExecCalls)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}

func TestMaybeInitFirewallSkipsWhenDisabled(t *testing.T) {
	f := &dockerx.Fake{}
	var out, err bytes.Buffer
	maybeInitFirewall(false, f, "c", &out, &err)
	if len(f.ExecCalls) != 1 {
		t.Fatalf("expected single exec call to disable firewall, got %v", f.ExecCalls)
	}
	call := f.ExecCalls[0]
	expected := []string{"c", "bash", "-c", "sudo /usr/local/bin/init-firewall.sh --clear"}
	if len(call) != len(expected) {
		t.Fatalf("unexpected exec call length: %v", call)
	}
	for i := range call {
		if call[i] != expected[i] {
			t.Fatalf("exec call mismatch at %d: got %q want %q", i, call[i], expected[i])
		}
	}
	if !bytes.Contains(out.Bytes(), []byte("Disabling firewall")) {
		t.Fatalf("expected disable message, got %q", out.String())
	}
}

func TestMaybeInitFirewallRunsWhenEnabled(t *testing.T) {
	f := &dockerx.Fake{}
	var out, err bytes.Buffer
	maybeInitFirewall(true, f, "c", &out, &err)
	if len(f.ExecCalls) != 1 {
		t.Fatalf("expected firewall exec, got %v", f.ExecCalls)
	}
	call := f.ExecCalls[0]
	if len(call) < 4 || call[0] != "c" || call[1] != "bash" || call[2] != "-c" || call[3] != "sudo /usr/local/bin/init-firewall.sh" {
		t.Fatalf("unexpected firewall call: %v", call)
	}
	if !bytes.Contains(out.Bytes(), []byte("Initializing firewall")) {
		t.Fatalf("expected firewall message, got %q", out.String())
	}
}
