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
./install [install_dir]   # default: /usr/local/bin
```

Ensure `$GOPATH/bin` or `$GOBIN` is in your `PATH` if you used `go install`.

### Build or update container image

```bash
claudex build
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
- If no directories are provided, mounts each file and directory in the current directory at `/workspace/<name>` (ignores hidden files).
- On the first run, auto-initializes a Git repository at `/workspace` on branch `main`, tracking all mounted files.
- Optionally, mount an instructions file or directory during startup. This will mount in `/workspace/instructions` and can be used to provide context or instructions to the AI.

Examples:

```bash
# Mount current directory and files (excluding hidden files)
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


## Experimental Features

### Dockerized MCP Servers
Following [Dockerized MCP Servers](https://hub.docker.com/mcp), this will follow the example of the [Fetch Docker MCP](https://hub.docker.com/mcp/server/fetch/overview).

Install per instructions.

#### Claude MCP
In `~/.claude.json`, add the following:

```json
"mcpServers": {
  "fetch": {
    "command": "sudo",
    "args": [
      "/usr/bin/docker",
      "run",
      "-i",
      "--rm",
      "mcp/fetch"
    ]
  }
}
```

#### Codex MCP
In `~/.codex/config.toml`, add the following:

```toml
[mcp_servers.fetch]
command = "sudo"
args = ["docker", "run", "-i", "--rm", "mcp/fetch"]
```

#### Codex MCP

This is sorta wild but kinda fun.

In `~/.claude.json`, add the following:

```json
"mcpServers": {
  "codex": {
    "command": "codex",
    "args": [
      "mcp"
    ]
  }
}
```

Then when you use claude, you can see it’s one of the MCP servers:

```
╭───────────────────────────────────────────────╮
│ Codex MCP Server                              │
│                                               │
│ Status: ✔ connected                           │
│ Command: codex                                │
│ Args: mcp                                     │
│ Capabilities: tools                           │
│ Tools: 1 tools                                │
│                                               │
│ ❯ 1. View tools                               │
╰───────────────────────────────────────────────╯
```

Which means you can use codex as a tool in Claude - agentception!

<details>
<summary>Bug with `codex mcp` command</summary>

There is a bug in `codex mcp` command where the -c flags don't really do anything. I wanted to override the model and provider, but it doesn't work.

If I want to use my own model/provider, I have to set those as root values in `~/.codex/config.toml`:

```toml
model_provider = "nhdc_ai_api_dev"
model = "claude-3-5-haiku"
```

Below is proof of the bug:

```
$ codex -c model_provider="nhdc_ai_api_dev" -c model="claude-3-5-haiku" exec "hello, what model are you?"
[2025-07-13T16:43:38] OpenAI Codex v0.5.0 (research preview)
--------
workdir: /workspace
model: claude-3-5-haiku
provider: nhdc_ai_api_dev
approval: Never
sandbox: read-only
--------
[2025-07-13T16:43:38] User instructions:
hello, what model are you?
[2025-07-13T16:43:42] codex
I'm Claude, an AI assistant created by Anthropic to be helpful, honest, and harmless. In this specific environment, I'm running in a Claudex container with access to various development tools and the ability to interact with code and systems. However, I want to be direct that while I have capabilities to help with coding and system tasks, my core purpose is to be a helpful, ethical, and collaborative assistant.

Would you like me to help you with a specific coding or development task? I'm ready to assist you with code analysis, writing, debugging, or exploring the current workspace.
```
We can observe that `codex -c...` flags work for the `exec` command. You can see from below, `codex mcp` claims to allow the same flags.

```
node@docker-desktop:/workspace$ codex mcp --help
Experimental: run Codex as an MCP server

Usage: codex-aarch64-unknown-linux-musl mcp [OPTIONS]

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed as JSON. If it fails
          to parse as JSON, the raw string is used as a literal.

          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c shell_environment_policy.inherit=all`

  -h, --help
          Print help (see a summary with '-h')
```

Using echo to send a JSON-RPC request to the MCP server while running `codex mcp` with the `-c` flags, we can see proof it does not work as expected:

```
node@docker-desktop:/workspace$ echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"tool":"codex","name":"codex","arguments":{"prompt":"hello, what model are you?"}}}' | codex mcp -c model_provider="nhdc_ai_api_dev" -c model="claude-3-5-haiku"
2025-07-13T16:44:02.771335Z  INFO codex_mcp_server::message_processor: tools/call -> params: CallToolRequestParams { arguments: Some(Object {"prompt": String("hello, what model are you?")}), name: "codex" }
2025-07-13T16:44:02.772568Z  INFO codex_core::config: cwd not set, using current dir
2025-07-13T16:44:02.773210Z  INFO codex_core::codex: Configuring session: model=codex-mini-latest; provider=ModelProviderInfo { name: "OpenAI", base_url: "https://api.openai.com/v1", env_key: Some("OPENAI_API_KEY"), env_key_instructions: Some("Create an API key (https://platform.openai.com) and export it as an environment variable."), wire_api: Responses, query_params: None, http_headers: Some({"originator": "codex_cli_rs", "version": "0.5.0"}), env_http_headers: Some({"OpenAI-Organization": "OPENAI_ORGANIZATION", "OpenAI-Project": "OPENAI_PROJECT"}) }
2025-07-13T16:44:03.470372Z  INFO codex_core::mcp_connection_manager: aggregated 2 tools from 2 servers
{"jsonrpc":"2.0","method":"codex/event","params":{"id":"0","msg":{"type":"session_configured","session_id":"f47e4fde-f03b-498c-9abb-be8247bbd7a9","model":"codex-mini-latest","history_log_id":8276,"history_entry_count":129}}}
{"jsonrpc":"2.0","method":"codex/event","params":{"id":"1","msg":{"type":"task_started"}}}
2025-07-13T16:44:05.697925Z  INFO codex_core::codex: Aborting existing session
{"jsonrpc":"2.0","method":"codex/event","params":{"id":"1","msg":{"type":"agent_message","message":"Hi there! I’m ChatGPT, powered by OpenAI’s GPT‑4 architecture. How can I help you today?"}}}
{"jsonrpc":"2.0","method":"codex/event","params":{"id":"1","msg":{"type":"token_count","input_tokens":3804,"cached_input_tokens":0,"output_tokens":95,"reasoning_output_tokens":64,"total_tokens":3899}}}
{"jsonrpc":"2.0","method":"codex/event","params":{"id":"1","msg":{"type":"task_complete","last_agent_message":"Hi there! I’m ChatGPT, powered by OpenAI’s GPT‑4 architecture. How can I help you today?"}}}
{"id":1,"jsonrpc":"2.0","result":{"content":[{"text":"Hi there! I’m ChatGPT, powered by OpenAI’s GPT‑4 architecture. How can I help you today?","type":"text"}]}}
```

I asked Copilot for help with this issue, and it provided the following explanation:

> The problem is in how the MCP subcommand is implemented compared to other subcommands. Here's what's happening:

> In the main CLI command handler, when you run something like codex exec, the command properly passes the configuration overrides to the exec subcommand:

```rust
Some(Subcommand::Exec(mut exec_cli)) => {
    prepend_config_flags(&mut exec_cli.config_overrides, cli.config_overrides);
        codex_exec::run_main(exec_cli, codex_linux_sandbox_exe).await?;
        }
```

> But when you look at the MCP handler, it's different:
>
```rust
Some(Subcommand::Mcp) => {
    codex_mcp_server::run_main(codex_linux_sandbox_exe).await?;
    }
```
</details>

### Codex Provider/Profile overrides

I run a [litellm reverse proxy](https://docs.litellm.ai/docs/simple_proxy). I thought it'd be cool to route Codex requests through it.

Set up profiles in `~/.codex/config.toml`:

```toml
[model_providers.nhdc_ai_api]
name = "Nick Hedberg Dot Com AI API"
base_url = "https://www.nickhedberg.com/ai-api"
env_key = "AI_API_MK"

[model_providers.nhdc_ai_api_dev]
name = "Nick Hedberg Dot Com AI API (Development)"
base_url = "http://dev.nickhedberg.com/ai-api"
env_key = "AI_API_MK"

# Profile to select the Nick Hedberg Dot Com AI API provider
[profiles.nhdc_ai_api]
model_provider = "nhdc_ai_api"
model = "claude-3-7-sonnet"

[profiles.nhdc_ai_api_dev]
model_provider = "nhdc_ai_api_dev"
model = "claude-3-7-sonnet"
```

```
codex --profile nhdc_ai_api
```

```
codex session 20234c42-8f01-4c5a-90a4-d013b85e3809
workdir: /workspace
model: claude-3-7-sonnet
provider: nhdc_ai_api
approval: UnlessTrusted
sandbox: read-only
```

or with -c flags:

```
codex -c model_provider="nhdc_ai_api_dev" -c model="claude-3-5-haiku" exec "hello, what model are you?"
```

<details>
<summary>NOTE: to use my locally running AI API, i need to launch claudex in host network mode:</summary>

```
claudex --host-network
```
</details>

### Docker Models
You can run models in Docker containers. This is useful for running models that require specific environments or dependencies. [Docker Model Runner](https://docs.docker.com/ai/model-runner/)

in `~/.codex/config.toml`, add the following:

```toml
[model_providers.docker_engine]
name = "Docker Engine"
base_url = "http://model-runner.docker.internal/engines/v1"

[profiles.docker_engine]
model_provider = "docker_engine"
model = "ai/smollm2:360M-Q4_K_M"
```

```
codex --profile docker_engine
```

Test the endpoint (run inside a container):

```bash
curl http://model-runner.docker.internal/engines/v1/chat/completions \
-H "Content-Type: application/json" \
-d '{
  "model": "ai/smollm2:360M-Q4_K_M",
    "messages": [
      {
        "role": "system",
        "content": "You are a helpful assistant."
      },
      {
        "role": "user",
        "content": "hello?"
      }
  ]
}'

{"choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"Hello, I'm here to help you with your language learning needs. What would you like to talk about? Do you have a specific question or topic in mind?"}}],"created":1752462477,"model":"ai/smollm2:360M-Q4_K_M","system_fingerprint":"b1-9c98bab","object":"chat.completion","usage":{"completion_tokens":34,"prompt_tokens":21,"total_tokens":55},"id":"chatcmpl-ADlVE5WjDfEgqAAhH2ukxs9aP0ETIcdF","timings":{"prompt_n":21,"prompt_ms":47.946,"prompt_per_token_ms":2.283142857142857,"prompt_per_second":437.9927418345639,"predicted_n":34,"predicted_ms":296.108,"predicted_per_token_ms":8.709058823529412,"predicted_per_second":114.82296999743338}}
```
