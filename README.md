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
- `pi-config/agent/` (tracked subset) — tau-standard Pi functionality that ships with
  the repo: the `tau` index skill, `tau-runtime` and `tau-git-shipping` skills, and
  the standard extensions (`tau-awareness`, `git-guard`, `workspace-tools`,
  `subagent-sessions`, `scripts-awareness`, `glossary-awareness`, `todos`). The
  `tau-awareness` extension injects the runtime block into every session's system
  prompt, so `APPEND_SYSTEM.md` stays purely user territory.

The repo checkout stays clean: all per-user runtime state lives under **`~/.tau/`**
on the host (override with `TAU_HOME`), not next to the CLI. Everything there is the
user's; nothing is tracked by this repo:

- `~/.tau/.env` — external tokens, runtime-injected (`tau env`).
- `~/.tau/.gitconfig` — git identity + GitHub credential helper (`tau gitconfig`), mounted read-only.
- `~/.tau/skills.conf` — host skill dirs mounted into Pi (`tau skills`).
- `~/.tau/scripts/` → `~/scripts` — the agent's own scripts.
- `~/.tau/workspace/` → `/workspace` — project copies and live mounts. Agent output lands here.
- `~/.tau/workspaces.conf` — workspace registry, managed by `tau workspace`.
- `~/.tau/share/` → `~/share` — file dropbox: what the agent writes here appears on the host.
- `~/.tau/pi/` → `~/.pi` — Pi's runtime state and user customization: settings, models,
  sessions, `APPEND_SYSTEM.md`, and the user's own skills/extensions.

On `tau start`, the git-tracked files under `./pi-config/` (the tau-standard skills and
extensions above) are copied into `~/.tau/pi/`, overwriting their runtime copies — so a
`git pull` of this repo ships updated tau-standard functionality. User files in
`~/.tau/pi/` that aren't in the repo are never touched. To promote a skill or extension to
tau-standard: add it under `./pi-config/`, un-ignore it in `.gitignore`, and list it in the
`tau` index skill.

Migrating an older checkout that kept state next to the CLI: `tau migrate` relocates
`.env`, `.gitconfig`, `pi-config/`, `workspace/`, `scripts/`, and the `*.conf` files into
`~/.tau/` (non-destructive; skips anything already present).

## What persists vs what's wiped

The container is recreated on every `tau start` / `stop` / `restart`. Only host-mounted
paths survive; everything else is lost.

| Path | Persists? | Holds |
|------|-----------|-------|
| `~/.tau/pi/` → `~/.pi` | yes | Pi settings, models, extensions, skills |
| `~/.tau/workspace/` → `/workspace` | yes | project copies and live mounts |
| `~/.tau/scripts/` → `~/scripts` | yes | reusable shell scripts |
| `~/.tau/share/` → `~/share` | yes | host↔container file dropbox |
| `tau-treehouse` volume → `~/.treehouse` | yes | treehouse worktree pools |
| `tau-nm-home` volume → `~/.no-mistakes` | yes | no-mistakes state (gate repos, runs, SQLite) |
| container `~/.tau/` (creds) | no — tmpfs | wiped on recreate (this is a container path, not host `~/.tau`) |

The two named Docker volumes are a third persistence class: they survive recreates like
the bind mounts, but live in Docker rather than the host tree (SQLite is unreliable on
virtiofs, and worktree gitdir pointers must stay container-local). `docker volume rm
tau-treehouse tau-nm-home` resets them. A restart kills any in-flight no-mistakes
pipeline run — check `no-mistakes axi status` from `tau shell` before restarting.

## Setup

1. `./build.sh`
2. `tau env` and fill any external tokens you want injected (optional; seeds `~/.tau/.env`).
3. `tau skills` and list host skill dirs to share (optional; seeds `~/.tau/skills.conf`).
4. `tau gitconfig` and set the agent's git identity (optional; seeds `~/.tau/.gitconfig`).
   The template wires GitHub auth to the `GITHUB_TOKEN` from `.env` via a credential
   helper — the token's scope, not the identity, bounds what the agent can reach.
   (These `tau` subcommands create `~/.tau/` and copy from the repo `*.example` templates.)
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

## Sharing files back to the host

The container is isolated, so files the agent creates don't reach your machine on
their own. The one channel out is the **share dropbox**: `~/share` in the container
is bind-mounted from `~/.tau/share` on the host. Anything the agent writes there
appears on your machine immediately.

The `share-tools` extension gives the agent a `share_file` tool (copy a file or
directory into the dropbox, refuses to clobber, reports the host path) and
`share_list`; the `tau-share` skill tells it when to reach for them. For code changes
to a tracked repo it ships a PR through no-mistakes instead — the dropbox is for
deliverables, not for landing commits, and the git-guard blocks git inside `~/share`.

## Git, treehouse, and no-mistakes

The image bundles `gh`, [treehouse](https://github.com/kunchenguid/treehouse) (pooled
git worktrees), and [no-mistakes](https://github.com/kunchenguid/no-mistakes) (a local
gated-push pipeline: review, test, docs, lint, push, PR, CI).

The agent has real git access, authenticated as you: HTTPS + the `GITHUB_TOKEN` PAT
from `.env`, via the credential helper in `.gitconfig`. `gh` reads the same token.
Scope the PAT to the repos the agent should reach (contents:write +
pull_requests:write) and put branch protection on those repos — that's the merge
gate. The agent's standing instructions (`pi-config/agent/APPEND_SYSTEM.md`) tell it
to ship through no-mistakes and stop at the PR; enforcement lives at GitHub, not in
the container.

Per-repo, inside the container: `no-mistakes init` once, then the agent drives runs
via `no-mistakes axi`. `init` installs its skill into Pi's skills dir (the image
symlinks `~/.agents/skills` and `~/.claude/skills` to `~/.pi/agent/skills`), so it
persists in `pi-config/` and Pi picks it up at next session start. The default
pipeline agent is Pi (`agent: pi` seeded in `NM_HOME/config.yaml`).

To watch or steer a run yourself: `tau shell`, `cd` into the repo, `no-mistakes`
(TUI) or `no-mistakes axi status`.

A `git-guard` Pi extension (`pi-config/agent/extensions/git-guard.ts`) backs the
prompt rules with mechanical blocks: no git in `~/share`, no direct pushes past
the gate, no PR merges. Block reasons point the agent at the correct path. It's a
heuristic on bash commands — it bounds accidents, not adversaries; branch
protection is the real backstop. `/git-guard off` disables it for a session (user
only — slash commands are TUI input the agent can't type).

Treehouse pools live under `~/.treehouse` (volume). Its hook config is symlinked to
`pi-config/agent/treehouse/config.toml`, so post-create hooks (e.g. seeding env
files into fresh worktrees) persist and are editable from the host. Pool only off
container-local clones — worktrees of host-side repos have gitdir pointers that
don't resolve in the container.

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
