---
name: subagent-sessions
description: Delegate work to other pi sessions running in this same harness (tmux), via the subagent-sessions extension. Use when the user wants to pipe your output to another pi session (e.g. "reformat this text using the voice skill"), spawn a "subagent" to do a task in its own context, keep a persistent helper session around for reuse, or send text to an existing session. Covers choosing which session to use, and persistent vs ephemeral.
---

# Subagent sessions

A **subagent session** is another `pi` process running in its own named tmux session inside this same tmux server (the tau harness). It has its own context window, its own skills, and the same filesystem you have. Because it is its own tmux session (e.g. `sa-voice-65a45018`), it shows up in `tau session list` and the user can watch or kill it from their own shell. The `subagent-sessions` extension gives you tools to create them, send text to them, read their responses, and destroy them.

The user sometimes calls this workflow "subagent". Existing skills (e.g. the voice skill) also use that term.

## Tools provided by the extension

| Tool | What it does |
|------|--------------|
| `subagent_run` | One-shot: create an ephemeral subagent, send one task, wait for the response, destroy it. Returns the response. |
| `subagent_create` | Start a persistent subagent and keep it alive. Returns its id. |
| `subagent_send` | Send a message to a session (by id or label). Waits for the response by default and returns it. `steer: true` interrupts a running subagent instead of waiting for it to idle. |
| `subagent_wait` | Block until the current run finishes; return the latest response. Use after a `wait: false` send. |
| `subagent_read` | Return the latest response without waiting. `raw: true` returns a tmux pane snapshot for debugging. |
| `subagent_list` | List managed sessions with id, label, status, cwd. |
| `subagent_destroy` | Kill a session (kills its tmux session). `all: true` destroys every session. |
| `subagent_alert_add` | Register a regex watched against live subagent output; fires a pointed alert into your context on match with a steer hint. |
| `subagent_alert_remove` | Deregister an alert by id, by pattern, or all. Safe to call while subagents run. |
| `subagent_alert_list` | List registered alerts (id, pattern, flags, label, target, cooldown). |

`subagent_send` returns the subagent's final response text when `wait: true` (the default). That text is the subagent's settled answer, captured directly from its beacon stream — not screen-scraped. A large answer is truncated inline and the full text spilled to a temp file (the reply names the path); read or grep that file when you need the rest, rather than carrying it all in context.

## Piping your own output to a subagent

Capture the text you want processed and pass it as `text`. The extension handles long or multi-line payloads safely (it writes them to a file and tells the subagent to read that file, so newlines and size are not a problem).

Example — reformat your own draft with the voice skill, one-shot:

```
subagent_run {
  text: "Reformat the following text using the voice skill:\n\n<your draft here>",
  label: "voice"
}
```

The subagent is a full pi with the voice skill available, so it loads and applies the skill on its own.

## Persistent vs ephemeral — decide which

- **Ephemeral** (`subagent_run`): the task is a one-off. The session is created, does the work, returns the answer, and is torn down. Default to this for single requests like "reformat this", "summarize this", "review this diff".
- **Persistent** (`subagent_create` + `subagent_send`): you will send more than one message, or the user wants to reuse the same session. Use when the user says "keep", "persistent", "I'll reuse it", "a standing voice editor", or when they name an existing session to send to.

If it is not obvious which, ask the user: "One-shot, or keep this session around for follow-ups?"

A persistent session stays alive in tmux until you `subagent_destroy` it (or the harness restarts, which kills all tmux sessions). Don't leave ephemeral sessions behind — `subagent_run` cleans up automatically; only `keepAlive: true` keeps one.

## Which session to use — the rule

When the user wants to delegate to a subagent:

1. **They name one** (by label, or "use the voice one"): target it. Match label case-insensitively.
2. **They say "fresh", "new", "a new one"**: create one. Ephemeral for one-offs (`subagent_run`), persistent if they want to keep it (`subagent_create`).
3. **They don't specify**: call `subagent_list`.
   - If there are existing **persistent** sessions, **ask the user which one** (list them by label/id) and offer to create a fresh one. Do not silently pick when the user gave no signal — confirm.
   - If none exist, create a fresh one and tell the user you did.

If you call `subagent_send` with no `target`, the extension itself will also prompt the user with a picker when several persistent sessions exist, and will auto-create one when none exist. Rely on that as a fallback, but prefer asking in conversation first so the user's intent is explicit.

## Sending to a persistent session

```
subagent_create { label: "voice", persistent: true }   # once
subagent_send  { target: "voice", text: "Reformat:\n\n<...>" }   # each time
subagent_send  { target: "voice", text: "Now do the next chunk:\n\n<...>" }
subagent_destroy { target: "voice" }   # when truly done
```

`subagent_send` waits for the response by default. For fire-and-forget, pass `wait: false` and later collect with `subagent_wait` / `subagent_read`.

## Reading results back

- `subagent_send` with `wait: true` (default) returns the response directly.
- After a `wait: false` send, call `subagent_wait` (blocks) or `subagent_read` (non-blocking, returns the latest response).
- The default read path is **pointed**, not a screen dump: the extension reconstructs a digest from the subagent's beacon events (tool calls with one-line summaries, assistant text snippets, settled answers) so only signal reaches your context, not rendered UI or long thinking runs.
- `subagent_read { target, raw: true }` is a **debug escape hatch** — it returns the raw tmux pane. Use it only when a digest looks empty or wrong and you need to see what the subagent actually rendered.

## Steering and alerting

Blocking sends (`wait: true`) are the default: you get result causality for free and no re-engagement cost. Two overlays handle the cases blocking can't:

**Steering** — `subagent_send { target, text, steer: true }` interrupts a running subagent mid-run instead of waiting for it to idle, then waits for the *next* settled answer so you still get the corrected result back. Use it to course-correct a subagent you can see is going wrong.

**Alerts** — register regexes that watch live subagent output and fire a pointed notification into your context on match, so you can catch a subagent losing the plot without polling it yourself:

```
subagent_alert_add { pattern: "rate limit|429|too many requests", flags: "i", label: "rate-limited", cooldownMs: 30000 }
subagent_alert_add { pattern: "Permission denied", target: "build-helper" }
subagent_alert_remove { pattern: "rate limit" }   # by source, while it runs
subagent_alert_remove { all: true }
```

- The poller tests only text-bearing events (tool snippets, assistant text, settled text) against your patterns — non-matching output never enters your context.
- Each alert injects a minimal two-line note: which subagent and alert tripped, a short slice of the matched text, and a ready steer command. You set the alert, so it doesn't re-explain — a long match spills to a temp file whose path is in the note (grep it if you need more). Not a transcript.
- Per-(pattern, subagent) `cooldownMs` (default 30000) prevents repeat spam for the same signal.
- Patterns can be added and removed **while subagents run** — they take effect on the next poll tick. Register one, let it run in the background, remove it once it has served its purpose.
- `target` (id or label) scopes an alert to one subagent; omit it to watch all of them.

## The `/panels` viewer

`/panels` (or `ctrl+alt+v`) opens one vim-ish modal that cycles through three panels so you can survey everything at a glance without leaving your session:

- **Plans** — your `write_todos` plans, reconstructed from the session branch (same source as the todos extension). `j/k` move, `g/G` jump to top/bottom.
- **Subagents** — live sessions with status, kind, settled count, cwd, and the last few beacon events for the selection.
- **Alerts** — registered patterns; `d` removes the selected one.

Keys: `h/l` (or `tab`/`shift+tab`) switch panels, `j/k` move within, `g/G` top/bottom, `d` acts on the selection (destroy subagent / remove alert), `r` refreshes, `q`/`Esc` returns to insert mode. A 1s tick keeps Subagents and Alerts current while open.

## Caveats

- Subagents share your container: same filesystem, same skills, same extensions, same AWS creds (read-only). They can read and edit files. Treat a subagent like another instance of yourself, not a sandbox.
- Don't have a subagent spawn its own subagents by default; nested delegation is not set up and gets confusing fast.
- Long tasks: the default wait timeout is 3 minutes. Raise `timeoutMs` for big jobs, or send with `wait: false` and poll with `subagent_wait`.
- To watch a subagent live, the user can switch tmux sessions in their terminal (or see them via `tau session list`).
- If a session is reported "gone", its tmux session died (or the harness restarted). Reconcile by calling `subagent_list`, then create a fresh one.
- These tools only work inside the tau tmux harness. Outside tmux they return a clear error and do nothing.
