package containers

import (
	"encoding/json"
	"testing"
	"time"

	"claudex/internal/dockerx"
)

func TestMountsFromLabel(t *testing.T) {
	mounts := []string{"/a", "/b"}
	b, _ := json.Marshal(mounts)
	c := &dockerx.Container{Labels: map[string]string{"com.claudex.mounts": string(b)}}

	got, err := MountsFromLabel(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("unexpected mounts: %v", got)
	}

	c2 := &dockerx.Container{Labels: map[string]string{}}
	if _, err := MountsFromLabel(c2); err == nil {
		t.Fatalf("expected error for missing label")
	}
}

func TestExists(t *testing.T) {
	f := &dockerx.Fake{Containers: map[string]dockerx.Container{
		"r1": {Name: "r1", Status: "running", CreatedAt: time.Now()},
		"s1": {Name: "s1", Status: "exited", CreatedAt: time.Now()},
	}}

	ok, running, info, err := Exists(f, "r1")
	if err != nil || !ok || !running || info == nil || info.Name != "r1" {
		t.Fatalf("Exists for r1 failed: ok=%v running=%v info=%v err=%v", ok, running, info, err)
	}

	ok, running, _, err = Exists(f, "s1")
	if err != nil || !ok || running {
		t.Fatalf("Exists for s1 failed: ok=%v running=%v err=%v", ok, running, err)
	}

	ok, running, info, err = Exists(f, "nope")
	if err != nil || ok || running || info != nil {
		t.Fatalf("Exists for missing should indicate not found")
	}
}

func TestWarnOrErrorOnMountMismatch(t *testing.T) {
	mounts := []string{"/x"}
	b, _ := json.Marshal(mounts)
	c := &dockerx.Container{Labels: map[string]string{"com.claudex.mounts": string(b)}}

	if err := WarnOrErrorOnMountMismatch(c, []string{"/x"}, true, "n"); err != nil {
		t.Fatalf("should match: %v", err)
	}
	if err := WarnOrErrorOnMountMismatch(c, []string{"/y"}, false, "n"); err != nil {
		t.Fatalf("non-strict mismatch should not error: %v", err)
	}
	if err := WarnOrErrorOnMountMismatch(c, []string{"/y"}, true, "n"); err == nil {
		t.Fatalf("strict mismatch should error")
	}
}
