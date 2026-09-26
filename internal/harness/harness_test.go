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
	for _, h := range Registry() {
		for _, m := range h.Mounts {
			if m.Container == "" {
				continue
			}
			if !strings.Contains(rendered, m.Container) {
				t.Fatalf("missing mount target %q in:\n%s", m.Container, rendered)
			}
		}
	}
}

func TestRenderDockerfileLayersContainsEveryTool(t *testing.T) {
	rendered := RenderDockerfileLayers()
	for _, h := range Registry() {
		arg := ToolBuildArg(h.Name)
		if len(h.Install) == 0 {
			if strings.Contains(rendered, arg) {
				t.Fatalf("harness %q has no install steps but rendered a layer:\n%s", h.Name, rendered)
			}
			continue
		}
		if !strings.Contains(rendered, "ARG "+arg+"=") {
			t.Fatalf("missing ARG %s= in:\n%s", arg, rendered)
		}
		for _, step := range h.Install {
			if !layerConsumesBuildArg(rendered, arg, step) {
				t.Fatalf("missing RUN layer for %q consuming %s in:\n%s", step, arg, rendered)
			}
		}
	}
}

// layerConsumesBuildArg reports whether rendered contains a RUN layer that
// both executes step and references ${arg}, so bumping the build arg busts
// that layer's cache — the reason the ARG + RUN pair exists.
func layerConsumesBuildArg(rendered, arg, step string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "RUN ") &&
			strings.Contains(line, "${"+arg+"}") &&
			strings.Contains(line, step) {
			return true
		}
	}
	return false
}
