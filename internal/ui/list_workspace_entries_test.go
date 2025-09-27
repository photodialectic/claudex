package ui

import (
	"testing"

	"claudex/internal/dockerx"
)

func TestListWorkspaceEntriesFiltersAndSorts(t *testing.T) {
	f := &dockerx.Fake{
		ExecOutputOut: []byte("z.txt\nAGENTS.md\n a.txt \nGEMINI.md\nCLAUDE.md\nB.txt\n"),
	}
	got, err := ListWorkspaceEntries(f, "c")
	if err != nil {
		t.Fatalf("ListWorkspaceEntries error: %v", err)
	}
	// Should filter CLAUDE.md, GEMINI.md, AGENTS.md and trim/sort the rest
	want := []string{"B.txt", "a.txt", "z.txt"}
	if len(got) != len(want) {
		t.Fatalf("len got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
