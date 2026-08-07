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
| `subagent_send` | Send a message to a session (by id or label). Waits for the response by default and returns it. |
| `subagent_wait` | Block until the current run finishes; return the latest response. Use after a `wait: false` send. |
| `subagent_read` | Return the latest response without waiting. `raw: true` returns a tmux pane snapshot for debugging. |
| `subagent_list` | List managed sessions with id, label, status, cwd. |
| `subagent_destroy` | Kill a session (kills its tmux session). `all: true` destroys every session. |

`subagent_send` returns the subagent's final response text when `wait: true` (the default). That text is captured from the subagent directly, not screen-scraped, so it is complete even if long.

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
- If a response looks empty or wrong, `subagent_read { target, raw: true }` shows the raw tmux pane so you can see what the subagent actually rendered.

## Caveats

- Subagents share your container: same filesystem, same skills, same extensions, same AWS creds (read-only). They can read and edit files. Treat a subagent like another instance of yourself, not a sandbox.
- Don't have a subagent spawn its own subagents by default; nested delegation is not set up and gets confusing fast.
- Long tasks: the default wait timeout is 3 minutes. Raise `timeoutMs` for big jobs, or send with `wait: false` and poll with `subagent_wait`.
- To watch a subagent live, the user can switch tmux sessions in their terminal (or see them via `tau session list`).
- If a session is reported "gone", its tmux session died (or the harness restarted). Reconcile by calling `subagent_list`, then create a fresh one.
- These tools only work inside the tau tmux harness. Outside tmux they return a clear error and do nothing.
