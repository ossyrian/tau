# Agents Guide — Tau

Docker sandbox that runs Pi with short-lived, readonly-scoped AWS credentials and persistent mounts for config, skills, and project copies.

## Architecture

- **Readonly AWS credentials** (short-lived, scoped) — you cannot escalate or reach production
- **Persistent mounts**: `~/.pi/agent/` (config, skills), `/workspace/` (project copies / live mounts), `~/scripts/` (reusable scripts)
- **Ephemeral container**: recreated on restart; tmpfs credentials are wiped

Short-lived creds and ephemeral storage block accidental production writes and credential leaks; the mounts keep the sandbox useful for real dev and debugging work across your services.

## Agent Execution Model

### Starting an Agent Session

```bash
tau session new [NAME]
```

Starts a new Pi conversation in the container. Sessions run in tmux, detach with `Ctrl-b d`, and reattach with `tau session attach <name>`. List all sessions with `tau session list`.

### Agent Configuration

All agent configuration lives under `~/.pi/agent/` (host-mounted, persistent):

| File | Purpose | Reload Timing |
|------|---------|---------------|
| `settings.json` | Pi settings | Session start |
| `models.json` | LLM providers and models | When `/model` invoked |
| `APPEND_SYSTEM.md` | System prompt extension | Next session start |

### Mounting External Skills

Share skills from your local machine via `skills.conf`:

```bash
tau skills
```

This opens `skills.conf` in your editor. List host directories to bind-mount into the container at `~/.pi/agent/skills/`. Changes take effect on the next session start.

## Workspaces: Managing Projects

Tau exposes your projects at `/workspace/<name>` inside the container in one of two modes:

```bash
tau workspace add [DIR] [--name NAME] [--cd] [--live]  # Add (copy by default; --live = shared)
tau workspace list                                     # List all (shows mode: copy/live)
tau workspace cd <name>                                # Switch (needs tau.fish sourced)
tau workspace remove <name>                            # Delete the copy / unregister a live mount
```

**`copy` (default)** — the source is cloned into the live-mounted `workspace/` dir
(filesystem clone where supported, e.g. macOS APFS clonefile — instant and near-zero
space; falls back to a real copy on other filesystems). Visible to the agent immediately, no restart. Edits stay in the
container copy and don't sync back to source. Re-adding merges source changes in via
rsync. The container can't see your original project directories — only the copy.

**`--live`** — your real source is bind-mounted directly at `/workspace/<name>`, so the
agent reads and writes the same tree you do. Bidirectional, no copy, no isolation: agent
edits land on your machine immediately. Because this changes container mounts, it applies
on the next `tau restart` (running sessions are lost) — the same rule as `skills.conf`.
Removing a live workspace unmounts it on the next restart; the source is never deleted.

Use `copy` when the agent should experiment safely on a throwaway snapshot; use `--live`
when you want the agent to place files somewhere accessible on your machine, or to work
against your real tree.

## Credential Scoping and Refresh

### Setup

> AWS access is optional. Skip this section entirely if you don't need AWS inside the container — `tau start` works without `PI_AWS_PROFILE`. The scoping below only matters when you choose to use it.

1. Create an AWS profile that can only assume the readonly role (not the write role):
   ```bash
   aws configure --profile readonly-tau
   ```
2. Verify the profile is truly readonly. If it can assume write, the sandbox is pointless.
3. Log in if SSO:
   ```bash
   aws sso login --profile readonly-tau
   ```

### Credential Flow

The container never holds your AWS credentials directly. Instead, `tau` resolves fresh temp credentials on the host and pipes them into a tmpfs in the container:

```bash
PI_AWS_PROFILE=readonly-tau tau start  # Creds are refreshed into /home/pi/.tau/credentials
# PI_AWS_PROFILE is optional — omit it to run without AWS. Set it at start,
# session, shell, or refresh to push creds into the (running) container.
```

Credentials are short-lived (typically 15 minutes for SSO + assume-role). Refresh manually without restarting:

```bash
tau refresh
```

Running Pi sessions keep working across a refresh.

### IMDS Bypass on EC2

On EC2, the container can reach the host's IAM role via `169.254.169.254` (the metadata service) and bypass scoping. Prevent this by setting the host IMDS hop limit to 1:

```bash
curl -X PUT "http://169.254.169.254/latest/api/token" \
  -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" \
  -H "Token-Required: true"
```

Or block link-local traffic from the container network.

## Container Lifecycle

### Commands

```bash
tau start      # Create and start the container, push fresh creds
tau stop       # Stop (sessions are lost)
tau restart    # Restart (sessions are lost)
tau shell      # Open an interactive zsh shell
tau refresh    # Re-push fresh creds without restart
```

### Persistence Model

| Path | Persists? | Notes |
|------|-----------|-------|
| `~/.pi/agent/` | ✅ Yes | Config, skills, models (host bind-mount) |
| `/workspace/` | ✅ Yes | Project copies and live mounts (host bind-mount) |
| `~/scripts/` | ✅ Yes | Shell scripts (host bind-mount) |
| `~/.treehouse/` | ✅ Yes | Treehouse worktree pools (named Docker volume `tau-treehouse`) |
| `~/.no-mistakes/` | ✅ Yes | no-mistakes state: gate repos, runs, SQLite (named volume `tau-nm-home`) |
| `~/.tau/` | ❌ No | AWS credentials (tmpfs, wiped on recreate) |
| `/tmp/`, `/home/pi` (rest) | ❌ No | Lost on restart |

A restart kills any in-flight no-mistakes pipeline run (crash recovery marks it
failed). Check `no-mistakes axi status` before restarting.

## Git, GitHub, and Shipping Work

The container has real git access, authenticated **as the user**: HTTPS + the
`GITHUB_TOKEN` PAT from `.env`, via the credential helper in the mounted
`~/.gitconfig`. `gh` reads the same token. There are no SSH keys.

The workflow:

1. Clone with plain `git clone https://github.com/org/repo` (auth is automatic).
2. Use **treehouse** for pooled, disposable worktrees — pool off container-local
   clones only. Hook config lives at `~/.pi/agent/treehouse/config.toml`
   (persistent, host-editable at `pi-config/agent/treehouse/`).
3. Ship through **no-mistakes**: `no-mistakes init` once per repo, then
   `no-mistakes axi run --intent "..."` from a branch. The run validates
   (review/test/docs/lint), pushes, opens a PR, and watches CI. The agent stops at
   `outcome: checks-passed` — the user reviews and merges the PR. The `/no-mistakes`
   skill (installed into Pi's skills by `init`) is the driving guide.

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

### Credentials are expired

```bash
tau refresh    # Refresh without restarting
tau restart    # Or restart the container
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

- Projects at `/workspace/<name>` — either copied (cloned from your source) or `--live` (your real source, bind-mounted read/write)
- Files under `~/.pi/agent/`, `~/scripts/`

To work with a project, add it as a workspace:

```bash
tau workspace add ~/path/to/my/project --name myproject
cd /workspace/myproject  # Then work there
```

## Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `PI_AWS_PROFILE` | Readonly AWS profile (optional; set to use AWS inside the container) | `readonly-tau` |
| `PI_CMD` | Command to start a Pi session | `pi` (default) |
| `AWS_REGION` | AWS region for cred refresh | `us-east-1` (default) |
| `PI_SCRIPTS` | Host directory of reusable scripts to mount | `~/scripts` |
| `PI_WORKSPACE` | Host directory for project copies | `./workspace` |
| `PI_CONFIG` | Host directory for Pi config and skills | `./pi-config` |
| `PI_SKILLS_CONF` | Path to skills mount config | `./skills.conf` |
| `PI_WORKSPACES_FILE` | Path to workspaces registry | `./workspaces.conf` |

## Image and Build

The Docker image (`tau:latest`) bundles:

- Debian + core toolchain (git, curl, jq, etc.)
- Pi (the agent binary)
- A tmux config for multi-session support

To rebuild:

```bash
./build.sh
```

To override Pi's install command:

```bash
PI_INSTALL="my_custom_pi_install" ./build.sh
```

## Security Boundaries

**Credentials** are the real boundary. Docker namespaces are a soft boundary (fine for this threat model; not designed to stop kernel exploits). Tau's security comes from:

1. **IAM scoping** — the readonly profile can't escalate to write permissions
2. **Token scope** — external tokens (`.env` values) are scoped and time-limited
3. **Ephemeral storage** — credentials live on tmpfs, wiped on container recreate

**You must set up the IAM side** (if you use `PI_AWS_PROFILE`). If your profile can assume a write role, tau gives you no sandbox guarantee. Omit `PI_AWS_PROFILE` entirely to run without AWS access.

## Quick Reference

```bash
# Start the sandbox (omit PI_AWS_PROFILE to run without AWS)
PI_AWS_PROFILE=readonly-tau tau start

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

# Refresh creds without restart
tau refresh

# Stop everything
tau stop
```
