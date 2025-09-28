package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"claudex/internal/commands"
	"claudex/internal/dockerx"
	"claudex/internal/run"
	"claudex/internal/version"
)

// Execute is the entrypoint for the refactored CLI.
// Currently a minimal dispatcher that routes subcommands to stubs while
// the refactor is in progress. This entrypoint is not used by the current
// Makefile build which still targets the legacy main.
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
	case "destroy":
		return commands.Destroy(args[1:])
	case "-h", "--help", "help":
		return usage()
	default:
		// Default: run the container workflow using remaining args
		return run.Run(args, os.Stdin, os.Stdout, os.Stderr, &dockerx.CLI{})
	}
}

func usage() error {
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

Build or update the Docker image:
  %s build [--no-cache]

Push/pull files with a container:
  %s push [--name <NAME>] <file_or_dir> [...]
  %s pull [--name <NAME>] <container_path> [dest_dir (default /tmp)]

List claudex containers:
  %s list [--all|--running|--stopped] [--format table|json|names] [--filter key=value]

Destroy claudex containers:
  %s destroy [--name <NAME> | --signature <HASH> | --all] [--running|--stopped] [--force|--prune-stopped]
`, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog, prog)
	return nil
}
