package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claudex/internal/buildctx"
	"claudex/internal/containers"
	"claudex/internal/dockerx"
	"claudex/internal/ui"
)

var ErrNotImplemented = fmt.Errorf("not yet implemented: refactor in progress")

func Build(args []string) error {
	fmt.Println("Preparing build context...")
	ctxDir, cleanup, err := buildctx.PrepareBuildContext()
	if err != nil {
		return err
	}
	defer cleanup()
	dx := &dockerx.CLI{}
	// Optional --no-cache flag
	noCache := false
	for _, a := range args {
		if a == "--no-cache" {
			noCache = true
		}
	}
	if noCache {
		fmt.Println("Building image 'claudex' with --no-cache...")
	} else {
		fmt.Println("Building image 'claudex'...")
	}
	if err := dx.Build("claudex", ctxDir, noCache); err != nil {
		return err
	}
	fmt.Println("✅ Build complete: claudex")
	return nil
}

// List implements `claudex list` with filters and formats.
func List(args []string) error {
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

	dx := &dockerx.CLI{}
	includeStopped := show != "running"
	cons, err := containers.List(dx, includeStopped)
	if err != nil {
		return err
	}
	if show == "stopped" {
		var tmp []dockerx.Container
		for _, c := range cons {
			if c.Status != "running" {
				tmp = append(tmp, c)
			}
		}
		cons = tmp
	}

	var outList []dockerx.Container
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
			m, _ := containers.MountsFromLabel(&c)
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
			m, _ := containers.MountsFromLabel(&c)
			created := c.CreatedAt.Format("2006-01-02 15:04:05")
			fmt.Printf("%-32s %-10s %-20s %-10s %-8d %-16s %-10s\n", c.Name, c.Status, created, c.Labels["com.claudex.signature"], len(m), c.Labels["com.claudex.slug"], c.Image)
		}
		return nil
	}
}

// Destroy removes claudex containers with safety prompt.
func Destroy(args []string) error {
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

	dx := &dockerx.CLI{}
	cons, err := containers.List(dx, true)
	if err != nil {
		return err
	}
	// Build candidate pool by status
	var pool []dockerx.Container
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
	var victims []dockerx.Container
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
		if err := dx.Remove(v.Name, true); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", v.Name, err)
		}
	}
	return nil
}

// Push copies local files/dirs into /workspace of a running container.
func Push(args []string) error {
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

	dx := &dockerx.CLI{}
	target, err := pickRunning(dx, nameFlag)
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
		dest := fmt.Sprintf("%s:/workspace/", target)
		fmt.Printf("Pushing %s -> %s\n", abs, dest)
		if err := dx.CP(abs, dest); err != nil {
			return fmt.Errorf("docker cp failed for %s: %w", abs, err)
		}
	}
	return nil
}

// Pull copies from container to local destination. If no path provided, runs interactive selection.
// Usage: claudex pull [--name <NAME>] <container_path> [dest_dir (default /tmp)]
func Pull(args []string) error {
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

	dx := &dockerx.CLI{}
	target, err := pickRunning(dx, nameFlag)
	if err != nil {
		return err
	}

	if len(rest) == 0 {
		// interactive
		entries, err := ui.ListWorkspaceEntries(dx, target)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("no files available under /workspace in container %s", target)
		}
		reader := bufio.NewReader(os.Stdin)
		selections, err := ui.PromptForWorkspaceSelection(reader, entries)
		if err != nil {
			return err
		}
		if len(selections) == 0 {
			fmt.Println("No selections made; aborting pull.")
			return nil
		}
		destDir, err := ui.PromptForDestination(reader)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("cannot ensure destination %s: %v", destDir, err)
		}
		for _, entry := range selections {
			src := fmt.Sprintf("%s:/workspace/%s", target, entry)
			fmt.Printf("Pulling %s -> %s\n", src, destDir)
			if err := dx.CP(src, destDir); err != nil {
				return fmt.Errorf("docker cp failed for %s: %w", entry, err)
			}
		}
		return nil
	}

	// direct mode
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
	if err := dx.CP(src, destDir); err != nil {
		return fmt.Errorf("docker cp failed: %w", err)
	}
	return nil
}

// pickRunning returns a running container name by explicit value or unique running instance.
func pickRunning(dx dockerx.Docker, name string) (string, error) {
	if name != "" {
		ok, running, _, err := containers.Exists(dx, name)
		if err != nil {
			return "", err
		}
		if !ok || !running {
			return "", fmt.Errorf("container %s is not running", name)
		}
		return name, nil
	}
	cons, err := containers.List(dx, false)
	if err != nil {
		return "", err
	}
	if len(cons) == 0 {
		return "", fmt.Errorf("no running claudex containers. Start one first.")
	}
	if len(cons) == 1 {
		return cons[0].Name, nil
	}
	// Interactive selection when TTY is available; otherwise, return error with choices
	if ui.StdinIsTTY() {
		fmt.Println("Select a target container:")
		for i, c := range cons {
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
		if line == "" || err != nil || idx < 1 || idx > len(cons) {
			// Fall back to non-interactive error message with choices
			var names []string
			for _, c := range cons {
				names = append(names, c.Name)
			}
			return "", fmt.Errorf("multiple running claudex containers. Specify --name. Choices: %s", strings.Join(names, ", "))
		}
		return cons[idx-1].Name, nil
	}
	var names []string
	for _, c := range cons {
		names = append(names, c.Name)
	}
	return "", fmt.Errorf("multiple running claudex containers. Specify --name. Choices: %s", strings.Join(names, ", "))
}
