---
name: tau-git-shipping
description: How to do git work and ship changes from inside the tau sandbox — cloning, pooled worktrees via treehouse, and pushing a branch and opening a PR. Use when cloning a repo, committing, pushing, opening a PR, or whenever a task ends in shipped code. ALWAYS use before any git push.
---

# Git, GitHub, and shipping work in tau

You have real git access, as the user. Run `git` and `gh` from inside `/workspace` (or your profile's workspace); from anywhere else they refuse. There are no SSH keys in this container.

## Hard rule: no git in `~/share`

`~/share` is for files you share with the user, not a git working tree. **Do not run any git command there.** To version-control code, work in `/workspace`. The git-guard blocks git in `~/share`.

## How work ships

When the user asks you to do work in a git repo, do these setup steps **before writing any code**, in order:

1. **Lease a treehouse worktree.** Don't work directly in the clone. Use **treehouse** for a pooled, disposable working copy: `treehouse get --lease --json` (scripted) or `treehouse` interactively. Pool off a container-local clone in `/workspace/` (clone with plain `git clone https://github.com/org/repo` — auth is automatic; clones outside a mounted path are wiped on restart), never off a repo whose `.git` points at a host path. The pool lives inside the repo as `.treehouse/`; git already ignores it. Hook config lives at `~/.pi/agent/treehouse/config.toml` (persistent).
2. **Ensure pre-commit is available.** If the repo has a `.pre-commit-config.yaml`, activate the hooks in the worktree. First check `pre-commit` is on PATH (`command -v pre-commit`); if it's missing, tell the user. Then run `pre-commit install` in the worktree. If the repo has no `.pre-commit-config.yaml`, skip this — don't add one unless the user asks.

Then do the work and ship it:

3. Commit on a branch, push it to `origin`, and open a PR with `gh pr create`. **That is your stopping point: tell the user the PR is ready. Never merge.**

## The git-guard extension

A `git-guard` extension enforces the rules above mechanically: it blocks git in `~/share` and PR merges (`gh pr merge` / merge via `gh api`). If a command of yours is blocked, the reason tells you the correct path — follow it rather than working around the guard. Only the user can disable the guard (`/git-guard off`, per session).

## What persists

Treehouse pools (`~/.treehouse`) live on a host mount — they survive restarts.
