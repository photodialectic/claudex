FROM node:22

# install Docker’s official CLI only
USER root
RUN apt-get update && apt-get install -y \
      ca-certificates \
      curl \
      gnupg \
      lsb-release \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg \
         | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && echo \
         "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
         https://download.docker.com/linux/debian \
         $(lsb_release -cs) stable" \
         > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*
ARG TZ
ENV TZ="$TZ"

# Install basic development tools and iptables/ipset
RUN apt update && apt install -y less \
  git \
  procps \
  sudo \
  fzf \
  zsh \
  man-db \
  unzip \
  gnupg2 \
  gh \
  iptables \
  ipset \
  iproute2 \
  dnsutils \
  aggregate \
  jq \
  ripgrep \
  fd-find \
  socat \
  tree

# Ensure default node user has access to /usr/local/share
RUN mkdir -p /usr/local/share/npm-global && \
  chown -R node:node /usr/local/share

ARG USERNAME=node

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

# Set `DEVCONTAINER` environment variable to help with orientation
ENV DEVCONTAINER=true

# Create workspace, context, config, and instructions directories and set permissions
RUN mkdir -p /workspace /context /home/node/.claude /home/node/.codex /home/node/.copilot /home/node/.gemini /home/node/.claudex /home/node/instructions && \
  chown -R node:node /workspace /context /home/node/.claude /home/node/.codex /home/node/.copilot /home/node/.gemini /home/node/.claudex /home/node/instructions

WORKDIR /workspace

RUN ARCH=$(dpkg --print-architecture) && \
  wget "https://github.com/dandavison/delta/releases/download/0.18.2/git-delta_0.18.2_${ARCH}.deb" && \
  dpkg -i "git-delta_0.18.2_${ARCH}.deb" && \
  rm "git-delta_0.18.2_${ARCH}.deb"

# Make Google Docs MCP server files available in the container
ENV GOOGLE_DOCS_MCP_HOME=/opt/google-docs-mcp
COPY google-docs-mcp ${GOOGLE_DOCS_MCP_HOME}
RUN chown -R node:node ${GOOGLE_DOCS_MCP_HOME} && \
    printf '%s\n' \
      '#!/bin/bash' \
      'set -euo pipefail' \
      'cd '"${GOOGLE_DOCS_MCP_HOME}" \
      'exec .venv/bin/python main.py "$@"' \
      > /usr/local/bin/google-docs-mcp && \
    chmod +x /usr/local/bin/google-docs-mcp

# Set up non-root user
USER node

# Install global packages
ENV NPM_CONFIG_PREFIX=/usr/local/share/npm-global
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH=/home/node/.local/bin:/usr/local/share/npm-global/bin:$PATH
RUN cd ${GOOGLE_DOCS_MCP_HOME} && uv sync --frozen

# Set the default shell to bash rather than sh
ENV SHELL=/bin/zsh

# Default powerline10k theme
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v1.2.0/zsh-in-docker.sh)" -- \
  -p git \
  -p fzf \
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

# Install CLI toolchain; build arg lets update builds bust cache for this layer
ARG CLAUDEX_BUILD_VERSION=0
RUN echo "Installing CLI tools (build version: ${CLAUDEX_BUILD_VERSION})" \
  && npm install -g @anthropic-ai/claude-code \
  && npm install -g @openai/codex \
  && npm install -g @google/gemini-cli \
  && npm install -g @github/copilot

COPY CLAUDEX.md /workspace/CLAUDE.md
COPY CLAUDEX.md /workspace/AGENTS.md
COPY CLAUDEX.md /workspace/GEMINI.md

# Copy and set up firewall script
COPY init-firewall.sh /usr/local/bin/
USER root
RUN chmod +x /usr/local/bin/init-firewall.sh && \
    printf '%s\n' \
      'node ALL=(ALL) NOPASSWD: /usr/bin/docker' \
      'node ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh' \
      > /etc/sudoers.d/node-firewall && \
    chmod 0440 /etc/sudoers.d/node-firewall
USER node
