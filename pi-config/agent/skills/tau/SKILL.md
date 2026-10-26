---
name: tau
description: Index of tau-standard functionality — the skills, extensions, tools, and guards that ship with the tau sandbox itself, as opposed to user customization. Use when you need an overview of what the tau harness provides, where a capability comes from, or which skill covers a runtime topic.
---

# tau — standard functionality index

Everything listed here ships with the tau repo and is present in every tau install. Skills and extensions NOT listed here are the user's own customization.

## Skills

| Skill | Covers |
|-------|--------|
| `tau-share` | Giving files to the user through `~/share`, with the `share_file` / `share_list` tools |
| `tau-runtime` | The sandbox itself: what persists vs is wiped, workspace live-bind vs copy semantics, `~/scripts/`, your own config/skills/system prompt, talking to other tmux sessions |
| `tau-git-shipping` | Git and GitHub in the container: cloning, treehouse worktrees, opening PRs, the `~/share` no-git rule, the git-guard |
| `subagent-sessions` | Delegating work to other pi sessions in this tmux harness |
| `index-maintenance` | Reading and maintaining tau's running indexes: the glossary, `~/scripts/INDEX.md`, and `/workspace/INDEX.md` |
| `tau-slack` | Reading and sending Slack as the user with `slack api` (needs the `slack` CLI). Maps the Slack MCP tool names other skills use to CLI calls, and the rule to send messages as Markdown in `markdown_text` |
| `tau-annotate` | Handling `[annotate]` messages: comments the user made in their browser on an HTML report in `~/share` (`tau annotate`) |
| `tau-live-debug` | (needs the impeccable skill mounted) impeccable live mode (in-browser visual feedback) in the container: reachable ports, keeping a poll running, `live_stop` to close sessions |

## Extensions (`~/.pi/agent/extensions/`)

| Extension | Does |
|-----------|------|
| `tau-awareness.ts` | Appends the tau runtime block (sandbox identity + skill pointers) to every session's system prompt |
| `share-tools/` | `share_file` / `share_list`: copy files into `~/share` for the user |
| `git-guard.ts` | Blocks git in `~/share` and PR merges. `/git-guard` to inspect; only the user can disable |
| `workspace-tools/` | `workspace_list` / `workspace_path` — read-only visibility into `/workspace/<name>` mounts and their kind (live vs copy) |
| `subagent-sessions/` | Spawn and message other pi sessions (`subagent_list`, `subagent_send`, …) |
| `index-awareness.ts` | Seeds an `INDEX.md` stub into `~/scripts/` and `/workspace/` and injects a pointer to each; the agent reads/maintains them (see `index-maintenance`) |
| `glossary-awareness.ts` | Injects `~/.pi/agent/glossary.md` in full into the system prompt |
| `live-bridge/` | (no-op unless the impeccable skill is mounted) Wakes the agent when an impeccable live-mode browser event is queued with no poll running; `live_stop` / `/live-stop` / `/live-status` (and `tau live stop` on the host) close every live debug session |
| `todos.ts` | `write_todos` planning tool |

## Bundled binaries (Docker image)

Always: `treehouse` (pooled worktrees; config at `~/.pi/agent/treehouse/config.toml`), `tmux`, `node`, `npm`. Others depend on the user's setup, so check with `command -v` before relying on one: `git`, `gh`, `rg`, `jq`, `uv`, `python3`, `terraform`, `psql`, Playwright + Chromium, and `md2html` / `ste-lint` (Markdown → one self-contained HTML with GitHub styling, Shiki, and pre-rendered Mermaid, gated by a prose lint).

## Host layout

All per-user host state lives under `~/.tau/` (override with `TAU_HOME`): `.env`, `share/` (↔ container `~/share`), `scripts/` (→ `~/scripts`), `workspace/` (→ `/workspace`), `pi/` (→ `~/.pi`), `config.yaml`, `secrets/`. The tau repo checkout itself holds only the image, the CLI, and tau-standard config — no user state.

## Not standard (user customization)

`APPEND_SYSTEM.md` (user standing instructions), `glossary.md`, `models.json`, `settings.json`, status-line extensions, and any skill not in the table above.
