## Google Docs MCP Server (FastAPI)

FastAPI application that exposes Google Docs automation tools as a local [Model Context Protocol](https://modelcontextprotocol.io) server.  It ships with REST endpoints for health checks + OAuth helpers and registers matching MCP tools for creating, reading, appending, and replacing Google Docs.

### Features
- OAuth 2.0 flow (Auth URL + callback) with cached refresh tokens.
- FastAPI routes you can hit from a browser or `curl` for quick testing.
- MCP tools powered by [fastmcp](/), mounted at `/mcp` for HTTP/SSE clients.
- Shared business logic so REST and MCP tools stay in sync.
- Markdown-first authoring: write Markdown in tool/REST calls and the server converts it
  into Google Docs headings, lists, bold/italic spans, and links automatically.
- Tab-aware operations: pass a `tab_id` to read/append/replace to target a specific tab,
  or call the tab-listing tool/endpoint to discover tab IDs and hierarchy.

### Prerequisites
1. A Google Cloud project with the **Docs API** enabled.
2. An OAuth client of type **Desktop** or **Web application** with an authorized redirect of `http://localhost:8810/auth/callback` (or whatever port you run on).
3. Save the downloaded OAuth client JSON to `google_oauth_client.json` (default) or set `GOOGLE_CLIENT_CREDENTIALS=/path/to/client.json`.

### Install & Run
Dependencies are pre-synced in the Claudex container image and the helper command
`google-docs-mcp` is placed on `$PATH`.

> Keep your Google OAuth client and tokens under `~/.claudex` on the host. Claudex
> automatically mounts it to `/home/node/.claudex` inside the container, which is now the
> default location for the server’s `GOOGLE_CLIENT_CREDENTIALS` and `GOOGLE_TOKEN_CACHE`.

```bash
# inside a Claudex container
google-docs-mcp                      # FastAPI + HTTP MCP on http://0.0.0.0:8810

# stdio mode (no REST endpoints, for clients that insist on stdio MCP)
google-docs-mcp --stdio

# manual run (custom host/port via env vars)
cd /opt/google-docs-mcp
uv run python main.py --host 0.0.0.0 --port 9000
```

Environment knobs (`.env` or exported vars before launching the server):

| Variable | Purpose | Default |
| --- | --- | --- |
| `MCP_SERVER_HOST` | Bind host for FastAPI | `0.0.0.0` |
| `MCP_SERVER_PORT` | Bind port | `8810` |
| `MCP_PUBLIC_BASE_URL` | Public URL used for OAuth redirects | `http://localhost:<port>` |
| `GOOGLE_CLIENT_CREDENTIALS` | Path to OAuth client JSON | `/home/node/.claudex/google_oauth_client.json` (auto-mounted from host `~/.claudex` when available) |
| `GOOGLE_TOKEN_CACHE` | Where refresh/access tokens live | `/home/node/.claudex/google-docs-token.json` |

### OAuth Flow
1. Hit `POST /auth/start` (curl or browser) to receive an `authorization_url` and `state`.
2. Open the `authorization_url`, grant access, and you’ll be redirected to `/auth/callback`.
3. The callback writes refreshed credentials to the token cache.  `GET /auth/status` should now report `authenticated: true`.

### REST API Cheatsheet
| Method & Path | Description |
| --- | --- |
| `GET /health` | Simple status & credential check |
| `POST /auth/start` | Returns OAuth consent URL |
| `GET /auth/callback` | Handles Google redirect |
| `POST /docs` | Create a document (`title`, optional `initial_text`) |
| `POST /docs/{id}/append` | Append text to the end of a doc (body accepts optional `tab_id`) |
| `PUT /docs/{id}` | Replace all content with new text (body accepts optional `tab_id`) |
| `GET /docs/{id}` | Fetch plain-text content (`tab_id` query param) |
| `GET /docs/{id}/tabs` | List tab IDs/titles/depth for the doc |

All responses are small JSON payloads (see `google_docs_mcp/schemas.py` for shapes).

### MCP Endpoint
The FastMCP server is mounted at `http://<host>:<port>/mcp`.  Clients that support HTTP transports can point to that URL and call these tools:

- `start_google_auth_flow`
- `check_google_auth_status`
- `create_google_doc`
- `append_google_doc`
- `replace_google_doc`
- `read_google_doc`
- `list_google_doc_tabs`

Most MCP-aware clients now support HTTP transports; configure them with `transport = http` and `url = http://localhost:8810/mcp` (names/fields vary slightly per client).

> Tip: When running inside Claudex without host networking, start your container with `--host-network` so Google can reach `http://localhost:<port>/auth/callback`. If you run `--stdio`, the REST endpoints are disabled, so complete OAuth while the server is in HTTP mode first. All document body inputs are treated as Markdown; plain text still works, but headings/bullets/bold italicize automatically when you use Markdown syntax. Use the `tab_id` parameters (or the `list_google_doc_tabs` tool / `GET /docs/{id}/tabs` endpoint) to work with documents that have multiple tabs.

### Hacking / Local Development
The source lives in this repository at `internal/buildctx/google-docs-mcp`. If you
need to experiment, clone Claudex, edit the Python code there, and rebuild the
container with `make image` (or `claudex build`). For ad-hoc tweaks inside a
running container, copy `/opt/google-docs-mcp` into your `/workspace` volume and
run `uv sync` before launching.
