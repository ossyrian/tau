---
name: index-maintenance
description: Read and maintain tau's directory indexes — the scripts index (~/scripts/INDEX.md) and the workspaces index (/workspace/INDEX.md). Each is a self-maintained catalog of what has accumulated in a tau directory over time. Use to orient before working in scripts or workspaces, and whenever you add, remove, or materially change a script or a workspace. (For the term→referent glossary, see the glossary-maintenance skill instead.)
---

# Index maintenance

tau keeps a running **index** — a plain-Markdown catalog — in each persistent directory that accumulates content over time. You maintain them yourself: read the relevant one to orient, and update it when the directory's contents change. This is how the sandbox remembers what is in it across sessions without you rediscovering it every time.

| Index | File | Catalogs | Surfaced by |
|-------|------|----------|-------------|
| Scripts | `~/scripts/INDEX.md` | reusable shell scripts you can reach for | a pointer line in the system prompt; you read the file on demand (`index-awareness.ts`) |
| Workspaces | `/workspace/INDEX.md` | the project workspaces mounted at `/workspace/<name>` | a pointer line in the system prompt; you read the file on demand (`index-awareness.ts`) |

Each index can grow, so only a pointer to it is in the prompt — **read the file itself when you start working in that directory**, before assuming what is there.

The user's term→referent glossary (`~/.pi/agent/glossary.md`) is a separate thing: it is injected into the prompt in full and maintained via the `glossary-maintenance` skill, not here.

## When to READ an index

- **Scripts** — before writing a new script or a shell one-liner for a recurring task, read `~/scripts/INDEX.md`. If an entry already covers it, read that script and run it instead of reinventing.
- **Workspaces** — when the user says "go to <name>" / "work in <name>", or you need to know what projects exist, read `/workspace/INDEX.md` to see what each workspace is and which kind (live vs copy) it is.

## When to UPDATE an index

Update the relevant index as a side effect of changing the directory. No cron, no periodic sweep — you keep it current because you are the one touching the directory.

- **Scripts** — you added, removed, or materially changed a script in `~/scripts/` → add, drop, or fix its entry in `~/scripts/INDEX.md`.
- **Workspaces** — a workspace appeared, went away, or you learned what an unlabeled one is → update `/workspace/INDEX.md`. (Workspaces are added/removed host-side with `tau workspace add|remove`; you do not create them, but you do keep the index describing them accurate.)

## Entry format

`name → what it is → when to reach for it`. One line each. Enough for a future session to decide whether to open it, no more.

```markdown
- `rds_connect` — opens a psql shell to a Fabric RDS instance (dev/preprod/prod). Reach for it before hand-rolling a DB connection.
- `fabric_main` (copy) — snapshot of the fabric monorepo. Work here for read-only exploration; edits do not reach the user's real tree.
```

## Mechanics

- Edit the files in place with the `edit` tool; keep the header and existing format intact.
- `~/scripts/` and `/workspace/` (including `/workspace/INDEX.md` at the mount root) are tau-owned persistent staging, safe to write. Do **not** write an `INDEX.md` inside a `--live` workspace's own subdirectory — that lands in the user's real project. The single `/workspace/INDEX.md` at the root is the workspaces index; it never touches a source tree.
- The `index-awareness.ts` extension seeds an empty `INDEX.md` stub (header + format) into `~/scripts/` and `/workspace/` when one is missing, so there is always a file to append to.
- Index files can be re-read any time in the same session; edits are effective immediately.
