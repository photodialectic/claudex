# Claudex
![claudex logo](https://www.nickhedberg.com/images/vD-aaHs4dzNr78KxDN4JFG8PLi8=/fit-in/1024x0/https://s3-us-west-2.amazonaws.com/nick-hedberg/img%2F545%3A1000%2Fc256ce85459b69da49ba801fd116a9170129f09c.png)
Claudex is a Docker-based environment for agentic AI systems (Anthropic Claude Code and OpenAI Codex), with strict firewall isolation and Git-based workspace tracking.

## Installation

Ensure you have the following prerequisites:

- Go 1.16 or later (for building the CLI)
- Docker 20.10 or later (for running container sessions)

### Install CLI

Using Go:

```bash
go install github.com/photodialectic/claudex@latest
```

Or from source:

```bash
git clone https://github.com/photodialectic/claudex.git
cd claudex
make install
```

To uninstall:

```bash
make uninstall
```

Ensure `$GOPATH/bin` or `/usr/local/bin` is in your `PATH`.

### Build container image

```bash
claudex build
```

Or using make:

```bash
make image          # Build CLI + image
make rebuild-image  # Force rebuild image only
```

### Refresh CLI tools inside the image

```bash
claudex update
```

Add `--no-cache` if you want to force a full rebuild during the refresh.

## Usage

### Launch Container Session

```bash
claudex [OPTIONS] [DIR1 DIR2 ...]
```

**Options:**
- `--host-network` - Use host networking (allows OAuth callbacks)
- `--name <NAME>` - Override derived container name
- `--parallel` - Always create new container (suffix with timestamp)
- `--replace` - Replace target container if it exists
- `--strict-mounts` - Error if existing container mounts differ

**Behavior:**
- Mounts each `DIR` at `/workspace/<basename(DIR)>` inside container
- If no directories provided, mounts current directory contents at `/workspace/<name>`
- Auto-initializes local Git repository at `/workspace` for change tracking
- Applies firewall to restrict network access
- Provides `claude-code`, `codex`, and `gemini-cli` tools

**Examples:**
```bash
claudex                              # Mount current directory
claudex service1/ service2/          # Mount multiple directories
claudex --host-network app/          # Enable host networking
claudex --name myproject app/        # Custom container name
claudex --parallel --replace app/    # Force new container
```

### Container Management

**Build/update image:**
```bash
claudex build
claudex update
```

**List containers:**
```bash
claudex list [OPTIONS]
  --all|--running|--stopped    # Filter by status
  --format table|json|names    # Output format
  --filter key=value           # Filter by name, signature, slug
```

**Destroy containers:**
```bash
claudex destroy [OPTIONS]
  --name <NAME>           # Target specific container
  --signature <HASH>      # Target by signature
  --all                   # Target all containers
  --running|--stopped     # Filter by status
  --force                 # Skip confirmation
  --prune-stopped         # Remove all stopped containers
```

**File operations:**
```bash
claudex push [--name <NAME>] <file_or_dir> [...]          # Copy to container
claudex pull [--name <NAME>] <container_path> [dest_dir]  # Copy from container
```

### Spec-Driven Development Workflow

Claudex supports spec-driven development by allowing you to share specifications with running containers:

**1. Create specification locally:**
```bash
echo "# Project Spec" > SPEC.md
# Edit your specification file with project requirements
```

**2. Push spec to running container:**
```bash
claudex push SPEC.md
# Spec is now available at /workspace/SPEC.md inside container
```

**3. Pull updated artifacts:**
```bash
claudex pull /workspace/updated-files.zip ./output/
# Retrieve AI-generated code and documentation
```

**Best practices for SPEC.md:**
- Focus on "what" and "why", not implementation details
- Define clear user journeys and success criteria
- Specify constraints and organizational standards
- Use clear, unambiguous language
- Include measurable outcomes

Inside the container:
- A firewall is applied to restrict network access.
- Local Git repository available for change tracking (local only, no remote)
- `claude-code`, `codex`, and `gemini-cli` tools available


## Troubleshooting

- Ensure directories you specify exist and are readable.
- If you see errors about missing `.claude.json`, place your credentials at `~/.claude.json`.
- To uninstall, remove the `claudex` binary from your `PATH`.


## Experimental Features

### Built-in Google Docs MCP server
The base container now ships with a FastAPI/fastmcp server that can create,
read, and update Google Docs through your account.

- Location: `/opt/google-docs-mcp` (source lives in `internal/buildctx/google-docs-mcp`)
- Start it inside the container with `google-docs-mcp` (installed on `$PATH`) for HTTP, or `google-docs-mcp --stdio` if your client only supports stdio transports
- OAuth redirect defaults to `http://localhost:8810/auth/callback`. Use
  `MCP_PUBLIC_BASE_URL` if you expose it elsewhere.
- Drop your OAuth client JSON at `/opt/google-docs-mcp/google_oauth_client.json`
  (or set `GOOGLE_CLIENT_CREDENTIALS`) before launching.
- The container automatically mounts your host `~/.claudex` directory (if it exists)
  to `/home/node/.claudex` and uses `google_oauth_client.json` / `google-docs-token.json`
  from there by default, so your credentials persist between sessions.
- The REST tools and MCP functions expect **Markdown** input; headings, lists, bold,
  italics, and links are converted into native Google Docs formatting automatically. All
  write/read endpoints accept an optional `tab_id` so you can work inside specific tabs.
- Once running, the MCP HTTP endpoint is `http://localhost:8810/mcp`. Point Codex
  or Claude to that transport to get the `start_google_auth_flow`, `create_google_doc`,
  `append_google_doc`, `replace_google_doc`, `read_google_doc`, and
  `list_google_doc_tabs` tools.
- Run `claudex auth google-docs-mcp [--container <name>]` for a guided OAuth flow (omit
  `--container` to pick from a list). The command starts
  the MCP server in your container, prints the Google consent link, and prompts you to
  paste the redirected URL so it can finish the callback and write tokens into `~/.claudex`.

The server also exposes REST endpoints for `/health`, `/auth/start`, `/auth/status`,
`/auth/callback`, and `/docs/*` which makes it easy to test outside of MCP clients.

### Docker MCP Gateway
Docker has an [MCP Gateway](https://github.com/docker/mcp-gateway/blob/main/docs/mcp-gateway.md) you can run on your host and then connect Codex or Claude to it as an MCP server. What's nice about this is you can run a single instance of the gateway and have multiple containers connect to it as MCP servers without needing to run separate MCP servers in each container.

#### Configure MCP Gateway Compose in .claudex
In `~/.claudex/compose/mcp-gateway.yml`, add the following:

```yaml
services:
  mcp-gateway:
    image: docker/mcp-gateway:dind
    container_name: claudex-mcp-gateway
    privileged: true
    ports:
      - "3000:3000"
    command:
      - --port=3000
      - --transport=http
      - --servers=fetch,playwright
      - --memory=512Mb
```

#### Run the Gateway
You can run this on your host and you can configure the port to whatever makes sense for your setup.

```bash
docker compose -f ~/.claudex/mcp-gateway.yaml up -d
```

### Configure Codex
In `~/.codex/config.toml`, add the following:

```toml
[mcp_servers.docker_gateway]
transport = "http"
url = "http://host.docker.internal:3000/mcp"
```

#### Configure Claude
In `~/.claude.json`, add the following:

```json
"mcpServers": {
  "DockerMCPGateway": {
    "type": "http",
    "url": "http://host.docker.internal:3000/mcp"
  }
}
```

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
      "docker",
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
