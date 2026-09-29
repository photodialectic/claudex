package commands

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestLockBundleMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockBundle(dir)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	_, err = lockBundle(dir)
	if err == nil {
		t.Fatal("expected a second lock to be refused while the bundle is locked")
	}
	if !strings.Contains(err.Error(), "locked by another") {
		t.Fatalf("unexpected error: %v", err)
	}
	unlock()

	unlock, err = lockBundle(dir)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	unlock()
}

func TestNewBundleID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id, err := newBundleID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 32 || id != strings.ToLower(id) {
			t.Fatalf("ID %q is not 32 lowercase hex characters", id)
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("ID %q is not hexadecimal: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
}
