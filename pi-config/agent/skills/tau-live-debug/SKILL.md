---
name: tau-live-debug
description: How impeccable live mode (the in-browser element picker / variant overlay, "live debug session") works inside tau — which ports the user's browser can reach, keeping a poll running so browser clicks reach you, what to do when the poll exits with no browser, wrapping elements that have no id or class, a user-facing toolbar guide, and how to close every live session (live_stop). Use whenever you run `impeccable live`, the user wants to give visual feedback from their own browser, or asks to stop/close live or debug sessions.
---

# Live mode in tau

Supplements the impeccable skill's `reference/live.md`; where they conflict about *where things run*, this wins.

## Ports

The user's browser reaches the container only through `$TAU_PORTS` (default `8400-8409`, published to the host's `127.0.0.1`). **Both** the live helper (`serverPort`, auto-picked from 8400) **and** the app server must sit in that range. Serve static files with `python3 -m http.server 8401 --bind 0.0.0.0`; for dev servers, use `--host` and a port in range (`vite --port 8402 --host`). Don't "probe the default dev-server port" if it's outside the range. Tell the user the exact `http://localhost:<port>` to open.

## Never leave a turn without a poll

Events are only delivered while `impeccable live-poll` runs. The `live-bridge` extension covers the gap: when a browser event is queued, nothing is polling, and you are idle, it sends you a `[live-bridge]` message. On that message, run `live-poll` immediately. Still:

- Start the poll as the last thing you do before you'd otherwise end your turn, and restart it after every event/reply (or use `--reply … --then-poll`).
- Prefer `live-poll --reply EVENT_ID done --then-poll` over reply-then-poll.

## `exit` right after starting (no browser)

`live-poll` returns `{"type":"exit"}` after ~8 s when no browser tab is connected. That is **not** the end of the session. Tell the user the URL to open, wait for them to say it's open (or watch `impeccable live-status` → `connectedClients > 0`), then poll again. Only clean up if the user says they're done.

## Wrapping elements with no locator

`live-wrap` needs at least one of `--element-id`, `--classes`, `--query`. Empty strings fail. For an element with neither id nor class use text: `--query "Cross when the" --tag h1 --text "…"`. Better: give the element a class/id up front when you build the page.

## What to tell the user about the toolbar

- **Pick**: crosshair icon in the toolbar → hover an element → click it.
- Choose an action (or type a request, e.g. "Reword this"), press **Go**; variants appear in place.
- Cycle variants with the arrows; **Accept** keeps one, **Discard** reverts.
- **Steer** sends page-level direction with no element.
- The Impeccable mark dims with a pulsing amber dot when nothing is polling — that means you (the agent) need to poll.

## Closing live sessions

Use the **`live_stop`** tool (or the user runs `/live-stop` in pi, or `tau live stop` on the host). It stops every helper in the container, strips the injected `live.js` tag from the project's HTML, and kills stray polls. It does not stop the app's own server (`pkill -f "http.server 8401"` etc.) — stop that separately if you started it. Afterwards grep for leftover `impeccable-variants-start` / `impeccable-carbonize-start` markers. `tau live status` / `/live-status` show what's running. Container restarts also kill everything, but leave the injected tag in the HTML — run `live_stop` first when the project is a live workspace.
