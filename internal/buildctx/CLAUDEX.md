# Claudex Container Environment

## Project Overview
You are working inside a Claudex container - a secure, isolated Docker environment designed for AI coding agents. This environment provides controlled access to user services while maintaining security boundaries.

## Quick Reference
- Current directory: `/workspace` (contains user's mounted projects)
- Run commands in services: `sudo docker exec <container> <command>`
- Find containers: `sudo docker ps`
- View logs: `sudo docker logs <container>`

## Container Architecture
- **Working directory**: `/workspace` (contains mounted service directories)
- **Git repository**: Automatically initialized at `/workspace` on branch `main` - for local version control only

## Testing Services
Always identify running services first, then execute tests within the appropriate containers.

```bash
# Find running services first
sudo docker ps

# Common test patterns:
sudo docker exec <service> ./scripts/test      # Custom test scripts
sudo docker exec <service> npm test           # Node.js projects
sudo docker exec <service> python -m pytest   # Python projects
sudo docker exec <service> go test ./...       # Go projects

# Examples with specific containers:
sudo docker exec myapp_api /app/scripts/test
sudo docker exec myapp_web npm test
sudo docker exec api_server python -m pytest tests/
sudo docker exec backend_service go test ./...
```

## Code Style
- Follow each project's existing conventions (check package.json, .editorconfig, or similar)
- Use consistent indentation as found in the project (spaces or tabs)
- Avoid else branches - prefer early returns for clarity
- Don't leave comments when removing code, just remove it
- Match the project's naming conventions (camelCase, snake_case, etc.)
- Follow the project's import/require patterns - don't do inline imports or requires
- Respect existing formatting tools (prettier, black, gofmt, etc.)

## Security Guidelines
- Always use `sudo` for Docker commands within the container
- Never expose container ports unnecessarily
- Network access is restricted - only essential outbound connections allowed
- Container isolation provides security boundaries between services
- Docker socket is mounted for container management - use responsibly

## Available Tools
- **Docker CLI**: Full access to Docker commands (requires sudo)
- **Git**: Pre-configured repository for LOCAL tracking only - DO NOT perform git operations
- **Google Docs MCP server**: Run `google-docs-mcp` to launch the FastAPI/fastmcp
  service that can create/edit Google Docs through your account. The source lives at
  `/opt/google-docs-mcp`.

### Google Docs MCP quickstart
1. Store your Google OAuth client JSON (and optionally an existing token cache) in
   `~/.claudex/` on the host. Claudex mounts it to `/home/node/.claudex` inside the
   container, which is used by default for `GOOGLE_CLIENT_CREDENTIALS` and `GOOGLE_TOKEN_CACHE`.
2. Run `google-docs-mcp` inside the container. It listens on `http://0.0.0.0:8810`
   and exposes an MCP transport at `/mcp`. Use `claudex --host-network ...` if Google
   needs to call back to `localhost`. For stdio-only clients, launch `google-docs-mcp --stdio`
   (but complete OAuth while HTTP mode is active).
3. Hit `POST /auth/start` (or call the `start_google_auth_flow` tool) to get the consent URL.
4. Complete the browser flow; the `/auth/callback` endpoint writes cached tokens into
   `/home/node/.claudex` (persisted from your host).
5. Wire your MCP client to `http://localhost:8810/mcp` to use the Google Docs tools.
   Content you send to `create/append/replace` is interpreted as Markdown and converted
   to Docs headings, lists, and inline styles. Provide `tab_id` parameters (or call
   `list_google_doc_tabs`) when you need to work inside specific tabs.

## Specification-Driven Development
When working on projects, consider writing a SPEC.md file to document requirements:

- Focus on "what" and "why", not implementation details
- Define clear user journeys and success criteria
- Specify constraints and organizational standards
- Use clear, unambiguous language with measurable outcomes
- Structure: Problem → User Experience → Success Criteria → Constraints

**SPEC.md Template:**
```markdown
# Project Specification

## Problem Statement
[What problem are we solving and why?]

## User Experience
[How will users interact with this? What's the user journey?]

## Success Criteria
[How do we know when this is complete and working?]

## Constraints
[Technical, business, or organizational limitations]

## Implementation Notes
[Any specific requirements or standards to follow]
```

## Git Policy
**IMPORTANT**: Do not perform any git operations (add, commit, push, pull, etc.). Leave all git management to the user. The git repository is for local tracking only.

## Working with Services
1. **Identify containers**: Use `sudo docker ps` to list running containers
2. **Find service structure**: Check docker-compose files or Dockerfiles in mounted directories
3. **Execute commands**: Use `sudo docker exec` to run commands inside service containers
4. **View logs**: Use `sudo docker logs <container>` to debug issues

## File System Layout
```
/workspace/
 service1/          # Mounted service directory
 service2/          # Mounted service directory
 .git/              # Git repository
 SPEC-13.md         # (optional) Specification file
```

service1 and service2 are just placeholders. `ls /workspace` to see actual files available

## Best Practices
1. **Always check container status** before running commands
2. **Use proper container names** when executing commands
3. **Check service logs** if commands fail
4. **Use `tree` or `ls` to explore mounted directories**

## Common Patterns
```bash
# List all containers
sudo docker ps -a

# Find service containers
sudo docker ps --filter "name=myapp"

# Run script/test for full test suite
sudo docker exec myapp_web /app/scripts/test

# Run tests in Node.js service
sudo docker exec myapp_web npm test

# Run tests in Python service
sudo docker exec myapp_api python -m pytest

# View service logs
sudo docker logs myapp_web --tail=50 -f
```

## Troubleshooting
- **Permission denied**: Use `sudo` for Docker commands
- **Container not found**: Check `sudo docker ps` for correct container names
- **Network issues**: Verify firewall settings or use `--host-network`
- **File not found**: Ensure paths are correct within the container context
