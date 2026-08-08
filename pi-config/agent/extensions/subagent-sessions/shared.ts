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

/**
 * Beacon event stream. The child appends one JSON line per event; the parent
 * reads them to track status, render digests, and match alert patterns — never
 * by screen-scraping the pane.
 *
 * `tool` and `assistant` carry mid-run signal so the parent can watch a long
 * run without pulling the whole transcript: a short tool summary + truncated
 * output snippet, and capped assistant text. `settled.text` stays full and
 * uncapped because it is the final answer the parent explicitly asked for.
 */
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

// --- In-memory registry (backed by REGISTRY_FILE; shared so the alert poller
// and the panel UI can read live subagents without importing the manager) ---

const registry = new Map<string, SubagentRecord>();

export function getRegistry(): Map<string, SubagentRecord> {
	return registry;
}

/** Load persisted records into the in-memory map (call once on manager load/reload). */
export function hydrateRegistry(): void {
	for (const r of loadRegistry()) registry.set(r.id, r);
}

export function upsertRecord(r: SubagentRecord): void {
	registry.set(r.id, r);
	saveRegistry([...registry.values()]);
}

export function removeRecord(id: string): void {
	registry.delete(id);
	saveRegistry([...registry.values()]);
}

export function findByIdOrLabel(target: string): SubagentRecord | undefined {
	const lower = target.toLowerCase();
	for (const r of registry.values()) {
		if (r.id.toLowerCase() === lower) return r;
	}
	for (const r of registry.values()) {
		if (r.label && r.label.toLowerCase() === lower) return r;
	}
	return undefined;
}

export function labelOf(r: SubagentRecord): string {
	return r.label ? `${r.label} (${r.id})` : r.id;
}

/** Tear down a subagent: kill its tmux session and drop registry/beacon/scratch state. */
export function destroySubagent(r: SubagentRecord): void {
	try {
		tmux(["kill-session", "-t", r.tmuxSession]);
	} catch {
		/* session may already be gone */
	}
	removeRecord(r.id);
	removeBeacon(r.id);
	cleanupScratchFiles(r.id);
}

/** Drop registry entries whose tmux session is gone, and refresh status from beacons. */
export function reconcile(): void {
	const live: SubagentRecord[] = [];
	for (const r of registry.values()) {
		if (!sessionExists(r.tmuxSession)) {
			removeBeacon(r.id);
			cleanupScratchFiles(r.id);
			continue;
		}
		const evs = readBeaconEvents(r.id);
		const last = evs[evs.length - 1];
		if (last?.event === "shutdown") {
			removeBeacon(r.id);
			cleanupScratchFiles(r.id);
			continue;
		}
		if (last?.event === "ready" || last?.event === "settled") {
			r.status = "idle";
			if (last.event === "settled") {
				r.settledCount = evs.filter((e) => e.event === "settled").length;
				r.lastText = last.text;
			}
		} else if (last?.event === "busy") {
			r.status = "busy";
		}
		live.push(r);
	}
	registry.clear();
	for (const r of live) registry.set(r.id, r);
	saveRegistry(live);
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

// --- Alert patterns (regexes the parent watches subagent output for) ---

export const ALERTS_FILE = path.join(STATE_DIR, "alerts.json");

export interface AlertPattern {
	id: string;
	/** Regex source. */
	pattern: string;
	/** Regex flags (e.g. "i"). */
	flags: string;
	label?: string;
	/** Optional subagent id/label filter; when set, the alert only watches that one. */
	target?: string;
	/** Min gap between firings for the same (pattern, subagent), in ms. */
	cooldownMs: number;
	createdAt: number;
}

const alerts = new Map<string, AlertPattern>();

export function getAlerts(): AlertPattern[] {
	return [...alerts.values()];
}

export function getAlert(id: string): AlertPattern | undefined {
	return alerts.get(id);
}

export function loadAlerts(): void {
	alerts.clear();
	try {
		const raw = fs.readFileSync(ALERTS_FILE, "utf8");
		const data = JSON.parse(raw);
		if (Array.isArray(data)) {
			for (const p of data) {
				if (p && typeof p.id === "string") alerts.set(p.id, p as AlertPattern);
			}
		}
	} catch {
		/* no alerts file yet */
	}
}

export function saveAlerts(): void {
	ensureStateDir();
	fs.writeFileSync(ALERTS_FILE, JSON.stringify([...alerts.values()], null, 2), { encoding: "utf8" });
}

export interface AddAlertInput {
	pattern: string;
	flags?: string;
	label?: string;
	target?: string;
	cooldownMs?: number;
}

/** Validate, store, and persist a new alert pattern. Throws on an invalid regex. */
export function addAlert(input: AddAlertInput): AlertPattern {
	const flags = input.flags ?? "";
	// Compile once to reject invalid patterns before they are stored.
	new RegExp(input.pattern, flags);
	const id = genId();
	const ap: AlertPattern = {
		id,
		pattern: input.pattern,
		flags,
		label: input.label,
		target: input.target,
		cooldownMs: input.cooldownMs ?? 30000,
		createdAt: Date.now(),
	};
	alerts.set(id, ap);
	saveAlerts();
	return ap;
}

export function removeAlert(id: string): boolean {
	const had = alerts.delete(id);
	if (had) saveAlerts();
	return had;
}

export function removeAlertsByPattern(pattern: string): number {
	const ids = [...alerts.values()].filter((a) => a.pattern === pattern).map((a) => a.id);
	for (const id of ids) alerts.delete(id);
	if (ids.length) saveAlerts();
	return ids.length;
}

export function clearAlerts(): number {
	const n = alerts.size;
	alerts.clear();
	saveAlerts();
	return n;
}
