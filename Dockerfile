FROM debian:stable-slim

# libraries/utilities to be shipped with Tau's container
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      bash \
      zsh \
      tmux \
      coreutils \
      curl \
      unzip \
      git \
      jq \
      ca-certificates \
      locales \
      python3 \
      python3-pip \
      python3-venv \
      gnupg \
      procps \
    && rm -rf /var/lib/apt/lists/*

# Pi needs Node >22
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

# don't render jank fonts
RUN sed -i 's/^# *\(en_US.UTF-8\)/\1/' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8

# AWS
RUN arch="$(uname -m)" && \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${arch}.zip" -o /tmp/awscliv2.zip && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install && \
    rm -rf /tmp/awscliv2.zip /tmp/aws

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends gh && \
    rm -rf /var/lib/apt/lists/*

# treehouse
ARG TREEHOUSE_VERSION=v2.1.1
ARG NO_MISTAKES_VERSION=v1.45.4
RUN arch="$(dpkg --print-architecture)" && \
    curl -fsSL "https://github.com/kunchenguid/treehouse/releases/download/${TREEHOUSE_VERSION}/treehouse-${TREEHOUSE_VERSION}-linux-${arch}.tar.gz" \
      | tar xz -C /usr/local/bin treehouse && \
    curl -fsSL "https://github.com/kunchenguid/no-mistakes/releases/download/${NO_MISTAKES_VERSION}/no-mistakes-${NO_MISTAKES_VERSION}-linux-${arch}.tar.gz" \
      | tar xz -C /usr/local/bin no-mistakes

# uv
RUN curl -fsSL https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

# pi
ARG PI_INSTALL="curl -fsSL https://pi.dev/install.sh | sh"
RUN eval "$PI_INSTALL"

# shared AWS creds
ENV AWS_SHARED_CREDENTIALS_FILE=/home/pi/.tau/credentials

# headless-ish tmux config for the container
RUN mkdir -p /etc/tau && \
    printf '%s\n' \
      'set -g status off' \
      'set -g mouse on' \
      'set -g remain-on-exit off' \
      'set -g detach-on-destroy on' \
      'set -g default-terminal "tmux-256color"' \
      'set -ga terminal-features ",*:RGB"' \
      'set -g extended-keys on' \
      'set -g extended-keys-format csi-u' \
      > /etc/tau/tmux.conf

# uid pinned so the host tmpfs mount (uid=1000) lands owned by pi.
RUN useradd -m -u 1000 -d /home/pi -s /bin/zsh pi
USER pi
WORKDIR /workspace

# tools managed by uv - add more here if you need
ENV PATH="/home/pi/.local/bin:/home/pi/scripts:$PATH"
RUN uv tool install ty@latest

# state directories for no-mistakes and treehouse
RUN mkdir -p /home/pi/.no-mistakes /home/pi/.treehouse && \
    printf 'agent: pi\n' > /home/pi/.no-mistakes/config.yaml

# `no-mistakes init` installs its agent skill to ~/.agents/skills and
# ~/.claude/skills — neither of which Pi reads, and neither persists. Point
# both at Pi's mounted skills dir so the skill lands in pi-config (persistent)
# and shows up in Pi's skill scan. Dangling at build time; resolves at runtime
# once ~/.pi is mounted.
RUN mkdir -p /home/pi/.agents && \
    ln -s /home/pi/.pi/agent/skills /home/pi/.agents/skills && \
    ln -s /home/pi/.pi/agent/skills 

# Treehouse reads hooks only from ~/.config/treehouse/config.toml (repo-level
# hooks are ignored for safety). Symlink it into the mounted pi-config so hook
# config (e.g. per-worktree env-file setup) persists across recreates.
RUN mkdir -p /home/pi/.config && \
    ln -s /home/pi/.pi/agent/treehouse /home/pi/.config/treehouse

# agent-managed scripts
COPY --chown=pi:pi scripts/ /home/pi/scripts/

# Long-lived container: hold PID 1 open so sessions can exec in over its life.
CMD ["sleep", "infinity"]
