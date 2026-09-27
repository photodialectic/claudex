package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/photodialectic/claudex/internal/commands"
	"github.com/photodialectic/claudex/internal/dockerx"
	"github.com/photodialectic/claudex/internal/run"
	"github.com/photodialectic/claudex/internal/version"
)

// Execute is the primary CLI dispatcher used by cmd/claudex and the
// thin legacy wrapper in claudex/main.go. It routes top‑level
// subcommands and falls back to the default run workflow when no
// subcommand (or an unknown token) is provided.
func Execute(args []string) error {
	if len(args) == 0 {
		// Default behavior: start/run container with current directory mounts
		return run.Run(args, os.Stdin, os.Stdout, os.Stderr, &dockerx.CLI{})
	}
	switch args[0] {
	case "--version", "version":
		fmt.Println(version.Version)
		return nil
	case "build":
		return commands.Build(args[1:])
	case "push":
		return commands.Push(args[1:])
	case "pull":
		return commands.Pull(args[1:])
	case "list":
		return commands.List(args[1:])
	case "bash":
		return commands.Bash(args[1:])
	case "destroy":
		return commands.Destroy(args[1:])
	case "callback":
		return commands.Callback(args[1:])
	case "harness":
		return commands.Harness(args[1:])
	case "bundle":
		return commands.Bundle(args[1:])
	case "-h", "--help", "help":
		return usage()
	default:
		// Default: run the container workflow using remaining args
		return run.Run(args, os.Stdin, os.Stdout, os.Stderr, &dockerx.CLI{})
	}
}

func usage() error {
	prog := filepath.Base(os.Args[0])
	fmt.Printf(`Usage: %s [--host-network | --network <NAME>] [--name <NAME>] [--parallel] [--replace] [--strict-mounts] [DIR1 DIR2 ...]

Mounts each DIRi at /workspace/<basename(DIRi)> in the claudex container.
If no DIR is provided, mounts each file and directory in the current directory at /workspace/<name>.

Options:
  --host-network    Use host networking (allows OAuth callbacks)
  --network <NAME>  Attach to a specific Docker network (e.g. mono_default)
  --name <NAME>     Override derived container name
  --parallel        Always create a new container (suffix with timestamp)
  --replace         Replace the target container if it exists
  --strict-mounts   Error if existing container mounts differ
  --no-git          Skip initializing an empty Git repository in /workspace
  --version         Print the Claudex CLI version and exit

Examples:
  %s
  %s service1/ service2/
  %s --host-network
  %s --parallel app/ api/
  %s --replace app/ api/

Build the Docker image:
  %s build [--no-cache]

Refresh one or more tool image layers:
  %s harness update [--no-cache] [<NAME> ...]

Push/pull files with a container:
  %s push [--name <NAME>] <file_or_dir> [...]
  %s pull [--name <NAME>] <container_path> [dest_dir (default /tmp)]

List claudex containers:
  %s list [--all|--running|--stopped] [--format table|json|names] [--filter key=value]

Destroy claudex containers:
  %s destroy [--name <NAME> | --signature <HASH> | --all] [--running|--stopped] [--force|--prune-stopped]

Open an interactive bash shell in a running container:
  %s bash [<NAME>]

Replay an OAuth redirect callback inside a container:
  %s callback [--name <NAME>] <callback-url>

Manage harness config volumes:
  %s harness list
  %s harness push [<NAME> ...]
  %s harness pull [<NAME> ...]

Portable bundles:
  claudex bundle create <dest-dir> [--volumes <n1,n2>] [--jobs <1-16>] [--no-binaries] [--no-host-config] [--force]
  claudex bundle run <bundle> [--refresh] [--write-back] [DIR...] [run-flags]
  claudex bundle destroy <bundle> [--no-verify]
  claudex bundle install <bundle> [--replace] [--volumes <n1,n2>] [--no-verify] [--with-host-config]
  Bundle archives contain credentials and session history; keep them private.
`, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog)
	return nil
}
