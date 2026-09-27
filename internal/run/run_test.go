package run

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/photodialectic/claudex/internal/dockerx"
)

func TestParseArgsAndDerive(t *testing.T) {
	args := []string{"--host-network", "--name", "X", "--parallel", "--strict-mounts", "--replace", "--no-git", "."}
	o, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.Network != "host" || !o.StrictMounts || !o.ForceReplace || !o.AlwaysParallel || !o.SkipGit {
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

func TestBundleRunNamespacesImageVolumesAndOverrideName(t *testing.T) {
	o, err := ParseArgs([]string{"--name", "project", "--replace", "."})
	if err != nil {
		t.Fatal(err)
	}
	o.BundleID = "0123456789abcdef0123456789abcdef"
	o.Image = BundleImageTag(o.BundleID)
	o.BundleImageID = "sha256:test"
	o.VolumeNames = map[string]string{"claudex-claude": BundleVolumeName(o.BundleID, "claudex-claude")}
	if err := o.Derive(); err != nil {
		t.Fatal(err)
	}
	if o.Name != "claudex-bundle-"+o.BundleID+"-project" {
		t.Fatalf("container override not isolated: %q", o.Name)
	}
	args, err := o.BuildRunArgs()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, BundleVolumeName(o.BundleID, "claudex-claude")+":/home/node/.claude") {
		t.Fatalf("missing namespaced volume: %v", args)
	}
	if !contains(args, BundleLabel+"="+o.BundleID) || !contains(args, o.Image) {
		t.Fatalf("missing bundle identity/image: %v", args)
	}
	if contains(args, "/var/run/docker.sock:/var/run/docker.sock") {
		t.Fatalf("bundle run must not expose the host Docker socket: %v", args)
	}
	for _, arg := range configMountArgs(o) {
		if strings.Contains(arg, "claudex-claude:") && !strings.Contains(arg, BundleVolumeName(o.BundleID, "claudex-claude")) {
			t.Fatalf("ordinary claudex volume leaked into bundle mounts: %v", arg)
		}
	}
}

func TestBundleContainerOwnershipValidation(t *testing.T) {
	o := Options{
		BundleID:      "bundle-id",
		Image:         "claudex-bundle:bundle-id",
		BundleImageID: "sha256:expected",
		VolumeNames:   map[string]string{"claudex-claude": "claudex-bundle-bundle-id-claudex-claude"},
		Name:          "claudex-bundle-bundle-id-test",
	}
	valid := &dockerx.Container{
		Image:   o.Image,
		ImageID: o.BundleImageID,
		Labels:  map[string]string{BundleLabel: o.BundleID},
		Volumes: []string{"claudex-bundle-bundle-id-claudex-claude"},
	}
	if err := validateBundleContainer(valid, o); err != nil {
		t.Fatalf("valid bundle container rejected: %v", err)
	}
	valid.Labels[BundleLabel] = "another-bundle"
	if err := validateBundleContainer(valid, o); err == nil {
		t.Fatal("container owned by another bundle was accepted")
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
	if len(f.ExecCalls) != 0 {
		t.Fatalf("expected no firewall exec calls, got %v", f.ExecCalls)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output when firewall disabled, got %q", out.String())
	}
	if err.Len() != 0 {
		t.Fatalf("expected no errors when firewall disabled, got %q", err.String())
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
