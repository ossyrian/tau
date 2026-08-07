/**
 * Glossary awareness: inject the user's terms of art into the system prompt.
 *
 * Reads ~/.pi/agent/glossary.md (override with PI_GLOSSARY_FILE) at session
 * start and appends it under a "## Glossary" section, so ambiguous phrases
 * like "the fabric repo" resolve to their concrete referents without asking.
 *
 * The file is re-read at each session start; mid-session edits (e.g. the
 * agent adding an entry on request) land in the next session.
 */

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const GLOSSARY_FILE =
	process.env.PI_GLOSSARY_FILE || path.join(os.homedir(), ".pi", "agent", "glossary.md");

function readGlossary(): string {
	try {
		return fs.readFileSync(GLOSSARY_FILE, "utf8").trim();
	} catch {
		return "";
	}
}

export default function (pi: ExtensionAPI) {
	let glossary = "";

	pi.on("session_start", () => {
		glossary = readGlossary();
	});

	pi.on("before_agent_start", async (event) => {
		if (!glossary) return;

		return {
			systemPrompt:
				event.systemPrompt +
				`

## Glossary — user's terms of art

When the user uses a term below, act on the concrete referent without asking for clarification. When the user corrects a misresolved reference or asks you to remember what a term means, update ${GLOSSARY_FILE} (add or fix the entry, keep the existing format). Edits take effect next session; resolve from the corrected meaning for the rest of this one.

${glossary}
`,
		};
	});
}
