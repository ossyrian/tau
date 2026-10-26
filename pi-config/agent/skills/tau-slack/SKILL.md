---
name: tau-slack
description: Read and send Slack messages as the user from the tau sandbox with the `slack api` CLI. There is no Slack MCP server here. Use for any Slack task, and whenever a skill names Slack MCP tools (slack_search_users, slack_search_channels, slack_read_channel, slack_read_thread, slack_send_message, slack_send_message_draft). Those tools do not exist; use this skill instead.
---

# tau-slack

Pi has no MCP. `SLACK_USER_TOKEN` is set, so you act as the user. Use `slack api <method>`
for everything. Do not run `slack login`, and ignore `slack --help` (it lists only
app-building commands). Use `--json` for bodies and parse output with `jq`.

If a call returns `"ok": false`, read `error`. For `missing_scope`, tell the user which
scope is missing. Do not look for another route.

## MCP tool to CLI

| If a skill says | Run |
|---|---|
| `slack_search_channels` | `conversations.list types=public_channel,private_channel exclude_archived=true limit=200`, filter `name` with `jq`. Page with `cursor=` from `response_metadata.next_cursor`; a channel can be on page 3 or later. |
| `slack_search_users` | `users.list limit=200`, filter `name`, `real_name`, `profile.display_name`. Page the same way. `users.lookupByEmail` lacks its scope here. |
| `slack_read_channel` | `conversations.history channel=<ID> limit=<n>` (newest first) |
| `slack_read_thread` | `conversations.replies channel=<ID> ts=<thread_ts>` |
| `slack_send_message` | `chat.postMessage` (see Sending) |
| `slack_send_message_draft` | No equivalent. See Drafts. |

## Sending

```
slack api chat.postMessage --json '{"channel":"C0123","markdown_text":"**api** first. See [doc](https://x.com)","thread_ts":"1700000000.000100"}'
slack api chat.update --json '{"channel":"C0123","ts":"1700000000.000100","markdown_text":"..."}'
```

1. Use IDs from `conversations.list` or `users.list`. Never guess one. If two match, ask.
2. Show the user the exact text and target. Wait for a yes. A sent message cannot be unsent.
3. Put the body in `markdown_text` as normal Markdown, and keep its formatting. Never send
   `text` or `blocks` with it (`markdown_text_conflict`). Limit: 12,000 characters.
   Headings all render at one size. Images become links.
4. Mentions work in `markdown_text`: `<@U123>`, `<#C123>`, `<!subteam^S123>`, `<!here>`. A
   bare `@name` does nothing.
5. `chat.postMessage` returns `channel` and `ts`. Keep both to edit your own message later.
   Reply in a thread with the parent's `ts` as `thread_ts`.
6. Do not delete messages or change channels or settings unless the user asks by name.

## Drafts

The API cannot save to *Drafts & Sent*. Show the message in chat. If the user wants it in
Slack, post it to their own DM (`auth.test` gives `user_id`; `conversations.open
users=<user_id>` gives the channel), and say it was sent, not drafted. There is no "Sent
using" footer, so skills that remove one do not apply.

## Token

Only `SLACK_USER_TOKEN` should be set. `SLACK_BOT_TOKEN` takes priority and makes you act
as an app. If `auth.test` shows a bot, tell the user.
