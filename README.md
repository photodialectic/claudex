# Claudex

Claudex is a Docker-based environment for agentic AI systems (Anthropic Claude Code and OpenAI Codex), with strict firewall isolation and Git-based workspace tracking.

## Installation

Ensure you have the following prerequisites:

- Go 1.16 or later (for building the CLI)
- Docker 20.10 or later (for running container sessions)

### Install CLI

Option 1: Using Go

```bash
go install github.com/photodialectic/claudex@latest
```

Option 2: From source

```bash
git clone https://github.com/photodialectic/claudex.git
cd claudex
./build [install_dir]   # default: /usr/local/bin
```

Ensure `$GOPATH/bin` or `$GOBIN` is in your `PATH` if you used `go install`.

### Install or update container image

```bash
claudex install
```

Alternatively, to manually build the Docker image:

```bash
docker build -t claudex .
```

## Usage

Launch a container session:

```bash
claudex [DIR1 DIR2 ...]
```

- Mounts each `DIRi` at `/workspace/<basename(DIRi)>` inside the container.
- If no directories are provided, mounts the current directory.
- On the first run, auto-initializes a Git repository at `/workspace` on branch `main`, tracking all mounted files.
- Optionally, mount an instructions file or directory during startup.

Examples:

```bash
# Mount current directory
claudex

# Mount multiple service folders
claudex service1/ service2/
```

Inside the container:
- A firewall is applied to restrict network access.
- You have a persistent Git repository to commit changes.
- Both `claude-code` and `codex` CLIs are available.


## Troubleshooting

- Ensure directories you specify exist and are readable.
- If you see errors about missing `.claude.json`, place your credentials at `~/.claude.json`.
- To uninstall, remove the `claudex` binary from your `PATH`.
