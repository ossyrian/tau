# Tau — a Docker sandbox for the Pi Coding Agent

Tau runs Pi in a disposable container with persistent host mounts for its config,
skills, scripts, and project workspaces. The container is recreated on each start;
only the mounted paths survive. That gives the agent a clean, reproducible
environment that can't touch your host, while the things you care about — settings,
skills, work-in-progress — persist across restarts.

Optional: if the agent needs cloud access, tau can push short-lived, readonly-scoped
AWS credentials into the container so it never holds long-lived secrets. See
[AWS credential scoping](#aws-credential-scoping-optional).

Requirements: Docker, bash, rsync (macOS or Linux). AWS CLI v2 only if you use
the optional credential scoping.

## Layout

Tracked:

- `Dockerfile` — Debian base + toolchain + Pi.
- `build.sh` — build the image. `PI_INSTALL=... ./build.sh` overrides Pi's install command.
- `tau` — the host CLI (start/stop/refresh/shell, sessions, workspaces). Bash, no extra deps.
- `tau.fish` / `tau.bash` — shell wrappers to source so `tau workspace cd` / `tau workspace add --cd`
  can change the *current* shell's directory (a child process can't cd its parent). Use the one
  for your shell.
- `*.example` — templates for the per-user files below.

Per-user / runtime, gitignored:

- `.env` — external tokens, runtime-injected.
- `.gitconfig` — git identity for the agent, mounted read-only if present.
- `skills.conf` — host skill dirs mounted into Pi.
- `scripts/` — the agent's own scripts (see `scripts/README.md`).
- `workspace/` — mounted read-write into the container. Agent output lands here.
- `workspaces.conf` — workspace registry, managed by `tau workspace`.
- `pi-config/` — Pi's `~/.pi` (settings, models, extensions, skills, sessions).

## What persists vs what's wiped

The container is recreated on every `tau start` / `stop` / `restart`. Only host-mounted
paths survive; everything else is lost.

| Path | Persists? | Holds |
|------|-----------|-------|
| `pi-config/` → `~/.pi` | yes | Pi settings, models, extensions, skills |
| `workspace/` → `/workspace` | yes | project copies and live mounts |
| `scripts/` → `~/scripts` | yes | reusable shell scripts |
| `~/.tau/` (creds) | no — tmpfs | wiped on recreate |

## Setup

1. `./build.sh`
2. `cp .env.example .env` and fill any external tokens you want injected (optional).
3. `cp skills.conf.example skills.conf` and list host skill dirs to share (optional;
   `tau skills` also seeds this on first edit).
4. `cp .gitconfig.example .gitconfig` and set the agent's git identity (optional).
5. Optionally symlink `tau` onto your PATH — it resolves through symlinks.
6. Start the sandbox and a session:

   ```bash
   tau start
   tau session new
   ```

Detach from a session with `Ctrl-b d` — Pi keeps running. Reattach with
`tau session attach <name>`.

## Shell integration (optional)

So `tau workspace cd <name>` and `tau workspace add <dir> --cd` change your current shell:

```bash
# bash / zsh
echo "source $PWD/tau.bash" >> ~/.bashrc   # or ~/.zshrc

# fish
echo "source $PWD/tau.fish" >> ~/.config/fish/config.fish
```

## AWS credential scoping (optional)

Set `PI_AWS_PROFILE` to give the agent readonly AWS access without holding long-lived
keys. Tau resolves fresh temp creds on the host (SSO / assume-role) and pipes them
into a container-only tmpfs — never onto host disk, never the SSO cache. Re-run
`tau refresh` to push fresh ones without losing running sessions.

```bash
PI_AWS_PROFILE=<readonly-profile> tau start
```

You can set it later too (e.g. `PI_AWS_PROFILE=... tau session new`) to push creds
into an already-running container. Omit it entirely to run without AWS.

**Scope it properly.** Make an AWS profile whose *only* reachable permission is the
readonly role. If it can also assume a write role, the scoping gives you nothing —
Docker namespaces bound what the agent can touch on your host, but they don't bound
what it can do in the cloud. The credential scope is the real cloud-side boundary.

**Instance metadata (EC2).** On EC2 a container can pull the *host's* role creds via
`169.254.169.254` and bypass all scoping. Block link-local traffic from the container
network, or set the host IMDS hop limit to 1:

```bash
curl -X PUT "http://169.254.169.254/latest/api/token" \
  -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" \
  -H "Token-Required: true"
```
