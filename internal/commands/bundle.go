package commands

import (
	"fmt"

	"github.com/photodialectic/claudex/internal/dockerx"
)

// Bundle dispatches portable bundle operations.
func Bundle(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(`Usage: claudex bundle <command> [arguments]

Commands:
  create <dest> [options]       Create or refresh a portable bundle
  run <bundle> [DIR...] [flags] Run a session isolated to that bundle
  destroy <bundle> [options]    Remove that bundle's local Docker resources
  install <bundle> [options]    Import into the host's regular Claudex state

Run 'claudex bundle <command> --help' for command options.`)
		return nil
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
		switch args[0] {
		case "create":
			fmt.Println("Usage: claudex bundle create <dest-dir> [--volumes <n1,n2>] [--jobs <1-16>] [--no-binaries] [--no-host-config] [--force]")
		case "run":
			fmt.Println("Usage: claudex bundle run <bundle> [--refresh] [--write-back] [--no-verify] [DIR...] [run-flags]")
		case "destroy":
			fmt.Println("Usage: claudex bundle destroy <bundle> [--no-verify]")
		case "install":
			fmt.Println("Usage: claudex bundle install <bundle> [--replace] [--volumes <n1,n2>] [--no-verify] [--with-host-config]")
		default:
			return fmt.Errorf("unknown bundle command %q", args[0])
		}
		return nil
	}
	switch args[0] {
	case "create":
		return BundleCreate(args[1:])
	case "run":
		return BundleRun(args[1:])
	case "destroy":
		return bundleDestroy(args[1:], &dockerx.CLI{})
	case "install":
		return BundleInstall(args[1:])
	default:
		return fmt.Errorf("unknown bundle command %q (use 'claudex bundle --help')", args[0])
	}
}
