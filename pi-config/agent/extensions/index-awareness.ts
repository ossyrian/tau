/**
 * Index awareness: point the agent at tau's running indexes, and seed them.
 *
 * tau keeps a self-maintained INDEX.md in each persistent, live-mounted
 * directory that accumulates content over time — ~/scripts/ and /workspace/.
 * Each INDEX.md is a curated catalog ("name — what it is — when to reach for
 * it") the agent reads on demand and updates as a side effect of changing the
 * directory. See the `index-maintenance` skill for the format and the rules.
 *
 * This extension does two small things at session start:
 *   1. Seeds an INDEX.md stub (header + format) into any configured directory
 *      that exists but has none, so there is always a file to append to.
 *   2. Appends one pointer line per present index to the system prompt — enough
 *      for the agent to know the index exists and read it, without paying to
 *      inject its (potentially large) contents into every session.
 *
 * The glossary is handled separately by glossary-awareness.ts: it is small and
 * always relevant, so its whole content rides in the prompt.
 */

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

interface Surface {
	/** Directory whose INDEX.md is the running catalog. */
	dir: string;
	/** How to describe the directory in the seeded stub + the prompt pointer. */
	label: string;
	/** One line telling the agent what the index is and when to read it. */
	pointer: string;
}

const SCRIPTS_DIR = process.env.PI_SCRIPTS_DIR || path.join(os.homedir(), "scripts");
const WORKSPACE_DIR = process.env.PI_WORKSPACE_DIR || "/workspace";
const INDEX_FILE = "INDEX.md";

const SURFACES: Surface[] = [
	{
		dir: SCRIPTS_DIR,
		label: "reusable shell scripts",
		pointer: `Reusable shell scripts live in ${SCRIPTS_DIR}. Read ${path.join(SCRIPTS_DIR, INDEX_FILE)} before writing a script for a recurring task — an entry may already cover it.`,
	},
	{
		dir: WORKSPACE_DIR,
		label: "project workspaces",
		pointer: `Project workspaces are mounted at ${WORKSPACE_DIR}/<name>. Read ${path.join(WORKSPACE_DIR, INDEX_FILE)} to see what each workspace is (and whether it is a live bind or a copy).`,
	},
];

/** The stub written when a directory has no INDEX.md yet. */
function stub(label: string): string {
	return `# Index — ${label}

Running catalog of what has accumulated in this directory. One entry per item,
maintained by the agent (see the \`index-maintenance\` skill). Format:

- \`name\` — what it is — when to reach for it

`;
}

/** True once the directory exists and has an INDEX.md (seeding it if absent). */
function ensureIndex(surface: Surface): boolean {
	if (!fs.existsSync(surface.dir)) return false;
	const indexPath = path.join(surface.dir, INDEX_FILE);
	if (!fs.existsSync(indexPath)) {
		try {
			fs.writeFileSync(indexPath, stub(surface.label), { flag: "wx" });
		} catch {
			// Racing session or read-only mount — a missing stub is not fatal.
			return fs.existsSync(indexPath);
		}
	}
	return true;
}

export default function (pi: ExtensionAPI) {
	let pointers: string[] = [];

	pi.on("session_start", () => {
		pointers = SURFACES.filter(ensureIndex).map((s) => `- ${s.pointer}`);
	});

	pi.on("before_agent_start", async (event) => {
		if (pointers.length === 0) return;

		return {
			systemPrompt:
				event.systemPrompt +
				`

## Indexes

tau keeps a running \`INDEX.md\` in each directory below — a catalog of what has accumulated there over time. Read the relevant one to orient, and update it when you change the directory's contents. Details: the \`index-maintenance\` skill.

${pointers.join("\n")}
`,
		};
	});
}
