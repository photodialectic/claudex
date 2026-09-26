package commands

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/photodialectic/claudex/internal/dockerx"
)

// Auth runs `claudex auth` workflows.
func Auth(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: claudex auth callback [--container <name>] <callback-url>")
	}
	switch args[0] {
	case "callback":
		return authCallback(args[1:])
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

// authCallback replays an OAuth redirect URL inside a running Claudex
// container. This is useful when an MCP server inside the container drives an
// OAuth flow and the browser callback (redirected to http://localhost:PORT/...)
// needs to be re-issued inside the container where that MCP server listens.
func authCallback(args []string) error {
	var containerFlag string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--container":
			if i+1 >= len(args) {
				return errors.New("--container requires a value")
			}
			containerFlag = args[i+1]
			i++
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return errors.New("usage: claudex auth callback [--container <name>] <callback-url>")
	}
	callback := rest[0]
	if _, err := url.Parse(callback); err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}

	dx := &dockerx.CLI{}
	target, err := pickRunning(dx, containerFlag)
	if err != nil {
		return err
	}

	fmt.Printf("Replaying callback inside container %s...\n", target)
	out, err := replayAndRead(dx, target, callback)
	if err != nil {
		return err
	}
	if len(out) > 0 {
		fmt.Println(strings.TrimSpace(string(out)))
	}
	fmt.Printf("✅ Callback replayed in container %s.\n", target)
	return nil
}

func replayAndRead(dx dockerx.Docker, container, callback string) ([]byte, error) {
	out, err := dx.ExecOutput(container, []string{"curl", "-s", callback})
	if err != nil {
		return nil, fmt.Errorf("failed to replay callback: %w", err)
	}
	return out, nil
}
