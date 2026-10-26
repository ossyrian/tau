/**
 * Process-level plumbing for impeccable's live mode (a browser overlay that
 * talks to a small "live-server" helper, which an agent drains with
 * `live-poll`). Shared by the extension (index.ts) and the host-facing CLI
 * (cli.ts, run via `tau live ...`). Erasable TS only: cli.ts runs under plain
 * `node` type stripping.
 */

import { execFile } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

export const IMPECCABLE = path.join(os.homedir(), ".pi/agent/skills/impeccable/scripts/impeccable");

export interface LiveServer {
	pid: number;
	/** Project root: the helper's working directory. */
	root: string;
	port?: number;
}

export interface LiveStatus {
	connectedClients: number;
	agentPolling: boolean;
	pendingEvents: unknown[];
}

function argvOf(pid: string): string[] | undefined {
	try {
		return fs.readFileSync(`/proc/${pid}/cmdline`, "utf8").split("\0").filter(Boolean);
	} catch {
		return undefined;
	}
}

/** pids whose argv is `<...>/impeccable <verb> ...` (the engine execs in place, so argv[0] is the binary). */
function pidsRunning(verb: string): number[] {
	const out: number[] = [];
	let entries: string[] = [];
	try {
		entries = fs.readdirSync("/proc");
	} catch {
		return out;
	}
	for (const e of entries) {
		if (!/^\d+$/.test(e)) continue;
		const argv = argvOf(e);
		if (!argv || path.basename(argv[0] ?? "") !== "impeccable" || argv[1] !== verb) continue;
		out.push(Number(e));
	}
	return out;
}

export function findServers(): LiveServer[] {
	return pidsRunning("live-server").map((pid) => {
		let root = "?";
		let port: number | undefined;
		try {
			root = fs.readlinkSync(`/proc/${pid}/cwd`);
			port = JSON.parse(fs.readFileSync(path.join(root, ".impeccable/live/server.json"), "utf8")).port;
		} catch {
			/* keep what we have */
		}
		return { pid, root, port };
	});
}

export function findPolls(): number[] {
	return pidsRunning("live-poll");
}

function run(args: string[], cwd: string, timeoutMs = 8000): Promise<{ ok: boolean; out: string }> {
	return new Promise((resolve) => {
		execFile(IMPECCABLE, args, { cwd, timeout: timeoutMs }, (error, stdout, stderr) => {
			resolve({ ok: !error, out: `${stdout}${stderr}`.trim() });
		});
	});
}

export async function statusOf(server: LiveServer): Promise<LiveStatus | undefined> {
	const r = await run(["live-status"], server.root);
	try {
		const s = JSON.parse(r.out)?.liveServer;
		if (!s) return undefined;
		return {
			connectedClients: s.connectedClients ?? 0,
			agentPolling: !!s.agentPolling,
			pendingEvents: Array.isArray(s.pendingEvents) ? s.pendingEvents : [],
		};
	} catch {
		return undefined;
	}
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Close every live debug session: each helper is stopped via its own
 * `live-server stop` (which also strips the injected <script> tag from the
 * project's HTML), stray `live-poll` processes are killed, and any helper that
 * survives is killed outright. Returns human-readable lines.
 */
export async function stopAll(opts: { keepInject?: boolean } = {}): Promise<string[]> {
	const lines: string[] = [];
	const servers = findServers();
	for (const s of servers) {
		const args = ["live-server", "stop", ...(opts.keepInject ? ["--keep-inject"] : [])];
		const r = await run(args, s.root);
		if (!r.ok) lines.push(`⚠ ${s.root}: \`live-server stop\` failed (${r.out.split("\n")[0]}); killing pid ${s.pid}`);
		else lines.push(`stopped live helper for ${s.root}${s.port ? ` (port ${s.port})` : ""}`);
	}
	for (const pid of [...findPolls(), ...servers.map((s) => s.pid)]) {
		if (alive(pid)) {
			try {
				process.kill(pid, "SIGTERM");
			} catch {
				/* already gone */
			}
		}
	}
	await sleep(500);
	for (const pid of [...findPolls(), ...findServers().map((s) => s.pid)]) {
		if (alive(pid)) {
			try {
				process.kill(pid, "SIGKILL");
			} catch {
				/* already gone */
			}
			lines.push(`force-killed pid ${pid}`);
		}
	}
	if (servers.length === 0) lines.push("no live helpers were running");
	return lines;
}

export async function describeAll(): Promise<string[]> {
	const servers = findServers();
	if (servers.length === 0) return ["no live helpers running"];
	const polls = findPolls().length;
	const lines: string[] = [];
	for (const s of servers) {
		const st = await statusOf(s);
		lines.push(
			`${s.root} port=${s.port ?? "?"} pid=${s.pid} ` +
				(st
					? `browsers=${st.connectedClients} polling=${st.agentPolling} pending=${st.pendingEvents.length}`
					: "status unavailable"),
		);
	}
	lines.push(`live-poll processes: ${polls}`);
	return lines;
}
