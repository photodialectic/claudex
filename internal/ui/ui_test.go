package ui

import (
    "bufio"
    "strings"
    "testing"
)

func TestPromptForWorkspaceSelection(t *testing.T) {
    entries := []string{"a", "b", "c"}
    // Input: duplicates, spaces and commas
    in := "1, 2 2 3\n"
    reader := bufio.NewReader(strings.NewReader(in))
    got, err := PromptForWorkspaceSelection(reader, entries)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    want := []string{"a", "b", "c"}
    if len(got) != len(want) {
        t.Fatalf("len mismatch got %v want %v", got, want)
    }
    for i := range want {
        if got[i] != want[i] {
            t.Fatalf("got %v want %v", got, want)
        }
    }
}

func TestPromptForDestination(t *testing.T) {
    reader := bufio.NewReader(strings.NewReader("\n"))
    got, err := PromptForDestination(reader)
    if err != nil || got != "/tmp" {
        t.Fatalf("default dest failed: %v %q", err, got)
    }
    reader = bufio.NewReader(strings.NewReader("/x\n"))
    got, err = PromptForDestination(reader)
    if err != nil || got != "/x" {
        t.Fatalf("explicit dest failed: %v %q", err, got)
    }
}

func TestPullIgnoreSet(t *testing.T) {
    ig := PullIgnoreSet()
    if !ig["AGENTS.md"] || !ig["CLAUDE.md"] || !ig["GEMINI.md"] {
        t.Fatalf("ignore set missing expected keys: %v", ig)
    }
}

