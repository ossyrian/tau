/**
 * The beacon event stream — the parent's window into a subagent without
 * screen-scraping. The child appends one JSON line per event; the parent reads
 * them to track status, render digests, and match alert patterns.
 *
 * `tool` and `assistant` carry mid-run signal so the parent can watch a long
 * run without pulling the whole transcript: a short tool summary + truncated
 * output snippet, and capped assistant text. `settled.text` stays full and
 * uncapped because it is the final answer the parent explicitly asked for.
 *
 * Text spilling lives here too: when an event's payload is large, we write it
 * to a file under STATE_DIR and hand back a preview + path, keeping the parent's
 * context lean.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { STATE_DIR, ensureStateDir, genId } from "./state.ts";

export type BeaconEventType =
	| "ready"
	| "busy"
	| "settled"
	| "shutdown"
	| "tool"
	| "assistant";

export interface BeaconEvent {
	event: BeaconEventType;
	seq?: number;
	ts: number;
	text?: string;
	/** `tool`: short, human-readable rendering of the call (command, path, query). */
	summary?: string;
	/** `tool`: tool name. */
	toolName?: string;
	/** `tool`: whether the tool result was an error. */
	isError?: boolean;
	/** `tool`: truncated output text, for alert matching and digests. */
	snippet?: string;
}

/** Truncate a string to `n` chars, marking the cut so consumers know it is partial. */
export function cap(s: string, n: number): string {
	if (s.length <= n) return s;
	return s.slice(0, n) + "\n…[truncated]";
}

/** Write `text` to a temp file under STATE_DIR and return the path, or undefined on failure. */
export function spillToFile(text: string, tag: string): string | undefined {
	ensureStateDir();
	const file = path.join(STATE_DIR, `spill-${tag}-${Date.now()}-${genId()}.txt`);
	try {
		fs.writeFileSync(file, text, { encoding: "utf8" });
		return file;
	} catch {
		return undefined;
	}
}

/**
 * Keep the parent's context lean: if `text` fits within `n` chars, return it as-is.
 * Otherwise write the full text to a temp file under STATE_DIR and return a preview
 * plus the path, so the parent can grep the file when it actually needs the rest
 * instead of carrying the whole payload in-context.
 */
export function spill(text: string, n: number, tag: string): string {
	if (text.length <= n) return text;
	const file = spillToFile(text, tag);
	if (!file) return cap(text, n);
	return `${text.slice(0, n)}\n…[truncated — ${text.length} chars total; full output: ${file}]`;
}

export function beaconPath(id: string): string {
	return path.join(STATE_DIR, `beacon-${id}.jsonl`);
}

export function readBeaconEvents(id: string): BeaconEvent[] {
	let raw: string;
	try {
		raw = fs.readFileSync(beaconPath(id), "utf8");
	} catch {
		return [];
	}
	const events: BeaconEvent[] = [];
	for (const line of raw.split("\n")) {
		const t = line.trim();
		if (!t) continue;
		try {
			events.push(JSON.parse(t) as BeaconEvent);
		} catch {
			/* ignore partial/corrupt trailing lines */
		}
	}
	return events;
}

export function appendBeacon(id: string, ev: BeaconEvent): void {
	ensureStateDir();
	fs.appendFileSync(beaconPath(id), JSON.stringify(ev) + "\n", { encoding: "utf8" });
}

export function removeBeacon(id: string): void {
	try {
		fs.unlinkSync(beaconPath(id));
	} catch {
		/* ignore */
	}
}
