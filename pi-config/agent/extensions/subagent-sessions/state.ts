/**
 * The one piece genuinely shared by every other module: where this extension
 * keeps its on-disk state, and how it mints subagent ids.
 *
 * The directory is host-mounted so it survives a /reload, and both roles agree
 * on it: the parent sets PI_SUBAGENT_STATE_DIR on the child's tmux env so a
 * subagent writes its beacon to the same place the parent reads it.
 */

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

export const STATE_DIR: string =
	process.env.PI_SUBAGENT_STATE_DIR || path.join(os.homedir(), ".pi", "agent", "subagent-state");

export function ensureStateDir(): void {
	fs.mkdirSync(STATE_DIR, { recursive: true });
}

export function genId(): string {
	return Math.random().toString(16).slice(2, 10).padStart(8, "0");
}
