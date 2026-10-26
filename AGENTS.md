# Agents Guide — Tau

Docker sandbox that runs Pi. The agent holds **no credentials**: credentialed CLIs run as *proxies* in a separate process the agent cannot read. Persistent mounts keep config, skills, and project copies across restarts.

## Architecture

- **No credentials in the agent's reach.** Host environment variables are not passed in. A credentialed CLI (`aws`, `gh`, ...) is a *proxy*: the agent types the command, a shim forwards it to a server that runs as another OS user, and the agent gets the output only.
- **Profiles** are OS users in one container. Each has its own `config.yaml`, `pi/` directory, packages, skills, env, sessions, and (optionally) workspace. Homes are mode 700.
- **Persistent mounts** (host `~/.tau/`, or `~/.tau/profiles/<name>/` for a named profile): `~/.pi/` (config, skills), the workspace, `~/scripts/`, `~/share/`
- **Ephemeral container**: recreated on restart; nothing secret is written to its disk

Ephemeral storage blocks accidental production writes, and keeping credentials out of the agent's reach blocks leaks. The mounts keep the sandbox useful for real dev and debugging work.

## Profiles

```bash
tau profile list                     # list profiles (the default is always first)
tau profile new work                 # create ~/.tau/profiles/work/{config.yaml,pi/}
tau profile workspaces work shared   # share the default's workspace, or: isolated
tau -p work status                   # act as a profile (or TAU_PROFILE=work)
```

The **default** profile uses `~/.tau/` itself and user `pi`. Another profile `NAME` uses `~/.tau/profiles/NAME/` and user `p-NAME`. There is one image and one container, so:

- `as: root` packages are shared: every profile sees the union.
- `as: user` packages, Pi config, skills, env vars, sessions and proxy choices belong to one profile.
- Adding a profile or a root package needs `tau build` then `tau restart`.
- `tau build` covers every profile and refuses while any profile has unresolved drift.

`workspaces.mode` in a profile's `config.yaml` is `isolated` (default: its own workspace under its home) or `shared` (the default profile's workspace, via the `tau-shared` group). A profile with an isolated workspace cannot read the shared one.

## Workspaces

A profile's workspace is the directory the agent works in. It is a bind mount of a host directory, so you and the agent see the same files. The agent sees it at `/workspace`.

```yaml
workspaces:
  mode: isolated              # or shared; see below
  live:                       # real directories mounted into the workspace
    myproject: ~/dev/myproject
```

```bash
tau workspace add [DIR] [--name NAME] [--live] [--cd]   # DIR defaults to the current directory
tau workspace list
tau workspace rm NAME
tau workspace cd [NAME]       # prints the directory on this machine (tau.fish / tau.bash cd to it)
```

Each entry is one of two kinds:

- **Copy** (the default). The directory is cloned into the workspace (a filesystem clone on macOS APFS: instant, almost no space). The agent edits the copy and your original is never changed. A copy shows in the running container at once. Adding the same directory again merges your changes in with rsync and keeps files only the copy has. `rm` deletes the copy and everything the agent changed in it.
- **Live** (`--live`). Your real directory is bind-mounted at `/workspace/NAME`, so the agent can edit or delete your files. It is recorded under `workspaces.live` in `config.yaml`. Docker cannot change a running container's mounts, so `add --live` and `rm` of a live entry take effect on the next `tau restart` (running sessions are lost). `rm` never touches your directory.

`add` and `cd` print only the host path on stdout. Messages go to stderr, so the shell wrappers and scripts can use the output.

Which workspace a profile gets:

- **`isolated`** (the default for a named profile): its own `workspace/` under its profile directory, mounted in its own mode-700 home. No other profile can read it.
- **`shared`**: the default profile's workspace. Every shared profile and the default profile see the same files. `tau profile workspaces NAME shared|isolated` sets it; it needs `tau build` and `tau restart`.

Each profile has its own `workspaces.live` list. A proxy with `runs_in: agent_directory` runs only from inside the shared workspace, so it runs any hook planted in a live directory mounted there.

## Proxies

A proxy is a credentialed CLI the agent can call but cannot read. The software, the credentials and the process that runs it are all out of the agent's reach.

Define proxies once, in the **default** profile's `config.yaml`. Each profile chooses which it may call. Three fields say where the software is, which file runs, and what the agent types:

| Field | Meaning |
|---|---|
| `package` + `binary` | A **hidden package** from `proxies.packages`, and the file to run inside it (`bin/aws`, relative to the package's `$PREFIX`). |
| `system_binary` | Or: an absolute path to a program already on the system (`/usr/bin/git`). Set this or `package` with `binary`, never both. |
| `agent_command` | The only name the agent sees: the shim it types. Required. |
| `runs_in` | `empty_directory` (default) or `agent_directory`. |
| `env_from` | A command run **on the host** that prints `KEY=VALUE` lines: short-lived credentials (SSO, assume-role). |
| `deny` | Argument sequences the proxy refuses. |

```yaml
proxies:
  packages:                            # hidden installs: the agent cannot run or read them
    aws-cli:
      check: test -x "$PREFIX/bin/aws"
      script: |
        curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/a.zip
        unzip -q /tmp/a.zip -d /tmp && /tmp/aws/install -i "$PREFIX" -b "$PREFIX/bin"
  defs:
    aws-prod:                          # a label for `tau proxy allow`, `env`, `refresh`; the agent never sees it
      package: aws-cli
      binary: bin/aws
      agent_command: aws
      env_from: aws configure export-credentials --profile prod-ro --format env
      deny: [configure export-credentials, --debug]
    aws-dev:
      package: aws-cli                 # same install, its own credentials and process
      binary: bin/aws
      agent_command: aws
      env_from: aws configure export-credentials --profile dev-admin --format env
    git:
      system_binary: /usr/bin/git
      agent_command: git
      runs_in: agent_directory
  active: [aws-dev, git]               # per profile; the default profile here
```

```yaml
# ~/.tau/profiles/prod/config.yaml
proxies:
  active: [aws-prod]                   # only prod's credentials
```

A profile cannot activate two proxies with the same `agent_command`: `tau proxy allow` refuses the second.

```bash
tau proxy list                            # definitions, what the agent types, and whether this profile uses each
tau proxy allow gh / tau proxy disallow gh   # per profile
tau proxy env set aws-prod AWS_REGION eu-west-1
tau proxy env list aws-prod [--show]        # values are masked unless --show
tau proxy refresh [PROXY...]              # mint fresh credentials; all in use if none named
tau proxy reload                          # push stored variables and choices; mints nothing
```

**Credentials and refresh.** `env_from` runs on the host at `tau start` and on `tau proxy refresh`. It never runs on `env set`, `allow`, `disallow` or `reload`: those keep the credentials a server already holds. `tau proxy refresh aws-prod` runs only `aws-prod`'s `env_from` and reloads only its server, so only the profiles that use it see new credentials. If `env_from` fails, every server keeps its old credentials and the error names the proxy. Nothing refreshes on a timer: re-run `refresh` when a credential expires.

**What needs a rebuild.** Only `proxies.packages` and a proxy's `binary` (they install into the image). Adding, removing or changing a proxy, its secrets, `deny`, `env_from` or `allow` needs no rebuild and no restart; `tau proxy reload` applies it.

**Isolation.** Each used proxy has its own OS user (`tx-<name>`, uid 30000-49999), its own server process and its own socket directory. A profile reaches a server only if the kernel says its uid is allowed (`SO_PEERCRED`). Secrets go to a server on stdin through a private pipe, live in that server's memory, and are never written to the container's disk or an argument list. Another uid cannot read a process's `/proc/<pid>/environ` or memory, and `--security-opt no-new-privileges` stops a setuid binary from changing that. So a profile that cannot call `aws-prod` cannot read its credentials even by exploiting another proxied CLI.

Hidden packages install as root into `/opt/tau/proxy-packages/<name>`, mode 750, group `tau-proxies`. Profile users are not in that group, so they can neither run nor read the software. The build fails if a proxy's `binary` is missing after the install. A proxy runs with an empty environment plus its own variables, in a private temp directory.

**What this protects against.** The goal is to keep credentials out of the agent's context and environment. It does not stop an agent that deliberately runs code as the proxy user. For `runs_in: agent_directory`, the proxy runs whatever the agent left in that directory (git hooks, `.git/config` helpers), with the credentials in its environment. Rely on narrowly scoped credentials and on the service (GitHub branch protection, IAM) for the real limits. With `workspaces.mode: shared` between profiles that hold different credentials, those credentials are only as separate as the shared files.

## Container Lifecycle

### Commands

```bash
tau start      # Create and start the container and the proxy servers
tau stop       # Stop (sessions are lost)
tau restart    # Restart (sessions are lost)
tau status     # container, image, packages, skills, profile
tau build      # build the image from every profile's packages
tau            # with -i / on a terminal: the interactive interface
```

### Persistence Model

| Path | Persists? | Notes |
|------|-----------|-------|
| `~/.pi/agent/` | ✅ Yes | Config, skills, models (host `~/.tau/pi`) |
| `/workspace/` | ✅ Yes | Project copies and live mounts (host `~/.tau/workspace`) |
| `~/scripts/` | ✅ Yes | Shell scripts (host `~/.tau/scripts`) |
| `~/share/` | ✅ Yes | File dropbox to the host (host `~/.tau/share`) — the way to hand files back |
| `~/.treehouse/` | ✅ Yes | Treehouse worktree pools (host `~/.tau/treehouse`, `PI_TREEHOUSE`) |
| `/tmp/`, `/home/pi` (rest) | ❌ No | Lost on restart |

All host-side state lives under `~/.tau/` on the user's machine (`~/.tau/pi`,
`~/.tau/workspace`, `~/.tau/scripts`, `~/.tau/share`, `~/.tau/.env`, etc.). The
container-side `~/.tau/` above is a different path (a tmpfs for creds) — don't
conflate them.

To hand a file back to the user's machine, write it into `~/share`. It's the only
file channel out of the sandbox.

## Git, GitHub, and Shipping Work

The agent has real git access, authenticated **as the user**: HTTPS + a
`GITHUB_TOKEN` PAT. `git` and `gh` are proxies with `runs_in: agent_directory`, so they
run only inside the workspace. Their token, identity (`user.name`, `user.email`) and
credential helper live in `~/.tau/secrets/git.env` and `gh.env` as `GITHUB_TOKEN` and
`GIT_CONFIG_*` variables. The container has no `~/.gitconfig` and no token in its
environment. There are no SSH keys. The agent's `umask` is 002, so the proxy users (in the
workspace group) can write what the agent wrote.

The workflow:

1. Clone with plain `git clone https://github.com/org/repo` (auth is automatic).
2. Use **treehouse** for pooled, disposable worktrees — pool off container-local
   clones only. The pool lives in the repo (`root = "."`), so git can reach it. Hook config lives at `~/.pi/agent/treehouse/config.toml`
   (persistent, host-editable at `pi-config/agent/treehouse/`).
3. Commit on a branch, push it, and open a PR with `gh pr create`. The agent stops at
   the open PR — the user reviews and merges it.

Enforcement is at GitHub — PAT scope plus branch protection — not in the container.

## Skills and Extensions

### Creating a Skill

Skills are Markdown files at `~/.pi/agent/skills/<name>/SKILL.md` with required frontmatter:

```markdown
---
name: example-skill
description: What this skill does and when to use it (max 1024 chars).
---

# Example Skill

Instructions. Reference scripts with relative paths:
  ./scripts/my-script.sh
```

To add a new skill:

1. `mkdir -p ~/.pi/agent/skills/<name>`
2. Write `SKILL.md` with frontmatter and instructions
3. Add scripts to a `scripts/` subdirectory if needed
4. Start a new Pi session — skills are scanned at startup

To edit an existing skill, modify its files in place. The agent picks up changes on next session.

### Sharing Skills with Pi

If you use Pi outside tau, share tau skills by listing them in your main Pi `skills.conf`. They appear alongside your IDE skills in both environments.

### Available Skills

Tau bundles no skills. `~/.pi/agent/skills/` holds skills the agent creates
(persistent) plus any mounted via `skills.conf` — look there for what's
available.

## Scripts and Tools

### Host-Mounted Scripts

Shell scripts in `~/scripts/` are bind-mounted and survive restarts. Use them for:

- Common automation (fetching creds, deploying, querying databases)
- Diagnostics (checking cluster health)
- Setup steps that multiple sessions reuse

Example:

```bash
./scripts/my-deployment-helper.sh --env prod --dry-run
```

### Built-In Scripts in Skills

Skills can include scripts in a `scripts/` subdirectory, documented in `SKILL.md` and referenced relative to the skill directory:

```bash
~/.pi/agent/skills/my-skill/scripts/helper.sh
```

## Live Debug Sessions (impeccable, optional)

Requires the [impeccable](https://impeccable.style) skill mounted via `skills.conf`; the
`live-bridge` extension is a no-op without it. `impeccable live` runs a helper in the container that the user's browser talks to
(ports in `PI_PORTS`). The bundled `live-bridge` extension wakes the agent when a
browser event is queued with no poll running. To close every live session:

```bash
tau live status   # helpers, connected browsers, queued events
tau live stop     # stop all helpers, strip injected live.js, kill stray polls
```

Inside pi: the `live_stop` tool (so you can tell the agent "close the debug
sessions") or the `/live-stop` command. See the `tau-live-debug` skill.

## Commenting on Reports

```bash
tau annotate reports/q3.html   # host: open a ~/share HTML file with a comment overlay
```

Comments on selected text or a section arrive as `[annotate] Feedback on ...` messages
in a session. See the `tau-annotate` skill.

## Talking to Other Sessions

Multiple Pi sessions run in the same tmux server.

### Subagent Sessions (Sessions You Spawned)

Use the `subagent-sessions` skill to spawn a subagent:

- `subagent_list` — list subagents you created
- `subagent_send <id> <message>` — send a message

### Other Sessions (Not Spawned by You)

Use raw tmux to interact with user-created sessions (e.g., a `main` session):

```bash
tmux -L tau ls                           # list all sessions
tmux -L tau send-keys -t <name> "<text>" Enter   # type into a pane
tmux -L tau capture-pane -p -t <name>   # read the pane
```

## Troubleshooting

### Container won't start

```bash
docker ps -a | grep tau           # Check if it exists
docker rm tau                     # Remove if in bad state
tau start                         # Retry
```


### A workspace isn't visible in the container

```bash
tau workspace list                              # List configured
tau workspace add /path/to/project --name myp   # Add if missing
tau shell; ls /workspace/                       # Verify mounted
```

### A skill doesn't load in a session

Skills are scanned at session start. If you created a skill mid-session, end it and start a new one.

### Can't access a source file in the container

The container only has access to:

- Projects at `/workspace/<name>`: a copy of your source, or a `--live` mount of your real source (read/write)
- Files under `~/.pi/agent/`, `~/scripts/`

To work with a project, add it as a workspace:

```bash
tau workspace add ~/path/to/my/project --name myproject
cd /workspace/myproject  # Then work there
```

## Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `TAU_PROFILE` | Profile to act as (also `-p NAME`) | `work` |
| `TAU_CONTAINER` | Container name | `tau` (default) |
| `TAU_IMAGE` | Image tag | `tau:latest` (default) |
| `PI_CMD` | Command to start a Pi session | `pi` (default) |
| `TAU_HOME` | Host state root for all per-user files | `~/.tau` (default) |
| `PI_PORTS` | Container ports published to host `127.0.0.1` and relayed from loopback (`tau-relay`) | `8400-8409` (default) |

Each profile keeps its files in its own directory: `~/.tau` for the default profile, `~/.tau/profiles/NAME` for another. The Go `tau` has no per-path overrides: set `TAU_HOME` to move everything.

## Image and Build

The Docker image (`tau:latest`) bundles:

- Debian + core toolchain (git, curl, jq, etc.)
- Pi (the agent binary)
- A tmux config for multi-session support

To rebuild:

```bash
tau build
```

The image holds only what Tau needs: zsh, tmux, curl, socat, Node, Pi, treehouse and `tau-proxy`. Everything else (git, gh, ripgrep, jq, Python, uv, terraform, Playwright, md2html, and so on) is a package in `config.yaml`.

`md2html` is a package like the others. The Dockerfile only copies its source to `/opt/tau/tools/md2html`, because a package script cannot read the build context.

## Security Boundaries

**Credentials** are the real boundary. Docker namespaces are a soft boundary (fine for this threat model; not designed to stop kernel exploits). Tau's security comes from:

1. **IAM scoping** — the readonly profile can't escalate to write permissions
2. **Proxies** — credentials live in memory of a separate OS user; the agent gets output only
3. **Ephemeral storage** — credentials live on tmpfs, wiped on container recreate

**Scope the credentials you give a proxy.** A proxy keeps the agent from reading them, not from using them: if the profile behind `env_from` can write to production, so can the agent.

## Quick Reference

```bash
# Start the sandbox
tau start

# Start a Pi session inside
tau session new

# Detach from a session
# Ctrl-b d

# List running sessions
tau session list

# Reattach to a session
tau session attach <name>

# Add a project
tau workspace add ~/dev/myproject --name myproject

# Open a shell in the container
tau shell


# Stop everything
tau stop
```
