package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressDisplayAggregatesConcurrentStreams(t *testing.T) {
	var output bytes.Buffer
	display := newProgressDisplay(&output, true)
	first := display.start("image.tar.gz", 200<<20)
	second := display.start("volumes/codex.tar.gz", 0)
	first.add(100 << 20)
	second.add(20 << 20)
	first.finish(true)
	second.finish(false)
	text := output.String()
	if !strings.Contains(text, "streams") || !strings.Contains(text, "image.tar.gz") || !strings.Contains(text, "✓") || !strings.Contains(text, "✗") {
		t.Fatalf("progress output did not show aggregate and completion states: %q", text)
	}
}

func TestProgressDisplayLogsMilestonesWithoutTerminal(t *testing.T) {
	var output bytes.Buffer
	display := newProgressDisplay(&output, false)
	task := display.start("large.tar.gz", 0)
	task.add(progressTick + 1)
	task.finish(true)
	if !strings.Contains(output.String(), "128.0 MiB written") {
		t.Fatalf("expected a byte milestone for non-terminal output, got %q", output.String())
	}
}
