# tau proxy
FROM golang:1.27 AS proxy

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/ cmd/
RUN CGO_ENABLED=0 go build -trimpath -o /out/tau-proxy ./cmd/tau-proxy

# actual tau runtime
FROM debian:stable-slim

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      zsh tmux curl ca-certificates locales procps socat \
    && rm -rf /var/lib/apt/lists/*

# pi needs Node
RUN curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

RUN sed -i 's/^# *\(en_US.UTF-8\)/\1/' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8

ARG TREEHOUSE_VERSION=v3.1.6
RUN arch="$(dpkg --print-architecture)" && \
    curl -fsSL "https://github.com/kunchenguid/treehouse/releases/download/${TREEHOUSE_VERSION}/treehouse-${TREEHOUSE_VERSION}-linux-${arch}.tar.gz" \
      | tar xz -C /usr/local/bin treehouse

# Source for the md2html package in config.yaml, which installs it from here.
COPY tools/md2html/ /opt/tau/tools/md2html/

# pi itself
RUN npm install -g --ignore-scripts --no-fund --no-audit @earendil-works/pi-coding-agent

# in-container tmux
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

# tau-relay allows the container to talk to a browser on the host machine
COPY tau-relay /usr/local/bin/tau-relay
COPY --from=proxy /out/tau-proxy /usr/local/bin/tau-proxy

# profile users/groups/permissions
RUN groupadd -g 1100 tau-shared && \
    groupadd -g 1101 tau-clients && \
    groupadd -g 1102 tau-proxies && \
    useradd -m -u 1000 -d /home/pi -s /bin/zsh -G tau-shared,tau-clients pi && \
    chmod 700 /home/pi && \
    install -d -m 2770 -o root -g tau-shared /srv/shared /srv/shared/workspace && \
    ln -s /srv/shared/workspace /workspace && \
    install -d -m 0755 /run/tau-proxy /opt/tau/shims

# default user
USER pi
WORKDIR /srv/shared/workspace
ENV PATH="/opt/tau/shims:/home/pi/.local/bin:/home/pi/scripts:$PATH"

# DO NOT REMOVE: the following line installs any packages in your tau config
# @tau:packages

# in-container treehouse config
RUN mkdir -p /home/pi/.treehouse /home/pi/share /home/pi/.config/tau && \
    ln -s /home/pi/.pi/agent/treehouse /home/pi/.config/treehouse

COPY --chown=pi:pi zshrc.base /home/pi/.zshrc

CMD ["tau-relay"]
