package harness

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed harnesses.yaml
var harnessDefsFS embed.FS

// Kind distinguishes directory mounts from single-file mounts.
type Kind int

const (
	KindDir Kind = iota
	KindFile
)

// Mount describes a single config path shared between host and container via a
// named volume. All harness state lives in one place so adding a harness is a
// single edit to registry (see Registry below).
type Mount struct {
	// HostRel is the path on the host relative to the user's home directory,
	// e.g. ".claude.json" or ".config/opencode".
	HostRel string
	// Container is the mount point inside the container.
	Container string
	// Volume is the name of the shared named volume.
	Volume string
	// VolumePath is the path within the volume for single-file mounts
	// (empty for directory mounts).
	VolumePath string
	// Kind is KindDir or KindFile.
	Kind Kind
}

// HostPath resolves HostRel to an absolute path on the host.
func (m Mount) HostPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, m.HostRel)
}

// MergeFunc merges host config (src) into existing volume state (dst).
type MergeFunc func(dst, src []byte) ([]byte, error)

// Harness describes a supported agent config surface.
type Harness struct {
	Name    string
	EnvVars []string
	Mounts  []Mount
	Merge   MergeFunc
	// Install is the shell command(s) used to install/refresh the tool inside
	// the image. Each element becomes one generated RUN layer in the Dockerfile.
	// Empty for harnesses that ship no tool binary (e.g. "claudex" itself).
	Install []string
}

// DockerfileMarker is the line in the embedded Dockerfile replaced with the
// generated per-tool install layers.
const DockerfileMarker = "# CLAUDEX_HARNESS_LAYERS"

// DockerfileMountDirsMarker is the line in the embedded Dockerfile replaced
// with the generated mkdir/chown directive for every harness config directory.
const DockerfileMountDirsMarker = "# CLAUDEX_HARNESS_DIRS"

// ToolBuildArg returns the image build arg used to bust a single tool's
// install layer during `claudex harness update <name>`.
func ToolBuildArg(name string) string { return "CLAUDEX_TOOL_" + name }

// RenderDockerfileLayers renders one ARG + RUN layer per tool install step,
// so changing a single tool's build arg rebuilds only that tool (plus any
// subsequent layers, per Docker's ordered-layer cache semantics).
func RenderDockerfileLayers() string {
	var b strings.Builder
	for _, h := range registry {
		if len(h.Install) == 0 {
			continue
		}
		arg := ToolBuildArg(h.Name)
		fmt.Fprintf(&b, "ARG %s=0\n", arg)
		for _, step := range h.Install {
			fmt.Fprintf(&b, "RUN echo \"Installing %s (${%s})\" && %s\n", h.Name, arg, step)
		}
	}
	return b.String()
}

// RenderDockerfileMountDirs renders a single RUN directive that creates and
// chowns every harness config directory so a newly mounted (empty) named
// volume never surfaces as root:root. Derives the set directly from the
// harness registry mount targets, keeping this in sync with harnesses.yaml.
func RenderDockerfileMountDirs() string {
	seen := map[string]bool{}
	var dirs []string
	for _, h := range registry {
		for _, m := range h.Mounts {
			if m.Container == "" || seen[m.Container] {
				continue
			}
			seen[m.Container] = true
			dirs = append(dirs, m.Container)
		}
	}
	sort.Strings(dirs)
	return "RUN mkdir -p " + strings.Join(dirs, " ") + " && chown -R node:node " + strings.Join(dirs, " ")
}

// Registry returns all supported harnesses in declaration order.
func Registry() []Harness {
	r := make([]Harness, len(registry))
	copy(r, registry)
	return r
}

// ByName returns the harness with the given name.
func ByName(name string) (Harness, bool) {
	for _, h := range registry {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// EnvVars returns the deduplicated union of every harness's env vars.
func EnvVars() []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range registry {
		for _, k := range h.EnvVars {
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// claudeConfigKeys are the top-level .claude.json keys treated as host-owned
// config (merged into the volume without touching session state).
var claudeConfigKeys = []string{
	"mcpServers",
	"permissions",
	"hooks",
	"model",
	"settings",
	"env",
}

// MergeClaudeJSON overlays host-owned config keys into existing .claude.json
// volume state, preserving projects/history and any other runtime keys.
func MergeClaudeJSON(dst, src []byte) ([]byte, error) {
	if len(dst) == 0 || string(dst) == "null" {
		return src, nil
	}
	dstMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(dst, &dstMap); err != nil {
		return src, nil
	}
	srcMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(src, &srcMap); err != nil {
		return dst, nil
	}
	for _, k := range claudeConfigKeys {
		if v, ok := srcMap[k]; ok {
			dstMap[k] = v
		}
	}
	return json.MarshalIndent(dstMap, "", "  ")
}

// harnessesFile mirrors the YAML schema in harnesses.yaml. Merge is not part of
// the schema; harness-specific merge logic lives in Go and is wired up by name.
type harnessesFile struct {
	Harnesses []harnessSpec `yaml:"harnesses"`
}

type harnessSpec struct {
	Name    string      `yaml:"name"`
	EnvVars []string    `yaml:"env_vars"`
	Install []string    `yaml:"install"`
	Mounts  []mountSpec `yaml:"mounts"`
}

type mountSpec struct {
	HostRel    string `yaml:"host_rel"`
	Container  string `yaml:"container"`
	Volume     string `yaml:"volume"`
	VolumePath string `yaml:"volume_path"`
	Kind       string `yaml:"kind"`
}

// mergeFuncs maps harness name -> merge logic that cannot be expressed in YAML.
var mergeFuncs = map[string]MergeFunc{
	"claude": MergeClaudeJSON,
}

// registry holds all supported harnesses in declaration order.
var registry = loadRegistry()

func loadRegistry() []Harness {
	data, err := harnessDefsFS.ReadFile("harnesses.yaml")
	if err != nil {
		panic(fmt.Sprintf("read embedded harnesses.yaml: %v", err))
	}
	var file harnessesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		panic(fmt.Sprintf("parse harnesses.yaml: %v", err))
	}

	out := make([]Harness, 0, len(file.Harnesses))
	for _, spec := range file.Harnesses {
		h := Harness{
			Name:    spec.Name,
			EnvVars: spec.EnvVars,
			Install: spec.Install,
			Merge:   mergeFuncs[spec.Name],
		}
		for _, m := range spec.Mounts {
			kind := KindDir
			if m.Kind == "file" {
				kind = KindFile
			}
			h.Mounts = append(h.Mounts, Mount{
				HostRel:    m.HostRel,
				Container:  m.Container,
				Volume:     m.Volume,
				VolumePath: m.VolumePath,
				Kind:       kind,
			})
		}
		out = append(out, h)
	}
	return out
}
