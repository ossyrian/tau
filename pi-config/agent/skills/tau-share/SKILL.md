---
name: tau-share
description: How to hand files back to the user's host machine from inside the tau sandbox — the ~/share dropbox and the share_file / share_list tools. Use when the user wants a deliverable OUT of the container ("give me", "share this", "hand me the file", "export", "where can I grab it") or when you produce an artifact (report, export, generated file, log bundle) the user needs on their own machine.
---

# tau-share — handing files back to the user

The tau container is isolated: the user's real files aren't reachable unless mounted as workspaces, and nothing you create in `/workspace/*` copies or `/tmp` reaches the user's machine on its own. The **one** sanctioned channel for files out of the sandbox is the share dropbox.

## The dropbox

`~/share` in the container is bind-mounted from `~/.tau/share` on the host. Anything written there appears on the user's machine immediately — no restart, no sync step. That's the whole mechanism.

## Preferred: the share_file tool

```
share_file(source="/workspace/proj/report.md")
        → lands at ~/.tau/share/report.md on the host
share_file(source="./out", dest="exports/run-42")   # directories work
share_file(source="x.csv", dest="data.csv", overwrite=true)
```

It copies a file or directory into the dropbox, refuses to clobber unless `overwrite: true`, keeps everything inside the share dir (no path escapes), and reports the exact host path the user will see. Use `share_list` to confirm a deliverable landed.

Writing directly into `~/share` with the normal write/edit tools also works; `share_file` just makes "give this to the user" one obvious verb and handles directories.

## When to use it vs shipping code

- **A deliverable the user reads/keeps** (report, export, generated artifact, diagnostic bundle) → share it into `~/share`.
- **A change to a git-tracked project** → do NOT drop it in `~/share`. Ship it through the no-mistakes pipeline as a PR (see `tau-git-shipping`). The dropbox is for files, not for landing code changes.

## Hard rule: no git in ~/share

`~/share` is a dropbox, not a working tree. Don't `git init`, clone, or run any git command there — the git-guard blocks it. If you need version control, work in `/workspace`.
