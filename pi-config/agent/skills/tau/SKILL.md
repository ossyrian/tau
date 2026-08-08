---
name: tau
description: Index of tau-standard functionality — the skills, extensions, tools, and guards that ship with the tau sandbox itself, as opposed to user customization. Use when you need an overview of what the tau harness provides, where a capability comes from, or which skill covers a runtime topic.
---

# tau — standard functionality index

Everything listed here ships with the tau repo and is present in every tau install. Skills and extensions NOT listed here are the user's own customization.

## Skills

| Skill | Covers |
|-------|--------|
| `tau-runtime` | The sandbox itself: what persists vs is wiped, workspace live-bind vs copy semantics, `~/scripts/`, your own config/skills/system prompt, talking to other tmux sessions |
| `tau-share` | Handing files back to the user's host machine via the `~/share` dropbox and the `share_file` / `share_list` tools |
| `tau-git-shipping` | Git and GitHub in the container: cloning with the token credential helper, treehouse worktrees, shipping through the no-mistakes pipeline, the `~/share` no-git rule, the git-guard |
| `subagent-sessions` | Delegating work to other pi sessions in this tmux harness |
| `index-maintenance` | Reading and maintaining tau's running indexes: the glossary, `~/scripts/INDEX.md`, and `/workspace/INDEX.md` |
| `no-mistakes` | Driving the validation pipeline (`no-mistakes axi`). Installed per-container by `no-mistakes init`, not tracked in the repo — if missing, run `no-mistakes init` in any repo |

## Extensions (`~/.pi/agent/extensions/`)

| Extension | Does |
|-----------|------|
| `tau-awareness.ts` | Appends the tau runtime block (sandbox identity + skill pointers) to every session's system prompt |
| `git-guard.ts` | Blocks git in `~/share`, direct pushes past the no-mistakes gate, and PR merges. `/git-guard` to inspect; only the user can disable |
| `share-tools/` | `share_file` / `share_list` — copy deliverables into `~/share` to hand them to the user's host machine |
| `workspace-tools/` | `workspace_list` / `workspace_path` — read-only visibility into `/workspace/<name>` mounts and their kind (live vs copy) |
| `subagent-sessions/` | Spawn and message other pi sessions (`subagent_list`, `subagent_send`, …) |
| `index-awareness.ts` | Seeds an `INDEX.md` stub into `~/scripts/` and `/workspace/` and injects a pointer to each; the agent reads/maintains them (see `index-maintenance`) |
| `glossary-awareness.ts` | Injects `~/.pi/agent/glossary.md` in full into the system prompt |
| `todos.ts` | `write_todos` planning tool |

## Bundled binaries (Docker image)

`git`, `gh`, `treehouse` (pooled worktrees; config at `~/.pi/agent/treehouse/config.toml`), `no-mistakes` (gated-push pipeline; state on the `tau-nm-home` volume), `tmux`, `node`, `uv`, `aws`.

## Host layout

All per-user host state lives under `~/.tau/` (override with `TAU_HOME`): `.env`, `.gitconfig`, `share/` (↔ container `~/share`), `scripts/` (→ `~/scripts`), `workspace/` (→ `/workspace`), `pi/` (→ `~/.pi`), `skills.conf`, `workspaces.conf`. The tau repo checkout itself holds only the image, the CLI, and tau-standard config — no user state.

## Not standard (user customization)

`APPEND_SYSTEM.md` (user standing instructions), `glossary.md`, `models.json`, `settings.json`, status-line extensions, and any skill not in the table above.
