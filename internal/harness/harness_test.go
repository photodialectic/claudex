package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range Registry() {
		if h.Name == "" {
			t.Fatalf("harness with empty name at index of %d harnesses", len(Registry()))
		}
		if seen[h.Name] {
			t.Fatalf("duplicate harness name %q", h.Name)
		}
		seen[h.Name] = true
	}
}

func TestRegistryCoreHarnessesPresent(t *testing.T) {
	for _, name := range []string{"claude", "codex", "copilot", "gemini", "opencode", "claudex"} {
		if _, ok := ByName(name); !ok {
			t.Fatalf("expected core harness %q to be present", name)
		}
	}
}

func TestEnvVarsDedupAndForward(t *testing.T) {
	got := EnvVars()
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Fatalf("duplicate env var %q", k)
		}
		seen[k] = true
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AI_API_MK", "GEMINI_API_KEY"} {
		if !seen[k] {
			t.Fatalf("expected env var %q to be forwarded", k)
		}
	}
}

func TestOpenCodeHasTwoMounts(t *testing.T) {
	h, ok := ByName("opencode")
	if !ok {
		t.Fatal("opencode harness missing")
	}
	if len(h.Mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(h.Mounts))
	}
}

func TestMergeClaudeJSONPreservesProjects(t *testing.T) {
	dst := `{"mcpServers":{"old":{"type":"http"}},"projects":{"/a":{"history":["x"]}},"permissions":{"allow":["Bash"]}}`
	src := `{"mcpServers":{"new":{"type":"stdio"}},"hooks":{"Post":"echo hi"}}`

	merged, err := MergeClaudeJSON([]byte(dst), []byte(src))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	if len(m["mcpServers"]) == 0 || !containsRaw(m["mcpServers"], "new") {
		t.Fatalf("expected mcpServers to reflect host (new), got %s", m["mcpServers"])
	}
	if containsRaw(m["mcpServers"], "old") {
		t.Fatalf("expected host mcpServers to replace volume, got %s", m["mcpServers"])
	}
	if len(m["projects"]) == 0 || !containsRaw(m["projects"], "/a") {
		t.Fatalf("expected projects to be preserved, got %s", m["projects"])
	}
	if len(m["hooks"]) == 0 {
		t.Fatalf("expected hooks to be merged in, got %v", m)
	}
}

func TestMergeClaudeJSONEmptyDstReturnsSrc(t *testing.T) {
	src := `{"mcpServers":{"a":{}}}`
	merged, err := MergeClaudeJSON(nil, []byte(src))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if string(merged) != src {
		t.Fatalf("expected src wholesale, got %s", merged)
	}
}

func containsRaw(raw json.RawMessage, substr string) bool {
	return len(raw) > 0 && strings.Contains(string(raw), substr)
}

func TestToolBuildArg(t *testing.T) {
	if got := ToolBuildArg("codex"); got != "CLAUDEX_TOOL_codex" {
		t.Fatalf("expected CLAUDEX_TOOL_codex, got %q", got)
	}
}

func TestRenderDockerfileMountDirsCoversEveryMountTarget(t *testing.T) {
	rendered := RenderDockerfileMountDirs()
	if !strings.HasPrefix(rendered, "RUN mkdir -p ") {
		t.Fatalf("expected RUN mkdir -p prefix, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "&& chown -R node:node ") {
		t.Fatalf("expected chown directive, got:\n%s", rendered)
	}
	for _, dir := range []string{
		"/home/node/.claude",
		"/home/node/.claude-state",
		"/home/node/.codex",
		"/home/node/.copilot",
		"/home/node/.gemini",
		"/home/node/.pi",
		"/home/node/.claudex",
		"/home/node/.config/opencode",
		"/home/node/.local/share/opencode",
	} {
		if !strings.Contains(rendered, dir) {
			t.Fatalf("missing mount target %q in:\n%s", dir, rendered)
		}
	}
}

func TestRenderDockerfileLayersContainsEveryTool(t *testing.T) {
	rendered := RenderDockerfileLayers()
	for _, n := range []string{"claude", "codex", "copilot", "gemini", "opencode"} {
		if !strings.Contains(rendered, ToolBuildArg(n)) {
			t.Fatalf("missing ARG for %q in:\n%s", n, rendered)
		}
	}
	if strings.Contains(rendered, ToolBuildArg("claudex")) {
		t.Fatalf("claudex (no install) should not render a layer:\n%s", rendered)
	}
	for _, pkg := range []string{"@openai/codex", "@github/copilot", "@google/gemini-cli", "opencode-ai", "claude.ai/install.sh"} {
		if !strings.Contains(rendered, pkg) {
			t.Fatalf("missing install command %q in:\n%s", pkg, rendered)
		}
	}
}
