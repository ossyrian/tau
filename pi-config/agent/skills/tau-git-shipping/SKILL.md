---
name: tau-git-shipping
description: How to do git work and ship changes from inside the tau sandbox — cloning with the GITHUB_TOKEN credential helper, pooled worktrees via treehouse, and shipping through the no-mistakes gated pipeline to a PR. Use when cloning a repo, committing, pushing, opening a PR, running validation, or whenever a task ends in shipped code. ALWAYS use before any git push.
---

# Git, GitHub, and shipping work in tau

You have real git access. You commit and push **as the user** — auth is HTTPS via the `GITHUB_TOKEN` env var (a PAT), wired through the credential helper in `~/.gitconfig`. `gh` reads the same token. There are no SSH keys in this container.

## Hard rule: no git in `~/share`

`~/share` is the host file dropbox (see `tau-share`), not a git working tree. **Do not run any git command there** — not `init`, `clone`, `commit`, `add`, or even read-only `status`/`log`. To hand files to the user, use `share_file`; to version-control code, work in `/workspace`. The git-guard blocks git in `~/share`.

## How work ships

1. **Clone** with plain `git clone https://github.com/org/repo` into `/workspace/` (auth is automatic). Clones outside a mounted path are wiped on restart.
2. **Worktrees**: for parallel or disposable working copies, use **treehouse** (`treehouse` — pooled git worktrees; `treehouse get --lease --json` for scripted use). Pool off a container-local clone, never off a repo whose `.git` points at a host path. Hook config lives at `~/.pi/agent/treehouse/config.toml` (persistent).
3. **Ship through no-mistakes**, not by pushing straight to `origin`. In an initialized repo (`no-mistakes init`, once per repo): commit on a branch, then

   ```sh
   no-mistakes axi run --intent "<the user's goal, verbatim and complete>"
   ```

   Answer gates with `no-mistakes axi respond`. The run ends at `outcome: checks-passed` — a PR is open and CI is green. **That is your stopping point: tell the user the PR is ready. Never merge.** The `/no-mistakes` skill has the full driving guide.
4. Direct `git push origin` is for cases where the gate genuinely doesn't apply (e.g. pushing to a scratch repo the user named). Default to the gate for anything that will become a PR.

## Gate etiquette

- At review gates, findings marked `action: ask-user` are for the **user**, not you — relay them and wait unless the user has told you to auto-resolve.
- Do not abort, reset, or re-push a branch while a pipeline run owns it; respond to the gate instead. Fix commits the pipeline made belong to the branch.

## The git-guard extension

A `git-guard` extension enforces the rules above mechanically: it blocks git in `tau_share`, direct `git push` to anything but the `no-mistakes` remote, and PR merges (`gh pr merge` / merge via `gh api`). If a command of yours is blocked, the reason tells you the correct path — follow it rather than working around the guard. For genuinely gate-exempt pushes (e.g. the first push of a brand-new repo), ask the user; only they can disable the guard (`/git-guard off`, per session).

## What persists

Treehouse pools (`~/.treehouse`) and no-mistakes state (`~/.no-mistakes`, aka `NM_HOME`) live on Docker volumes — they survive restarts, but a restart kills any **in-flight** pipeline run (crash recovery marks it failed; re-run validation after).
