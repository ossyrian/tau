---
name: glossary-maintenance
description: Add, fix, or remove entries in the user's glossary (~/.pi/agent/glossary.md) — the term→referent mapping injected into every session's system prompt. Use when the user says "remember that X means Y", "add X to the glossary", corrects how you resolved a term ("no, by X I meant Z"), or asks to clean up glossary entries.
---

# Glossary maintenance

The glossary at `~/.pi/agent/glossary.md` maps the user's ambiguous phrases to concrete referents. The `glossary-awareness` extension injects it into the system prompt at session start, so every entry costs context in every session — keep entries few, short, and high-value.

## Entry format

```markdown
- "term" / "alias" → referent — what to do with it
```

Three parts:

1. **Term** — the phrase as the user actually says it, quoted. Add aliases with `/` if the user uses several.
2. **Referent** — the concrete thing: a URL, a path, an ID. Not a description.
3. **Action** — what resolving the term implies. "Prefer the /workspace copy if mounted", "query the prod DB, not dev". Omit if the referent alone is enough.

Good: `- "my_repo" → https://github.com/my-repo — prefer /workspace/my_repo if mounted`
Bad: `- "my_repo" → the main product` (no concrete referent, no action)

## When to edit

- User says "remember that X means Y" / "add X to the glossary" → add an entry.
- User corrects a resolution ("no, by X I meant Z") → fix the existing entry, or add one if missing. Also apply the correction immediately in the current conversation.
- User asks to remove or clean up → delete entries; don't leave tombstones.

Do NOT add entries speculatively. Only the user's explicit ask or correction justifies an edit.

## Mechanics

- Edit `~/.pi/agent/glossary.md` in place with the edit tool.
- Keep the header and format intact; append new entries to the list.
- Edits take effect at the **next session start**. Tell the user this, and honor the new meaning for the rest of the current session yourself.
