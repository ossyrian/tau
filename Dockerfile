# Tau — controlled sandbox for Pi Coding Agent.
# Debian stable (glibc) base. Alpine's musl broke some package builds, so we
# use the official Debian image, which ships tmux 3.5+ (Pi needs 3.5+ for
# `extended-keys-format csi-u`).
FROM debian:stable-slim

# Toolchain the agent's scripts lean on. Trim what you don't use.
# zsh: interactive shell (`tau shell`). tmux: session persistence.
# aws-cli v2 is not in Debian apt, so it's installed from the official
# bundle below rather than via the (v1) awscli package.
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
    && rm -rf /var/lib/apt/lists/*

# Node.js from NodeSource. Debian stable's apt nodejs is v20, but Pi's installer
# requires Node >= 22.19.0, so pull Node 22.x from the official NodeSource repo.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

# UTF-8 locale so icon tools (starship, eza, lsd, …) that detect UTF-8 via the
# LANG/LC_* env vars emit NERD-font PUA codepoints instead of ASCII fallbacks.
# glibc needs the locale generated (unlike musl's built-in C.UTF-8).
RUN sed -i 's/^# *\(en_US.UTF-8\)/\1/' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8

# aws-cli v2 from the official bundle (no maintained apt package on Debian).
RUN arch="$(uname -m)" && \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${arch}.zip" -o /tmp/awscliv2.zip && \
    unzip -q /tmp/awscliv2.zip -d /tmp && \
    /tmp/aws/install && \
    rm -rf /tmp/awscliv2.zip /tmp/aws

# uv (standalone installer; no apt package). Land it in /usr/local/bin so it's
# on PATH for every user, including the pi user below.
RUN curl -fsSL https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

# Pi install. Override with `PI_INSTALL=... ./build.sh` (pin a version, or
# "npm install -g --ignore-scripts @earendil-works/pi-coding-agent").
ARG PI_INSTALL="curl -fsSL https://pi.dev/install.sh | sh"
RUN eval "$PI_INSTALL"

# AWS SDK reads creds from this file. The host keeps it fresh (see the CLI's
# `refresh`); it lives on a container-only tmpfs, never on host disk.
ENV AWS_SHARED_CREDENTIALS_FILE=/home/pi/.tau/credentials

# Minimal tmux config on a dedicated socket ("tau"). Status bar off so a
# session feels like a raw shell; Ctrl-b d detaches back, Pi keeps running.
# extended-keys: Pi needs modified Enter (Shift/Ctrl+Enter) to survive tmux,
# else its "Enter submits, Shift+Enter newline" keybindings break.
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

# uv-installed CLI tools (ty, …). Install as pi so the shims land in and are
# owned under /home/pi/.local/bin. ENV persists that dir on PATH to runtime —
# a `RUN export` would not (each RUN is a throwaway shell). Add more tools to
# the same `uv tool install` line.
ENV PATH="/home/pi/.local/bin:/home/pi/scripts:$PATH"
RUN uv tool install ty@latest

# Agent's own scripts (Pi favours scripts over MCP). The CLI bind-mounts the
# host scripts/ over this at runtime; the baked copy keeps the image portable.
COPY --chown=pi:pi scripts/ /home/pi/scripts/

# Long-lived container: hold PID 1 open so sessions can exec in over its life.
CMD ["sleep", "infinity"]
