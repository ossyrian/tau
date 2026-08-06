# Tau — controlled sandbox for Pi Coding Agent.
# Alpine (musl) base. aws-cli v2 comes from the Alpine community repo,
# which dodges the glibc pain of the official v2 installer on musl.
# 3.21 ships tmux 3.5a — Pi needs 3.5+ for `extended-keys-format csi-u`.
FROM alpine:3.21

# Toolchain the agent's scripts lean on. Trim what you don't use.
# zsh: interactive shell (`tau shell`). tmux: session persistence.
RUN apk add --no-cache \
      bash \
      zsh \
      tmux \
      coreutils \
      curl \
      git \
      jq \
      ca-certificates \
      aws-cli \
      python3 \
      py3-pip \
      nodejs \
      npm \
      uv

# --- Pi install -------------------------------------------------------------
# CONFIRM THIS. Swap PI_INSTALL for whatever Pi actually ships.
ARG PI_INSTALL="curl -fsSL https://pi.dev/install.sh | sh"
RUN eval "$PI_INSTALL" || echo "WARN: Pi install placeholder — fix PI_INSTALL"
# ---------------------------------------------------------------------------

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
      'set -g extended-keys on' \
      'set -g extended-keys-format csi-u' \
      > /etc/tau/tmux.conf

# uid pinned so the host tmpfs mount (uid=1000) lands owned by pi.
RUN adduser -D -u 1000 -h /home/pi pi
USER pi
WORKDIR /workspace

# uv-installed CLI tools (ty, …). Install as pi so the shims land in and are
# owned under /home/pi/.local/bin. ENV persists that dir on PATH to runtime —
# a `RUN export` would not (each RUN is a throwaway shell). Add more tools to
# the same `uv tool install` line.
ENV PATH="/home/pi/.local/bin:$PATH"
RUN uv tool install ty@latest

# Agent's own scripts (Pi favours scripts over MCP). The CLI bind-mounts the
# host scripts/ over this at runtime; the baked copy keeps the image portable.
COPY --chown=pi:pi scripts/ /home/pi/scripts/

# Long-lived container: hold PID 1 open so sessions can exec in over its life.
CMD ["sleep", "infinity"]
