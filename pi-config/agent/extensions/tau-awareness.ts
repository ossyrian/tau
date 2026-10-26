/**
 * Tau awareness: the standard tau runtime block for the system prompt.
 *
 * Every pi session in the container learns what tau is, what persists, and
 * which skills hold the details. This ships with the tau repo — user-specific
 * standing instructions belong in ~/.pi/agent/APPEND_SYSTEM.md instead.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const TAU_RUNTIME_BLOCK = `

# tau runtime

You run inside **tau** — a Docker sandbox on the user's machine, as a non-root user of a **profile** (your name and home are in \`$USER\` and \`$HOME\`).

The container is recreated on restarts: only mounted paths (\`~/.pi/agent/\`, \`/workspace/\`, \`~/scripts/\`) and the host-mounted \`~/.treehouse\` survive. Details live in skills — read them when the task touches their territory:

- **\`tau\`** — index of everything tau-standard: skills, extensions, guards.
- **\`tau-runtime\`** — your own config, skills, and system prompt; what persists vs what is wiped; workspace live-bind vs copy semantics; \`~/scripts/\`; talking to other tmux sessions. Read before modifying yourself or reasoning about persistence.
- **\`tau-git-shipping\`** — how git and GitHub work here and how changes ship (treehouse worktrees, the PR workflow). **Read before any git operation.**
- **\`tau-slack\`** — reading and sending Slack as the user with the \`slack api\` CLI. There is no Slack MCP server and no \`slack_*\` tools; use this skill when any task or skill mentions Slack. Send message bodies as Markdown in the \`markdown_text\` argument, never in \`text\`, and keep the source formatting. **Read before any Slack action.**
- **\`tau-live-debug\`** — impeccable live mode with the user's browser: reachable ports, keeping a poll running, \`live_stop\` to close sessions. Only relevant if the impeccable skill is mounted. **Read before running \`impeccable live\`.**
`;

export default function (pi: ExtensionAPI) {
	pi.on("before_agent_start", async (event) => {
		return { systemPrompt: event.systemPrompt + TAU_RUNTIME_BLOCK };
	});
}
