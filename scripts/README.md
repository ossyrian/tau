# scripts/

One part of Pi's philosophy is that it avoids reaching for MCP as the default transport/presentation layer of "deterministic tooling" to an agent. See [this article](https://mariozechner.at/posts/2025-11-02-what-if-you-dont-need-mcp/) for more details.

As such, this directory is live-mounted such that if your agent needs programmatic access to something, you will have written a script to do provide it. Once written, you're free to associate the script with a formal tool by [extending pi](https://pi.dev/docs/latest/extensions#custom-tools).

You may prompt your agent to be aware of this directory in whatever way you see fit - for example, by [extending Pi's system prompt](https://pi.dev/docs/latest/usage#system-prompt-files).
