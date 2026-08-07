/**
 * Shared helpers for the subagent-sessions extension.
 *
 * Two roles share this module:
 *  - The parent (the pi you are talking to) registers the management tools.
 *  - Each subagent (a child pi in its own tmux session) registers only the beacon hooks.
 *
 * The role is chosen in index.ts via the PI_SUBAGENT_ID env var, which the parent
 * sets on `tmux new-session -e` when it spawns a subagent. The same env also points
 * the child at this state dir so parent and child agree on beacon/registry paths.
 */

import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

/** Where registry + per-subagent beacon/input files live. Host-mounted, survives /reload. */
export const STATE_DIR: string =
	process.env.PI_SUBAGENT_STATE_DIR || path.join(os.homedir(), ".pi", "agent", "subagent-state");

export const REGISTRY_FILE = path.join(STATE_DIR, "registry.json");

export function ensureStateDir(): void {
	fs.mkdirSync(STATE_DIR, { recursive: true });
}

export function genId(): string {
	return Math.random().toString(16).slice(2, 10).padStart(8, "0");
}

/** Shell-quote a single arg so it is safe to embed in `sh -c "<cmd>"`. */
export function shellQuote(s: string): string {
	if (s === "") return "''";
	if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s;
	return "'" + s.replace(/'/g, "'\\''") + "'";
}

export function isTmuxAvailable(): boolean {
	return !!process.env.TMUX;
}

export class TmuxError extends Error {
	readonly exitCode: number;
	readonly stderr: string;
	constructor(message: string, exitCode: number, stderr: string) {
		super(message);
		this.name = "TmuxError";
		this.exitCode = exitCode;
		this.stderr = stderr;
	}
}

/** Run a tmux command and return trimmed stdout. Throws TmuxError on non-zero exit. */
export function tmux(args: string[], opts: { timeout?: number } = {}): string {
	try {
		const out = execFileSync("tmux", args, {
			encoding: "utf8",
			timeout: opts.timeout ?? 15000,
			stdio: ["ignore", "pipe", "pipe"],
		});
		return (out ?? "").toString().trim();
	} catch (e: any) {
		const stderr = ((e.stderr ?? "") + "").toString().trim();
		const code = e.status ?? -1;
		if (e.code === "ENOENT") {
			throw new TmuxError("tmux binary not found on PATH", -1, "");
		}
		throw new TmuxError(`tmux ${args.join(" ")} failed: ${stderr || e.message}`, code, stderr);
	}
}

/** Run a tmux command, returning true on exit 0, false otherwise. */
export function tmuxOk(args: string[], opts: { timeout?: number } = {}): boolean {
	try {
		execFileSync("tmux", args, {
			encoding: "utf8",
			timeout: opts.timeout ?? 15000,
			stdio: ["ignore", "pipe", "pipe"],
		});
		return true;
	} catch {
		return false;
	}
}

export function tmuxSessionName(): string {
	return tmux(["display", "-p", "#{session_name}"]);
}

export function paneExists(paneId: string): boolean {
	return tmuxOk(["display", "-t", paneId, "-p", "#{pane_id}"]);
}

/** True if a tmux session with the given name exists. */
export function sessionExists(name: string): boolean {
	return tmuxOk(["has-session", "-t", name]);
}

/** Sanitize a label into a tmux-safe session-name fragment (no `.` or `:`, ascii-only). */
export function sanitizeTmuxName(s: string): string {
	const cleaned = s.trim().replace(/[^A-Za-z0-9_-]+/g, "-").replace(/^[-_]+|[-_]+$/g, "");
	return cleaned || "subagent";
}

// --- Beacon (the child writes, the parent reads) ---

export type BeaconEventType = "ready" | "busy" | "settled" | "shutdown";
export interface BeaconEvent {
	event: BeaconEventType;
	seq?: number;
	ts: number;
	text?: string;
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

// --- Registry (the parent's book of live subagents) ---

export type SubagentStatus = "starting" | "idle" | "busy" | "dead";

export interface SubagentRecord {
	id: string;
	label?: string;
	model?: string;
	cwd: string;
	persistent: boolean;
	paneId: string;
	tmuxSession: string;
	windowName: string;
	createdAt: number;
	settledCount: number;
	lastText?: string;
	status: SubagentStatus;
}

export function loadRegistry(): SubagentRecord[] {
	try {
		const raw = fs.readFileSync(REGISTRY_FILE, "utf8");
		const data = JSON.parse(raw);
		return Array.isArray(data) ? (data as SubagentRecord[]) : [];
	} catch {
		return [];
	}
}

export function saveRegistry(records: SubagentRecord[]): void {
	ensureStateDir();
	fs.writeFileSync(REGISTRY_FILE, JSON.stringify(records, null, 2), { encoding: "utf8" });
}

export function inputPayloadPath(id: string, nonce: number): string {
	return path.join(STATE_DIR, `input-${id}-${nonce}.md`);
}

export function systemPromptPath(id: string): string {
	return path.join(STATE_DIR, `systemprompt-${id}.md`);
}

/** Best-effort cleanup of per-subagent scratch files (system prompt, inputs). */
export function cleanupScratchFiles(id: string): void {
	try {
		fs.unlinkSync(systemPromptPath(id));
	} catch {
		/* ignore */
	}
	for (const name of fs.readdirSync(STATE_DIR)) {
		if (name.startsWith(`input-${id}-`)) {
			try {
				fs.unlinkSync(path.join(STATE_DIR, name));
			} catch {
				/* ignore */
			}
		}
	}
}
