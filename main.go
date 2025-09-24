package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "build":
			if err := build(); err != nil {
				log.Fatalf("build failed: %v", err)
			}
			return
		case "push":
			if err := pushCommand(os.Args[2:]); err != nil {
				log.Fatalf("push failed: %v", err)
			}
			return
		case "pull":
			if err := pullCommand(os.Args[2:]); err != nil {
				log.Fatalf("pull failed: %v", err)
			}
			return
		case "list":
			if err := listCommand(os.Args[2:]); err != nil {
				log.Fatalf("list failed: %v", err)
			}
			return
		case "destroy":
			if err := destroyCommand(os.Args[2:]); err != nil {
				log.Fatalf("destroy failed: %v", err)
			}
			return
		case "include":
			if err := includeCommand(os.Args[2:]); err != nil {
				log.Fatalf("include failed: %v", err)
			}
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}
	if err := runCli(os.Args[1:]); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func usage() {
	prog := filepath.Base(os.Args[0])
	fmt.Printf(`Usage: %s [--host-network] [--name <NAME>] [--parallel] [--replace] [--strict-mounts] [DIR1 DIR2 ...]

Mounts each DIRi at /workspace/<basename(DIRi)> in the claudex container.
If no DIR is provided, mounts each file and directory in the current directory at /workspace/<name>.

Options:
  --host-network    Use host networking (allows OAuth callbacks)
  --name <NAME>     Override derived container name
  --parallel        Always create a new container (suffix with timestamp)
  --replace         Replace the target container if it exists
  --strict-mounts   Error if existing container mounts differ

Examples:
  %s
  %s service1/ service2/
  %s --host-network
  %s --parallel app/ api/
  %s --replace app/ api/

Build or updates the Docker image:
  %s build

Push/pull files with a container:
  %s push [--name <NAME>] <file_or_dir> [...]
  %s pull [--name <NAME>] <container_path> [dest_dir (default /tmp)]

Include files/directories in a running container:
  %s include [--name <NAME> | --auto] <file_or_directory> [...] (deprecated; use push)

List claudex containers:
  %s list [--all|--running|--stopped] [--format table|json|names] [--filter key=value]

Destroy claudex containers:
  %s destroy [--name <NAME> | --signature <HASH> | --all] [--running|--stopped] [--force|--prune-stopped]
`, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog)
	os.Exit(0)
}

// includeCommand copies a file or directory to the /context directory in the claudex container.
func includeCommand(args []string) error {
	// Deprecated
	fmt.Fprintln(os.Stderr, "Note: 'include' is deprecated; use 'claudex push' instead.")
	// parse flags: --name, --auto
	var name string
	var auto bool
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			name = args[i+1]
			i++
		case "--auto":
			auto = true
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("usage: claudex include [--name <NAME> | --auto] <file_or_directory> [more...]")
	}

	// Determine target container (early-return style)
	var target string
	if name != "" {
		target = name
	}
	if target == "" && auto {
		// compute signature from current directory
		norm, err := normalizeDirs([]string{"."})
		if err != nil {
			return err
		}
		sig := deriveSignature(norm)
		cons, err := getClaudexContainers(false)
		if err != nil {
			return err
		}
		var newest containerInfo
		found := false
		for _, c := range cons {
			if c.Labels["com.claudex.signature"] != sig {
				continue
			}
			if !found || c.CreatedAt.After(newest.CreatedAt) {
				newest = c
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no running claudex container found for current workspace")
		}
		target = newest.Name
	}
	if target == "" {
		cons, err := getClaudexContainers(false)
		if err != nil {
			return err
		}
		if len(cons) == 1 {
			target = cons[0].Name
		}
		if target == "" && len(cons) == 0 {
			return fmt.Errorf("no running claudex containers. Start one first.")
		}
		if target == "" {
			var names []string
			for _, c := range cons {
				names = append(names, c.Name)
			}
			return fmt.Errorf("multiple running claudex containers. Specify --name. Choices: %s", strings.Join(names, ", "))
		}
	}

	// Ensure container is running
	if ok, running, _, _ := containerExists(target); !ok || !running {
		return fmt.Errorf("container %s is not running", target)
	}

	// Process each argument
	for _, source := range paths {
		abs, err := filepath.Abs(source)
		if err != nil {
			return fmt.Errorf("invalid path: %s", source)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("'%s' does not exist", abs)
		}
		basename := filepath.Base(abs)
		destPath := fmt.Sprintf("%s:/context/%s", target, basename)
		fmt.Printf("Including %s at /context/%s...\n", abs, basename)
		cpCmd := exec.Command("docker", "cp", abs, destPath)
		cpCmd.Stdout = os.Stdout
		cpCmd.Stderr = os.Stderr
		if err := cpCmd.Run(); err != nil {
			return fmt.Errorf("docker cp failed for %s: %w", abs, err)
		}
		fmt.Printf("Successfully included %s at /context/%s\n", abs, basename)
	}
	return nil
}

// ---- Derived naming and container helpers ----

const claudexVersion = "0.1.0"

type containerInfo struct {
	ID        string
	Name      string
	Image     string
	Status    string
	CreatedAt time.Time
	Labels    map[string]string
}

// defaultDirs returns ["."] if input is empty, otherwise returns input.
func defaultDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return []string{"."}
	}
	return dirs
}

func normalizeDirs(dirs []string) ([]string, error) {
	var res []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %s", d)
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("'%s' is not a directory", abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve symlinks for %s: %w", abs, err)
		}
		res = append(res, real)
	}
	sort.Strings(res)
	return res, nil
}

func deriveSignature(norm []string) string {
	salt := os.Getenv("CLAUDEX_NAME_SALT")
	h := sha256.New()
	for _, p := range norm {
		v := p
		if salt != "" {
			v = salt + "|" + p
		}
		h.Write([]byte(v))
		h.Write([]byte("\n"))
	}
	sum := fmt.Sprintf("%x", h.Sum(nil))
	if len(sum) > 8 {
		return sum[:8]
	}
	return sum
}

func toKebab(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	var b strings.Builder
	prev := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = false
			continue
		}
		if !prev {
			b.WriteByte('-')
			prev = true
		}
	}
	out := b.String()
	out = strings.Trim(out, "-")
	dashSeq := regexp.MustCompile(`-+`)
	out = dashSeq.ReplaceAllString(out, "-")
	if out == "" {
		out = "ws"
	}
	return out
}

func deriveSlug(norm []string) string {
	parts := []string{}
	for _, p := range norm {
		parts = append(parts, toKebab(filepath.Base(p)))
		if len(parts) == 2 {
			break
		}
	}
	slug := strings.Join(parts, "-")
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-")
	}
	if slug == "" {
		slug = "ws"
	}
	return slug
}

func deriveName(slug, sig string) string {
	prefix := os.Getenv("CLAUDEX_NAME_PREFIX")
	if prefix == "" {
		prefix = "claudex"
	}
	return fmt.Sprintf("%s-%s-%s", prefix, slug, sig)
}

func dockerOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	return cmd.CombinedOutput()
}

// pickRunningContainer selects a running container by explicit name or prompts when multiple.
func pickRunningContainer(name string) (string, error) {
	if name != "" {
		exists, running, _, err := containerExists(name)
		if err != nil {
			return "", err
		}
		if !exists || !running {
			return "", fmt.Errorf("container %s is not running", name)
		}
		return name, nil
	}

	containers, err := getClaudexContainers(false)
	if err != nil {
		return "", err
	}
	count := len(containers)
	if count == 0 {
		return "", fmt.Errorf("no running claudex containers. Start one first")
	}
	if count == 1 {
		return containers[0].Name, nil
	}

	fmt.Println("Select a target container:")
	for i, c := range containers {
		sig := c.Labels["com.claudex.signature"]
		slug := c.Labels["com.claudex.slug"]
		created := c.CreatedAt.Format("2006-01-02 15:04:05")
		fmt.Printf("  [%d] %s  (%s  %s  %s)\n", i+1, c.Name, c.Status, created, slug+":"+sig)
	}
	fmt.Print("Enter number: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	idx, err := strconv.Atoi(line)
	if err != nil {
		return "", fmt.Errorf("invalid selection: %v", err)
	}
	if idx < 1 || idx > count {
		return "", fmt.Errorf("selection out of range")
	}
	return containers[idx-1].Name, nil
}

// warnOrErrorOnMountMismatch validates mounts, returning error if strict or warning otherwise.
func warnOrErrorOnMountMismatch(info *containerInfo, normDirs []string, strict bool, name string) error {
	if info == nil {
		return nil
	}
	labeled, err := mountsFromLabel(info)
	if err != nil {
		return nil
	}
	same := compareStringSlices(labeled, normDirs)
	if same {
		return nil
	}
	if strict {
		return fmt.Errorf("mount mismatch for %s. Use --replace or --parallel", name)
	}
	fmt.Fprintf(os.Stderr, "Warning: mount mismatch with container %s. Attaching anyway.\n", name)
	return nil
}

func containerExists(name string) (exists bool, running bool, info *containerInfo, err error) {
	out, err := dockerOutput("inspect", name, "--format", "{{json .}}")
	if err != nil {
		// Distinguish not-found from other errors
		if bytes.Contains(out, []byte("No such object")) || bytes.Contains(out, []byte("Error: No such object")) {
			return false, false, nil, nil
		}
		return false, false, nil, fmt.Errorf("docker inspect failed for %s: %s", name, strings.TrimSpace(string(out)))
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return true, false, nil, fmt.Errorf("failed to parse docker inspect for %s: %v", name, err)
	}
	state := ""
	if m, ok := raw["State"].(map[string]any); ok {
		if r, ok := m["Running"].(bool); ok && r {
			state = "running"
		}
		if state == "" {
			if st, ok := m["Status"].(string); ok {
				state = st
			}
		}
	}
	createdAt := time.Now()
	if cstr, ok := raw["Created"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, cstr); err == nil {
			createdAt = t
		}
	}
	labels := map[string]string{}
	if m, ok := raw["Config"].(map[string]any); ok {
		if lm, ok := m["Labels"].(map[string]any); ok {
			for k, v := range lm {
				if s, ok := v.(string); ok {
					labels[k] = s
				}
			}
		}
	}
	image := ""
	if c, ok := raw["Config"].(map[string]any); ok {
		if s, ok := c["Image"].(string); ok {
			image = s
		}
	}
	id := ""
	if s, ok := raw["Id"].(string); ok {
		id = s
	}
	info = &containerInfo{ID: id, Name: name, Image: image, Status: state, CreatedAt: createdAt, Labels: labels}
	return true, state == "running", info, nil
}

func getClaudexContainers(includeStopped bool) ([]containerInfo, error) {
	args := []string{"ps", "--format", "{{.Names}}", "-f", "label=com.claudex.signature"}
	if includeStopped {
		args = append(args, "-a")
	}
	out, err := dockerOutput(args...)
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %v: %s", err, string(out))
	}
	lines := strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' || r == '\r' })
	var res []containerInfo
	for _, n := range lines {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if ok, running, info, _ := containerExists(n); ok {
			if !includeStopped && !running {
				continue
			}
			if info != nil {
				res = append(res, *info)
			}
		}
	}
	return res, nil
}

func mountsFromLabel(info *containerInfo) ([]string, error) {
	s := info.Labels["com.claudex.mounts"]
	if s == "" {
		return nil, errors.New("mount label missing")
	}
	var m []string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func compareStringSlices(a, b []string) bool {
	return slices.Equal(a, b)
}

//go:embed Dockerfile init-firewall.sh CLAUDEX.md
var dockerContextFS embed.FS

// prepareBuildContext writes embedded Dockerfile and init-firewall.sh to a temp directory.
func prepareBuildContext() (string, error) {
	tmpDir, err := os.MkdirTemp("", "claudex-build-")
	if err != nil {
		return "", fmt.Errorf("cannot create temp build dir: %w", err)
	}
	files := []string{"Dockerfile", "init-firewall.sh", "CLAUDEX.md"}
	for _, name := range files {
		data, err := dockerContextFS.ReadFile(name)
		if err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("cannot read embedded %s: %w", name, err)
		}
		outPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("cannot write %s to temp dir: %w", name, err)
		}
	}
	return tmpDir, nil
}

// build or updates the claudex Docker image.
func build() error {
	fmt.Println("Building/updating the claudex container image...")
	ctxDir, err := prepareBuildContext()
	if err != nil {
		return err
	}
	defer os.RemoveAll(ctxDir)
	cmd := exec.Command("docker", "build", "--no-cache", "-t", "claudex", ctxDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// ensureImage checks if the 'claudex' image exists; builds it if missing.
func ensureImage() error {
	out, err := exec.Command("docker", "images", "-q", "claudex").Output()
	if err != nil {
		return fmt.Errorf("docker images check failed: %w", err)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return nil
	}
	fmt.Println("Building 'claudex' container image...")
	ctxDir, err := prepareBuildContext()
	if err != nil {
		return err
	}
	defer os.RemoveAll(ctxDir)
	cmd := exec.Command("docker", "build", "-t", "claudex", ctxDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// runCli handles the container setup and shell attachment.
func runCli(args []string) error {
	// Flags
	var useHostNetwork bool
	var nameOverride string
	var forceReplace bool
	var alwaysParallel bool
	var strictMounts bool

	var workdirs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--host-network":
			useHostNetwork = true
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			nameOverride = args[i+1]
			i++
		case "--replace":
			forceReplace = true
		case "--parallel":
			alwaysParallel = true
		case "--strict-mounts":
			strictMounts = true
		default:
			workdirs = append(workdirs, a)
		}
	}

	// Normalize workdirs
	normDirs, err := normalizeDirs(defaultDirs(workdirs))
	if err != nil {
		return err
	}

	// Compute derived name
	sig := deriveSignature(normDirs)
	slug := deriveSlug(normDirs)
	name := deriveName(slug, sig)
	if nameOverride != "" {
		name = nameOverride
	}
	if alwaysParallel {
		name = fmt.Sprintf("%s-%d", name, time.Now().Unix())
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	var mounts []string
	// Mount Claude credentials if available
	claudeJson := filepath.Join(home, ".claude.json")
	_, cjErr := os.Stat(claudeJson)
	if cjErr == nil {
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/node/.claude.json", claudeJson))
	}
	if cjErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s not found; proceeding without it.\n", claudeJson)
	}

	// mount Claude, Codex, and Gemini config directories
	for _, dir := range []string{"claude", "codex", "gemini"} {
		configDir := filepath.Join(home, "."+dir)
		fi, err := os.Stat(configDir)
		if err == nil && fi.IsDir() {
			mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/node/.%s", configDir, dir))
		}
		if err != nil || (fi != nil && !fi.IsDir()) {
			fmt.Fprintf(os.Stderr, "Warning: %s not found or not a directory; proceeding without it.\n", configDir)
		}
	}

	// Mount workspace directories from normalized list
	for _, abs := range normDirs {
		base := filepath.Base(abs)
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/workspace/%s", abs, base))
	}

	// Ensure the 'claudex' image exists; build if missing
	if err := ensureImage(); err != nil {
		return err
	}

	// Reuse-or-create flow
	exists, running, info, err := containerExists(name)
	if err != nil {
		return err
	}
	if exists && !forceReplace {
		if err := warnOrErrorOnMountMismatch(info, normDirs, strictMounts, name); err != nil {
			return err
		}
		if !running {
			fmt.Printf("Starting container %s...\n", name)
			if err := exec.Command("docker", "start", name).Run(); err != nil {
				return fmt.Errorf("failed to start container: %w", err)
			}
		}
		cmdShell := exec.Command("docker", "exec", "-it", name, "bash")
		cmdShell.Stdin = os.Stdin
		cmdShell.Stdout = os.Stdout
		cmdShell.Stderr = os.Stderr
		return cmdShell.Run()
	}
	if exists && forceReplace {
		fmt.Printf("Replacing existing container %s...\n", name)
		exec.Command("docker", "rm", "-f", name).Run()
	}

	// Run the container in detached mode
	runArgs := []string{"run", "--name", name, "-d", "-e", "OPENAI_API_KEY", "-e", "AI_API_MK", "-e", "GEMINI_API_KEY", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW"}
	// add docker sock mount
	_, sockErr := os.Stat("/var/run/docker.sock")
	if sockErr == nil {
		runArgs = append(runArgs, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}
	if sockErr != nil {
		fmt.Fprintln(os.Stderr, "Warning: /var/run/docker.sock not found; Docker commands inside the container will not work. If you're on macOS with Docker Desktop, you may need to symlink the CLI socket, e.g.:\n  sudo ln -s ~/Library/Containers/com.docker.docker/Data/docker-cli.sock /var/run/docker.sock\n")
	}

	// Add host networking if requested
	if useHostNetwork {
		runArgs = append(runArgs, "--network=host")
	}

	// Labels
	labels := map[string]string{
		"com.claudex.signature":  sig,
		"com.claudex.slug":       slug,
		"com.claudex.version":    claudexVersion,
		"com.claudex.created_at": time.Now().UTC().Format(time.RFC3339),
	}
	if b, err := json.Marshal(normDirs); err == nil {
		labels["com.claudex.mounts"] = string(b)
	}
	for k, v := range labels {
		runArgs = append(runArgs, "-l", fmt.Sprintf("%s=%s", k, v))
	}

	runArgs = append(runArgs, mounts...)
	runArgs = append(runArgs, "claudex", "sleep", "infinity")
	cmdRun := exec.Command("docker", runArgs...)
	cmdRun.Stdout = os.Stdout
	cmdRun.Stderr = os.Stderr
	if err := cmdRun.Run(); err != nil {
		return fmt.Errorf("docker run failed: %w", err)
	}

	// Initialize Git repository if not present, using 'main' as the initial branch
	gitCmd := "if [ ! -d /workspace/.git ]; then git -C /workspace init -b main && git -C /workspace config user.name 'Claudex CLI' && git -C /workspace config user.email 'claudex@local' && git -C /workspace add . && git -C /workspace commit -m 'Initial workspace commit'; fi"
	cmdGit := exec.Command("docker", "exec", "-u", "node", name, "bash", "-lc", gitCmd)
	cmdGit.Stdout = os.Stdout
	cmdGit.Stderr = os.Stderr
	if err := cmdGit.Run(); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}

	// Initialize the firewall inside the container (skip with host networking)
	if !useHostNetwork {
		cmdFirewall := exec.Command("docker", "exec", name, "bash", "-c", "sudo /usr/local/bin/init-firewall.sh")
		cmdFirewall.Stdout = os.Stdout
		cmdFirewall.Stderr = os.Stderr
		if err := cmdFirewall.Run(); err != nil {
			return fmt.Errorf("init-firewall failed: %w", err)
		}
	}

	// Attach an interactive shell
	cmdShell := exec.Command("docker", "exec", "-it", name, "bash")
	cmdShell.Stdin = os.Stdin
	cmdShell.Stdout = os.Stdout
	cmdShell.Stderr = os.Stderr
	return cmdShell.Run()
}

// listCommand shows claudex-managed containers with filters and formats.
func listCommand(args []string) error {
	show := "running"
	format := "table"
	filters := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--all":
			show = "all"
		case "--running":
			show = "running"
		case "--stopped":
			show = "stopped"
		case "--format":
			if i+1 >= len(args) {
				return fmt.Errorf("--format requires a value")
			}
			format = args[i+1]
			i++
		case "--filter":
			if i+1 >= len(args) {
				return fmt.Errorf("--filter requires key=value")
			}
			kv := args[i+1]
			i++
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --filter %q", kv)
			}
			filters[parts[0]] = parts[1]
		default:
			return fmt.Errorf("unknown arg: %s", a)
		}
	}

	includeStopped := show != "running"
	cons, err := getClaudexContainers(includeStopped)
	if err != nil {
		return err
	}

	if show == "stopped" {
		var tmp []containerInfo
		for _, c := range cons {
			if c.Status != "running" {
				tmp = append(tmp, c)
			}
		}
		cons = tmp
	}

	var outList []containerInfo
	for _, c := range cons {
		if v, ok := filters["name"]; ok {
			if v == "" {
				continue
			}
			okm, err := filepath.Match(v, c.Name)
			if err != nil {
				return fmt.Errorf("invalid --filter name pattern %q: %v", v, err)
			}
			if !okm {
				continue
			}
		}
		if v, ok := filters["signature"]; ok && c.Labels["com.claudex.signature"] != v {
			continue
		}
		if v, ok := filters["slug"]; ok {
			if v == "" {
				continue
			}
			okm, err := filepath.Match(v, c.Labels["com.claudex.slug"])
			if err != nil {
				return fmt.Errorf("invalid --filter slug pattern %q: %v", v, err)
			}
			if !okm {
				continue
			}
		}
		outList = append(outList, c)
	}

	switch format {
	case "json":
		type outItem struct {
			Name      string            `json:"name"`
			Status    string            `json:"status"`
			Created   time.Time         `json:"created"`
			Image     string            `json:"image"`
			Labels    map[string]string `json:"labels"`
			Mounts    []string          `json:"mounts"`
			Signature string            `json:"signature"`
			Slug      string            `json:"slug"`
		}
		var items []outItem
		for _, c := range outList {
			m, _ := mountsFromLabel(&c)
			items = append(items, outItem{Name: c.Name, Status: c.Status, Created: c.CreatedAt, Image: c.Image, Labels: c.Labels, Mounts: m, Signature: c.Labels["com.claudex.signature"], Slug: c.Labels["com.claudex.slug"]})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	case "names":
		for _, c := range outList {
			fmt.Println(c.Name)
		}
		return nil
	default:
		fmt.Printf("%-32s %-10s %-20s %-10s %-8s %-16s %-10s\n", "NAME", "STATUS", "CREATED", "SIGNATURE", "MOUNTS", "SLUG", "IMAGE")
		for _, c := range outList {
			m, _ := mountsFromLabel(&c)
			created := c.CreatedAt.Format("2006-01-02 15:04:05")
			fmt.Printf("%-32s %-10s %-20s %-10s %-8d %-16s %-10s\n", c.Name, c.Status, created, c.Labels["com.claudex.signature"], len(m), c.Labels["com.claudex.slug"], c.Image)
		}
		return nil
	}
}

// destroyCommand removes claudex containers with safety prompt.
func destroyCommand(args []string) error {
	var byName, bySig string
	var all bool
	var runningOnly, stoppedOnly bool
	var force bool
	var pruneStopped bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			byName = args[i+1]
			i++
		case "--signature":
			if i+1 >= len(args) {
				return fmt.Errorf("--signature requires a value")
			}
			bySig = args[i+1]
			i++
		case "--all":
			all = true
		case "--running":
			runningOnly = true
		case "--stopped":
			stoppedOnly = true
		case "--force":
			force = true
		case "--prune-stopped":
			pruneStopped = true
		default:
			return fmt.Errorf("unknown arg: %s", a)
		}
	}
	if pruneStopped {
		all = true
		runningOnly = false
		stoppedOnly = true
	}

	cons, err := getClaudexContainers(true)
	if err != nil {
		return err
	}
	// Build candidate pool by status
	var pool []containerInfo
	for _, c := range cons {
		if runningOnly && c.Status != "running" {
			continue
		}
		if stoppedOnly && c.Status == "running" {
			continue
		}
		pool = append(pool, c)
	}

	// Resolve victims from selectors or interactive choice
	var victims []containerInfo
	if all {
		victims = append(victims, pool...)
	}
	if len(victims) == 0 && (byName != "" || bySig != "") {
		for _, c := range pool {
			if byName != "" && c.Name != byName {
				continue
			}
			if bySig != "" && c.Labels["com.claudex.signature"] != bySig {
				continue
			}
			victims = append(victims, c)
		}
		if len(victims) == 0 {
			fmt.Println("No matching containers.")
			return nil
		}
	}
	if len(victims) == 0 {
		if len(pool) == 0 {
			fmt.Println("No claudex containers match the status filter.")
			return nil
		}
		fmt.Println("Select containers to destroy (comma-separated numbers):")
		for i, c := range pool {
			sig := c.Labels["com.claudex.signature"]
			slug := c.Labels["com.claudex.slug"]
			fmt.Printf("  [%d] %-32s %-10s %-8s %-16s\n", i+1, c.Name, c.Status, sig, slug)
		}
		fmt.Print("Enter selection (blank to abort): ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println("Aborted.")
			return nil
		}
		parts := strings.Split(line, ",")
		seen := map[int]bool{}
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 1 || idx > len(pool) {
				return fmt.Errorf("invalid selection '%s'", p)
			}
			if seen[idx] {
				continue
			}
			seen[idx] = true
			victims = append(victims, pool[idx-1])
		}
		if len(victims) == 0 {
			fmt.Println("No selection; aborted.")
			return nil
		}
	}

	if !force {
		fmt.Printf("About to remove %d container(s):\n", len(victims))
		fmt.Printf("%-32s %-10s %-10s %-16s\n", "NAME", "STATUS", "SIGNATURE", "SLUG")
		for _, v := range victims {
			fmt.Printf("%-32s %-10s %-10s %-16s\n", v.Name, v.Status, v.Labels["com.claudex.signature"], v.Labels["com.claudex.slug"])
		}
		fmt.Print("Proceed? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(ans)
		if !strings.EqualFold(ans, "y") && !strings.EqualFold(ans, "yes") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	for _, v := range victims {
		fmt.Printf("Removing %s...\n", v.Name)
		if err := exec.Command("docker", "rm", "-f", v.Name).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", v.Name, err)
		}
	}
	return nil
}

// pushCommand copies local files/dirs into /workspace of a running container.
func pushCommand(args []string) error {
	var nameFlag string
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			nameFlag = args[i+1]
			i++
		default:
			paths = append(paths, a)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("usage: claudex push [--name <NAME>] <file_or_dir> [...]")
	}

	target, err := pickRunningContainer(nameFlag)
	if err != nil {
		return err
	}

	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("invalid path: %s", p)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("'%s' does not exist", abs)
		}
		// Copy to /workspace
		dest := fmt.Sprintf("%s:/workspace/", target)
		fmt.Printf("Pushing %s -> %s\n", abs, dest)
		cmd := exec.Command("docker", "cp", abs, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("docker cp failed for %s: %w", abs, err)
		}
	}
	return nil
}

// pullCommand copies a file/dir from a container to a local destination directory.
// Usage: claudex pull [--name <NAME>] <container_path> [dest_dir (default /tmp)]
func pullCommand(args []string) error {
	var nameFlag string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			nameFlag = args[i+1]
			i++
		default:
			rest = append(rest, a)
		}
	}

	target, err := pickRunningContainer(nameFlag)
	if err != nil {
		return err
	}

	if len(rest) == 0 {
		return interactivePull(target)
	}

	containerPath := rest[0]
	destDir := "/tmp"
	if len(rest) >= 2 {
		destDir = rest[1]
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("cannot ensure destination %s: %v", destDir, err)
	}

	src := fmt.Sprintf("%s:%s", target, containerPath)
	fmt.Printf("Pulling %s -> %s\n", src, destDir)
	cmd := exec.Command("docker", "cp", src, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker cp failed: %w", err)
	}
	return nil
}

func interactivePull(container string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("stdin is not a TTY; specify a container path to pull")
	}

	entries, err := listWorkspaceEntries(container)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no files available under /workspace in container %s", container)
	}

	reader := bufio.NewReader(os.Stdin)
	selections, err := promptForWorkspaceSelection(reader, entries)
	if err != nil {
		return err
	}
	if len(selections) == 0 {
		fmt.Println("No selections made; aborting pull.")
		return nil
	}

	destDir, err := promptForDestination(reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("cannot ensure destination %s: %v", destDir, err)
	}

	for _, entry := range selections {
		src := fmt.Sprintf("%s:/workspace/%s", container, entry)
		fmt.Printf("Pulling %s -> %s\n", src, destDir)
		cmd := exec.Command("docker", "cp", src, destDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("docker cp failed for %s: %w", entry, err)
		}
	}
	return nil
}

func listWorkspaceEntries(container string) ([]string, error) {
	cmd := exec.Command("docker", "exec", container, "ls", "-1A", "/workspace")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list workspace entries: %w", err)
	}
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil, nil
	}
	lines := strings.Split(string(trimmed), "\n")
	var entries []string
	ignores := pullIgnoreSet()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if ignores[line] {
			continue
		}
		entries = append(entries, line)
	}
	sort.Strings(entries)
	return entries, nil
}

func pullIgnoreSet() map[string]bool {
	return map[string]bool{
		"AGENTS.md": true,
		"CLAUDE.md": true,
		"GEMINI.md": true,
	}
}

func promptForWorkspaceSelection(reader *bufio.Reader, entries []string) ([]string, error) {
	fmt.Println("Select files or directories to pull:")
	for i, entry := range entries {
		fmt.Printf("  %d) %s\n", i+1, entry)
	}
	fmt.Println("Enter numbers separated by commas or spaces (blank to cancel):")
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}
	replaced := strings.ReplaceAll(input, ",", " ")
	fields := strings.Fields(replaced)
	if len(fields) == 0 {
		return nil, nil
	}
	indexSet := make(map[int]struct{})
	for _, field := range fields {
		num, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid selection '%s'", field)
		}
		if num < 1 || num > len(entries) {
			return nil, fmt.Errorf("selection %d out of range", num)
		}
		indexSet[num-1] = struct{}{}
	}
	var selections []string
	for idx := range indexSet {
		selections = append(selections, entries[idx])
	}
	slices.Sort(selections)
	return selections, nil
}

func promptForDestination(reader *bufio.Reader) (string, error) {
	const defaultDest = "/tmp"
	fmt.Printf("Destination directory (default %s): ", defaultDest)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultDest, nil
	}
	return input, nil
}

func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
