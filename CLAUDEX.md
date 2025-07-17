# Claudex Container Environment Instructions

## Overview
You are running inside a Claudex container, a secure Docker environment designed for AI agents. Your services are containerized and accessible via Docker commands.

## Container Architecture
- **Working directory**: `/workspace` (contains mounted service directories)
- **Context directory**: `/context` (for additional files added via `claudex include`)
- **Instructions directory**: `/workspace/instructions` (optional user-provided context)
- **Git repository**: Automatically initialized at `/workspace` on branch `main`

## Running Tests and Commands
Since services run in Docker containers, use this pattern:
```bash
sudo docker exec <container_name_or_id> <command>
```

**Important**: You may need to locate the container ID or name first using `sudo docker ps`.

Examples:
```bash
# First, find the container
sudo docker ps

# Run tests in a service container (using name)
sudo docker exec myapp_web /app/scripts/test

# Run tests in a service container (using container ID)
sudo docker exec a1b2c3d4e5f6 /app/scripts/test
```

## Available Tools
- **Docker CLI**: Full access to Docker commands
- **Git**: Pre-configured repository for tracking changes
- **Development tools**: ripgrep, fd-find, jq, fzf, tree
- **AI CLIs**: claude-code, codex, gemini-cli

## Network Environment
- **Firewall**: Restricted outbound network access for security
- **Docker socket**: Mounted at `/var/run/docker.sock` for container management
- **Host network**: Available via `--host-network` flag if needed for OAuth/callbacks

## Working with Services
1. **Identify containers**: Use `sudo docker ps` to list running containers
2. **Find service structure**: Check docker-compose files or Dockerfiles in mounted directories
3. **Execute commands**: Use `sudo docker exec` to run commands inside service containers
4. **View logs**: Use `sudo docker logs <container>` to debug issues

## Git Workflow
- Repository auto-initialized with initial commit
- Use standard Git commands: `git add`, `git commit`, `git push`
- Pre-configured with user "Claudex CLI" and email "claudex@local"

## File System Layout
```
/workspace/
 service1/          # Mounted service directory
 service2/          # Mounted service directory
 .git/              # Git repository
 instructions/      # Optional user instructions (if provided)

/context/              # Files added via `claudex include`
```

service1 and service2 are just placeholders. `ls /workspace` to see actual files available

## Best Practices
1. **Always check container status** before running commands
2. **Use proper container names** when executing commands
3. **Commit changes** to Git for persistence
4. **Check service logs** if commands fail
5. **Use `tree` or `ls` to explore mounted directories**

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
