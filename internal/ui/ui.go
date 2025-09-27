package ui

import (
    "bufio"
    "bytes"
    "fmt"
    "os"
    "strconv"
    "strings"

    "claudex/internal/dockerx"
)

func StdinIsTTY() bool {
    info, err := os.Stdin.Stat()
    if err != nil {
        return false
    }
    return info.Mode()&os.ModeCharDevice != 0
}

func PromptForWorkspaceSelection(reader *bufio.Reader, entries []string) ([]string, error) {
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
    // stable order by entry value
    sortStrings(selections)
    return selections, nil
}

func PromptForDestination(reader *bufio.Reader) (string, error) {
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

func PullIgnoreSet() map[string]bool {
    return map[string]bool{
        "AGENTS.md": true,
        "CLAUDE.md": true,
        "GEMINI.md": true,
    }
}

func ListWorkspaceEntries(dx dockerx.Docker, container string) ([]string, error) {
    out, err := dx.ExecOutput(container, []string{"ls", "-1A", "/workspace"})
    if err != nil {
        return nil, fmt.Errorf("list workspace entries: %w", err)
    }
    trimmed := bytes.TrimSpace(out)
    if len(trimmed) == 0 {
        return nil, nil
    }
    lines := strings.Split(string(trimmed), "\n")
    var entries []string
    ignores := PullIgnoreSet()
    for _, line := range lines {
        line = strings.TrimSpace(line)
        if line == "" || ignores[line] {
            continue
        }
        entries = append(entries, line)
    }
    sortStrings(entries)
    return entries, nil
}

func sortStrings(a []string) {
    if len(a) < 2 {
        return
    }
    // simple insertion sort to avoid extra imports
    for i := 1; i < len(a); i++ {
        j := i
        for j > 0 && a[j-1] > a[j] {
            a[j-1], a[j] = a[j], a[j-1]
            j--
        }
    }
}
