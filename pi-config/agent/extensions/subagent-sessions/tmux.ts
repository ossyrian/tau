/**
 * tmux primitives. The parent drives subagents (and now the panel's session
 * switching) entirely through tmux; nothing here knows about subagents or
 * beacons — it is the raw command layer plus session enumeration.
 */

import { execFileSync } from "node:child_process";

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

export interface TmuxSession {
	name: string;
	/** subagent = managed by this extension (sa-* naming); session = a top-level tau session. */
	kind: "subagent" | "session";
	/** True if this is the session the current client is attached to. */
	current: boolean;
	attached: boolean;
	windows: number;
	createdAt: number;
	lastActivity: number;
}

/** Enumerate every tmux session on the server, classified and marked with the current one. */
export function listTmuxSessions(): TmuxSession[] {
	let current = "";
	try {
		current = tmuxSessionName();
	} catch {
		/* not attached / no server */
	}
	let raw: string;
	try {
		raw = tmux([
			"list-sessions",
			"-F",
			"#{session_name}\t#{session_attached}\t#{session_windows}\t#{session_created}\t#{session_activity}",
		]);
	} catch {
		return [];
	}
	const sessions: TmuxSession[] = [];
	for (const line of raw.split("\n")) {
		const t = line.trim();
		if (!t) continue;
		const [name, attached, windows, created, activity] = t.split("\t");
		if (!name) continue;
		sessions.push({
			name,
			kind: name.startsWith("sa-") ? "subagent" : "session",
			current: name === current,
			attached: attached === "1",
			windows: Number(windows) || 1,
			createdAt: Number(created) * 1000 || 0,
			lastActivity: Number(activity) * 1000 || 0,
		});
	}
	return sessions;
}

/**
 * Switch the current tmux client to another session. The pi you are attached to
 * stays alive in the background. Returns false if the session is gone.
 */
export function switchTmuxClient(sessionName: string): boolean {
	if (!sessionExists(sessionName)) return false;
	return tmuxOk(["switch-client", "-t", sessionName]);
}
