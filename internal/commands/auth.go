package commands

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"claudex/internal/containers"
	"claudex/internal/dockerx"
)

const googleDocsAuthPort = "8810"

type authStartResponse struct {
	AuthorizationURL string   `json:"authorization_url"`
	State            string   `json:"state"`
	RedirectURI      string   `json:"redirect_uri"`
	Scopes           []string `json:"scopes"`
}

type authStatusResponse struct {
	Authenticated bool   `json:"authenticated"`
	TokenFile     string `json:"token_file"`
}

// Auth runs `claudex auth <service>` workflows.
func Auth(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: claudex auth <service> [--container <name>]")
	}

	service := args[0]
	if service != "google-docs-mcp" {
		return fmt.Errorf("unknown auth target %q", service)
	}

	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	container := fs.String("container", "", "Name of an existing Claudex container (omit to pick interactively)")
	keep := fs.Bool("keep-server", false, "Leave the MCP server running after auth")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	dx := &dockerx.CLI{}
	targetContainer := *container
	if targetContainer == "" {
		name, err := promptForContainer(dx)
		if err != nil {
			return err
		}
		targetContainer = name
	}
	if _, err := dx.Inspect(targetContainer); err != nil {
		return fmt.Errorf("container %q not found: %w", targetContainer, err)
	}

	fmt.Printf("Starting google-docs-mcp inside container %s...\n", targetContainer)
	if err := restartServer(dx, targetContainer); err != nil {
		return err
	}
	defer func() {
		if !*keep {
			_ = stopServer(dx, targetContainer)
		}
	}()

	if err := waitForServer(dx, targetContainer); err != nil {
		return err
	}

	startResp, err := requestAuthStart(dx, targetContainer)
	if err != nil {
		return err
	}

	fmt.Println("✅ Authorization link generated.")
	fmt.Println()
	fmt.Println("1. Open the URL below in your browser and complete the Google consent:")
	fmt.Println(startResp.AuthorizationURL)
	fmt.Println()
	fmt.Println("2. After Google redirects you back to http://localhost:8810/... you'll see an error.")
	fmt.Println("   Copy the entire redirected URL (including ?state=...&code=...) and paste it here.")
	fmt.Print("Paste redirected URL: ")

	reader := bufio.NewReader(os.Stdin)
	callbackURL, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read callback URL: %w", err)
	}
	callbackURL = strings.TrimSpace(callbackURL)
	if callbackURL == "" {
		return errors.New("no callback URL provided")
	}
	if _, err := url.Parse(callbackURL); err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}

	if err := replayCallback(dx, targetContainer, callbackURL); err != nil {
		return err
	}

	status, err := requestAuthStatus(dx, targetContainer)
	if err != nil {
		return err
	}
	if !status.Authenticated {
		return errors.New("callback completed but credentials were not persisted; check logs")
	}

	fmt.Println("🎉 Google Docs credentials stored at", status.TokenFile)
	if *keep {
		fmt.Println("The google-docs-mcp server is still running inside the container.")
	} else {
		fmt.Println("Stopped the temporary google-docs-mcp server.")
	}
	return nil
}

func restartServer(dx dockerx.Docker, container string) error {
	_ = dx.Exec(container, "pkill", "-f", "google-docs-mcp")
	cmd := fmt.Sprintf("nohup google-docs-mcp >/tmp/google-docs-mcp-auth.log 2>&1 &")
	return dx.Exec(container, "bash", "-lc", cmd)
}

func stopServer(dx dockerx.Docker, container string) error {
	return dx.Exec(container, "pkill", "-f", "google-docs-mcp")
}

func waitForServer(dx dockerx.Docker, container string) error {
	for i := 0; i < 30; i++ {
		if _, err := dx.ExecOutput(container, []string{"curl", "-s", fmt.Sprintf("http://localhost:%s/health", googleDocsAuthPort)}); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("google-docs-mcp server did not become ready; check container logs")
}

func requestAuthStart(dx dockerx.Docker, container string) (*authStartResponse, error) {
	out, err := dx.ExecOutput(container, []string{"curl", "-s", "-X", "POST", fmt.Sprintf("http://localhost:%s/auth/start", googleDocsAuthPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to call /auth/start: %w", err)
	}
	var resp authStartResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("unable to parse /auth/start response: %w", err)
	}
	if resp.AuthorizationURL == "" {
		return nil, errors.New("auth/start did not return an authorization_url")
	}
	return &resp, nil
}

func replayCallback(dx dockerx.Docker, container, callback string) error {
	_, err := dx.ExecOutput(container, []string{"curl", "-s", callback})
	if err != nil {
		return fmt.Errorf("failed to replay callback: %w", err)
	}
	return nil
}

func requestAuthStatus(dx dockerx.Docker, container string) (*authStatusResponse, error) {
	out, err := dx.ExecOutput(container, []string{"curl", "-s", fmt.Sprintf("http://localhost:%s/auth/status", googleDocsAuthPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to call /auth/status: %w", err)
	}
	var resp authStatusResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("unable to parse /auth/status: %w", err)
	}
	return &resp, nil
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func promptForContainer(dx dockerx.Docker) (string, error) {
	cons, err := containers.List(dx, true)
	if err != nil {
		return "", fmt.Errorf("failed to list Claudex containers: %w", err)
	}
	if len(cons) == 0 {
		return "", errors.New("no Claudex containers found; start one first")
	}
	fmt.Println("Select a Claudex container:")
	for idx, c := range cons {
		fmt.Printf("  %d) %s [%s]\n", idx+1, c.Name, c.Status)
	}
	fmt.Print("Container number (default 1): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return cons[0].Name, nil
	}
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(cons) {
		return "", fmt.Errorf("invalid selection %q", input)
	}
	return cons[choice-1].Name, nil
}
