---
name: tau-share
description: Give a file to the user. Use when the user asks for a file, report, export or other output ("give me", "share", "send me", "export"), or when you make something they need on their own machine.
---

# Share files with the user

Put files in `~/share`. The user sees them on their machine at once.

- `share_file(source, dest?, overwrite?)` copies a file or directory into `~/share`.
- `share_list()` shows what is in `~/share`.

Do not use git in `~/share`. To ship code changes, see `tau-git-shipping`.
