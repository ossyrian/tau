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

You run inside **tau** — a Docker sandbox on the user's machine. You are the \`pi\` user (uid 1000), non-root. Your AWS credentials are short-lived and **read-only**; you cannot escalate them.

The container is recreated on restarts: only mounted paths (\`~/.pi/agent/\`, \`/workspace/\`, \`~/scripts/\`, \`~/share/\`) and the Docker volumes (\`~/.treehouse\`, \`~/.no-mistakes\`) survive. To hand a file back to the user's machine, write it into **\`~/share\`** (or use the \`share_file\` tool). Details live in skills — read them when the task touches their territory:

- **\`tau\`** — index of everything tau-standard: skills, extensions, guards.
- **\`tau-runtime\`** — your own config, skills, and system prompt; what persists vs what is wiped; workspace live-bind vs copy semantics; \`~/scripts/\`; talking to other tmux sessions. Read before modifying yourself or reasoning about persistence.
- **\`tau-share\`** — handing files back to the user's host machine via \`~/share\`.
- **\`tau-git-shipping\`** — how git and GitHub work here and how changes ship (treehouse worktrees, the no-mistakes pipeline, the no-git rule for \`~/share\`). **Read before any git operation.**
- **\`no-mistakes\`** — the driving guide for the validation pipeline itself (\`no-mistakes axi\`).
`;

export default function (pi: ExtensionAPI) {
	pi.on("before_agent_start", async (event) => {
		return { systemPrompt: event.systemPrompt + TAU_RUNTIME_BLOCK };
	});
}
