---
name: tau-runtime
description: Use when you need to inspect or change your own configuration, models, skills, or system prompt, to reason about what survives a restart, to work with /workspace/ mounts or ~/scripts/, or to talk to other tmux sessions. Explains the tau sandbox you run in — where your config and skills live, which paths persist vs are wiped, and how to modify yourself safely.
---

# tau runtime

You run inside **tau** — a Docker sandbox on the user's machine. You are the `pi` user (uid 1000), non-root. Your AWS credentials are short-lived and **read-only**; you cannot escalate them.

## What persists vs what is wiped

The container is recreated on every `tau start` / `stop` / `restart`. Anything written **outside** a mounted path is lost on recreate. Mounted (persistent) paths:

| Path | Holds | Notes |
|------|-------|-------|
| `~/.pi/agent/` | your config + skills | host-mounted, read-write, survives recreate |
| `/workspace/` | working files; `/workspace/<name>` are either live binds into the user's project or copy mounts (snapshots) | host-mounted (virtiofs), read-write |
| `~/scripts/` | reusable shell scripts you're expected to know about and reach for | host-mounted (virtiofs), read-write, survives recreate |
| `~/.pi/agent/skills/<name>/` (the seeded ones) | skills the user shares from their own machine | edits write back to the user's real files — see warning below |
| `~/.treehouse/` | treehouse worktree pools | Docker volume, survives recreate |
| `~/.no-mistakes/` | no-mistakes state (gate repos, runs, SQLite) | Docker volume, survives recreate |
| `~/.tau/` | your AWS creds | **tmpfs — wiped on recreate.** Never store anything here you need to keep |

Write durable things under `~/.pi/agent/` or `/workspace/`. Nothing else is safe across a restart.

## Workspaces

The user's projects are mounted at `/workspace/<name>` (added host-side with `tau workspace add`). A workspace is one of two kinds, and the kind decides whether your edits reach the user's real project:

- **Live bind** — `/workspace/<name>` has its own entry in `/proc/self/mountinfo`, with a root field pointing at the user's real project path on the host. Edits write straight back to the original project.
- **Copy** — `/workspace/<name>` has no mountinfo entry of its own; the files sit in tau's workspace staging dir on the host, surfaced through the parent `/workspace` mount. Edits do NOT reach the original project source.

To tell which, check `/proc/self/mountinfo` for an entry whose mount point is `/workspace/<name>`. The `workspace_list` and `workspace_path` tools report the kind for you. When the user says **"go to the <name> workspace"**, "go to <name>", or "work in <name>", they mean: make `/workspace/<name>` your working directory and do the task there. Use `workspace_list` to see what exists and `workspace_path <name>` to resolve and verify the directory before you `cd` into it.

Never assume a path outside `/workspace/`.

## Scripts (`~/scripts/`)

A host-mounted, persistent directory of reusable shell scripts you're expected to know about and reach for at all times. It survives `tau` restarts. When a task could reuse a script already in there, read it and run it rather than reinventing. You may add new scripts to it when you build something reusable, and edit scripts there when needed.

This is separate from scripts bundled inside a skill (e.g. `~/.pi/agent/skills/<name>/scripts/`); those belong to their skill.

## Your config (all under `~/.pi/agent/`)

| File | What | When it reloads |
|------|------|-----------------|
| `settings.json` | general settings | session start |
| `models.json` | LLM providers + model list | each time `/model` opens |
| `APPEND_SYSTEM.md` | text appended to your system prompt | session start |

To change a model list, edit `models.json` then reopen `/model`. To change your own standing instructions, edit `APPEND_SYSTEM.md` — it takes effect in the **next** session, not the current one.

## Modifying your skills

Skills live in `~/.pi/agent/skills/`. Each is a directory with a `SKILL.md`:

```
~/.pi/agent/skills/<name>/SKILL.md
```

`SKILL.md` frontmatter (both required):

```markdown
---
name: <lowercase-a-z-0-9-hyphens, max 64>
description: <what it does and when to use it, max 1024>
---

# <title>
...instructions; reference scripts with relative paths...
```

To **add** a skill: create `~/.pi/agent/skills/<name>/SKILL.md`. To **edit** one: change its files in place.

The skill list is scanned at **session start**. A skill you create mid-session is not in your context until a **new session** — tell the user to start one to pick it up.

## Warning: some skills are the user's own shared files

Skills you did **not** create may be bind-mounted from the user's personal machine (e.g. their Claude skills). Editing those changes the user's real files, used by their other tools. Do not edit a skill you didn't author unless the user asked. When in doubt, create a **new** skill under `~/.pi/agent/skills/` instead of editing an existing one.

## Talking to other sessions on this harness

Other `pi` sessions run in this same tmux server — not just subagents you spawn. The subagent extension (`subagent_list`, `subagent_send`, …) only tracks sessions you created. Sessions the user spawned (e.g. `main`, `other`) are not registered there, so `subagent_list` will say "No subagent sessions" even though `tmux ls` shows them.

To talk to a session you did not spawn, use raw tmux:
- `tmux ls` — list every session on the harness.
- `tmux send-keys -t <name> "<text>" Enter` — type into its pane.
- `tmux capture-pane -p -t <name>` — read its current pane content back.

Response capture this way is screen-scrape, not the extension's clean channel, so it is coarser and you may need to poll until the pane settles. When the user asks you to talk to another session, use raw tmux unless it is a subagent you spawned.

## Quick self-inspection

```bash
ls ~/.pi/agent/                 # settings.json, models.json, APPEND_SYSTEM.md, skills/
ls ~/.pi/agent/skills/          # your skills
ls ~/scripts/                   # reusable scripts you should know about
cat ~/.pi/agent/models.json     # configured providers + models
mount | grep -E '/workspace|\.pi|/scripts'   # what's host-mounted (persistent)
```
