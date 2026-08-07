/**
 * Scripts awareness: surface ~/scripts/ to the agent at startup.
 *
 * A "## Scripts" section is appended to the system prompt listing each script
 * with a one-line description, so the agent can read and run a fitting script
 * instead of reinventing it.
 *
 * The scripts directory defaults to ~/scripts and can be overridden with
 * PI_SCRIPTS_DIR. If the directory is missing or empty, this extension is quiet.
 */

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

interface ScriptInfo {
	name: string;
	description: string;
}

const SCRIPTS_DIR = process.env.PI_SCRIPTS_DIR || path.join(os.homedir(), "scripts");
const MAX_DESCRIPTION = 100;

function truncate(text: string, max: number): string {
	return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

function escapeRegex(s: string): string {
	return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Strip a leading "<name> — " / "<name>: " style prefix a comment often repeats. */
function stripNamePrefix(text: string, name: string): string {
	const stem = name.replace(/\.[^./]+$/, "");
	for (const leader of [name, stem]) {
		const stripped = text.replace(new RegExp(`^${escapeRegex(leader)}\\s*[—:\\\-]?\\s*`), "");
		if (stripped !== text && stripped.length > 0) return stripped;
	}
	return text;
}

/** First non-shebang `#` comment line, stripped of `#`, leading space, and a redundant filename prefix. */
function describeScript(file: string, name: string): string {
	let content: string;
	try {
		content = fs.readFileSync(file, "utf8");
	} catch {
		return "";
	}

	for (const raw of content.split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "" || line.startsWith("#!")) continue;
		if (line.startsWith("#")) {
			const text = stripNamePrefix(line.replace(/^#+\s*/, "").trim(), name);
			if (text) return truncate(text, MAX_DESCRIPTION);
		} else {
			break; // hit code before any comment
		}
	}
	return "";
}

function listScripts(dir: string): ScriptInfo[] {
	let entries: fs.Dirent[];
	try {
		entries = fs.readdirSync(dir, { withFileTypes: true });
	} catch {
		return [];
	}

	const scripts: ScriptInfo[] = [];
	for (const entry of entries) {
		if (!entry.isFile() || entry.name.startsWith(".")) continue;
		scripts.push({ name: entry.name, description: describeScript(path.join(dir, entry.name), entry.name) });
	}
	scripts.sort((a, b) => a.name.localeCompare(b.name));
	return scripts;
}

export default function (pi: ExtensionAPI) {
	let scripts: ScriptInfo[] = [];

	pi.on("session_start", () => {
		scripts = listScripts(SCRIPTS_DIR);
	});

	pi.on("before_agent_start", async (event) => {
		if (scripts.length === 0) return;

		const lines = scripts.map((s) => `- ${s.name}${s.description ? ` — ${s.description}` : ""}`);

		return {
			systemPrompt:
				event.systemPrompt +
				`

## Scripts

Reusable shell scripts in ${SCRIPTS_DIR}. When a task fits one, read it for full usage and run it rather than reinventing.

${lines.join("\n")}
`,
		};
	});
}
