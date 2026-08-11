/**
 * Manager: tools the parent pi uses to create and drive subagent sessions.
 *
 * A "subagent session" is another `pi` process running in its own named tmux
 * session inside this same tmux server (the tau harness). The parent sends text
 * to a subagent by typing into its pane with `tmux send-keys`, and reads
 * responses back from the beacon event stream the subagent writes (see beacon.ts).
 *
 * Each subagent runs in a detached, named tmux session (e.g. `sa-voice-65a45018`)
 * rather than a window in the parent's session, so it shows up in `tau session
 * list` and the user can watch it independently.
 *
 * Two flavors:
 *   - persistent: stays alive across many sends, reused by name. Destroy
 *     explicitly with subagent_destroy.
 *   - ephemeral: subagent_run creates one, sends a single task, captures the
 *     response, and tears it down.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";
import { Type } from "typebox";
import * as fs from "node:fs";
import { STATE_DIR, ensureStateDir, genId } from "./state.ts";
import { type BeaconEvent, cap, readBeaconEvents, removeBeacon, spill } from "./beacon-store.ts";
import {
	type SubagentRecord,
	cleanupScratchFiles,
	destroySubagent,
	findByIdOrLabel,
	getRegistry,
	hydrateRegistry,
	inputPayloadPath,
	labelOf,
	reconcile,
	removeRecord,
	systemPromptPath,
	upsertRecord,
} from "./registry.ts";
import {
	type AlertPattern,
	addAlert,
	clearAlerts,
	getAlerts,
	removeAlert,
	removeAlertsByPattern,
} from "./alert-store.ts";
import { isTmuxAvailable, sanitizeTmuxName, sessionExists, shellQuote, tmux } from "./tmux.ts";
import { forgetSubagent } from "./alerts.ts";
import { registerPanel } from "./panel.ts";

// The live subagent registry lives in registry.ts so the alert poller and panel
// UI can read it without importing the manager. `registry` here is a direct
// reference to that in-memory map.
const registry = getRegistry();

// per-subagent async lock so concurrent tool calls on the same session serialize
class KeyedLock {
	private tails = new Map<string, Promise<unknown>>();
	async run<T>(key: string, fn: () => Promise<T>): Promise<T> {
		const prev = this.tails.get(key) ?? Promise.resolve();
		let release!: () => void;
		const gate = new Promise<void>((res) => (release = res));
		this.tails.set(key, prev.then(() => gate));
		await prev;
		try {
			return await fn();
		} finally {
			release();
		}
	}
}
const lock = new KeyedLock();

// --- polling helpers ---

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve) => {
		const t = setTimeout(resolve, ms);
		signal?.addEventListener(
			"abort",
			() => {
				clearTimeout(t);
				resolve();
			},
			{ once: true },
		);
	});
}

async function waitForBeacon(
	id: string,
	predicate: (evs: BeaconEvent[]) => boolean,
	timeoutMs: number,
	signal?: AbortSignal,
): Promise<boolean> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		if (signal?.aborted) return false;
		if (predicate(readBeaconEvents(id))) return true;
		if (Date.now() >= deadline) return false;
		await sleep(120, signal);
	}
}

type IdleResult = "idle" | "dead" | "timeout";

async function waitForIdle(r: SubagentRecord, timeoutMs: number, signal?: AbortSignal): Promise<IdleResult> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		if (signal?.aborted) return "timeout";
		if (!sessionExists(r.tmuxSession)) return "dead";
		const evs = readBeaconEvents(r.id);
		const last = evs[evs.length - 1];
		if (last?.event === "shutdown") return "dead";
		if (last && (last.event === "ready" || last.event === "settled")) return "idle";
		if (Date.now() >= deadline) return "timeout";
		await sleep(120, signal);
	}
}

// --- session lifecycle ---

interface CreateOptions {
	label?: string;
	model?: string;
	systemPrompt?: string;
	tools?: string;
	skills?: string[];
	cwd?: string;
	persistent?: boolean;
	saveSession?: boolean;
	extraArgs?: string[];
}

async function createSession(opts: CreateOptions): Promise<SubagentRecord> {
	if (!isTmuxAvailable()) {
		throw new Error("Not running inside tmux. Subagent sessions need the tau tmux server.");
	}
	ensureStateDir();
	const id = genId();
	const cwd = opts.cwd || process.cwd();
	const persistent = opts.persistent ?? true;
	const saveSession = opts.saveSession ?? false;
	const labelSlug = opts.label ? sanitizeTmuxName(opts.label) : "";
	const sessionName = labelSlug ? `sa-${labelSlug}-${id}` : `sa-${id}`;
	const windowName = labelSlug || `sa-${id}`;

	const piArgs: string[] = [];
	if (!saveSession) piArgs.push("--no-session");
	if (opts.label) piArgs.push("--name", opts.label);
	if (opts.model) piArgs.push("--model", opts.model);
	if (opts.tools) piArgs.push("--tools", opts.tools);
	if (opts.systemPrompt) {
		const sp = systemPromptPath(id);
		fs.writeFileSync(sp, opts.systemPrompt, { encoding: "utf8", mode: 0o600 });
		piArgs.push("--append-system-prompt", sp);
	}
	if (Array.isArray(opts.skills)) {
		for (const s of opts.skills) {
			if (fs.existsSync(s)) piArgs.push("--skill", s);
		}
	}
	if (Array.isArray(opts.extraArgs)) piArgs.push(...opts.extraArgs);

	const cmd = ["pi", ...piArgs].map(shellQuote).join(" ");

	const paneId = tmux([
		"new-session",
		"-d",
		"-s",
		sessionName,
		"-e",
		`PI_SUBAGENT_ID=${id}`,
		"-e",
		`PI_SUBAGENT_STATE_DIR=${STATE_DIR}`,
		"-c",
		cwd,
		"-n",
		windowName,
		"-P",
		"-F",
		"#{pane_id}",
		cmd,
	]);

	if (opts.label) {
		try {
			tmux(["select-pane", "-t", paneId, "-T", opts.label]);
		} catch {
			/* non-fatal */
		}
	}

	const record: SubagentRecord = {
		id,
		label: opts.label,
		model: opts.model,
		cwd,
		persistent,
		paneId,
		tmuxSession: sessionName,
		windowName,
		createdAt: Date.now(),
		settledCount: 0,
		status: "starting",
	};
	upsertRecord(record);

	const ready = await waitForBeacon(id, (evs) => evs.some((e) => e.event === "ready"), 30000);
	if (!ready) {
		try {
			tmux(["kill-session", "-t", sessionName]);
		} catch {
			/* ignore */
		}
		removeRecord(id);
		removeBeacon(id);
		cleanupScratchFiles(id);
		throw new Error(
			`Subagent ${id} did not report ready within 30s. Confirm 'pi' starts cleanly in a new tmux session.`,
		);
	}
	record.status = "idle";
	upsertRecord(record);
	return record;
}

/** Type the message line into the subagent's pane and submit with Enter. */
function sendKeysToPane(r: SubagentRecord, text: string): { inputPath?: string } {
	const useFile = /[\n\r\t]/.test(text) || Buffer.byteLength(text, "utf8") > 1000;
	let line: string;
	let inputPath: string | undefined;
	if (useFile) {
		const nonce = Date.now();
		inputPath = inputPayloadPath(r.id, nonce);
		fs.writeFileSync(inputPath, text, { encoding: "utf8", mode: 0o600 });
		line = `Read the file at ${inputPath}. Treat its entire contents as your next user message and respond accordingly.`;
	} else {
		line = text;
	}
	const target = r.paneId;
	// Clear any text sitting in the editor, then type the message, then submit.
	tmux(["send-keys", "-t", target, "C-u"]);
	tmux(["send-keys", "-t", target, "-l", "--", line]);
	tmux(["send-keys", "-t", target, "Enter"]);
	return { inputPath };
}

interface SendResult {
	timedOut: boolean;
	text?: string;
}

async function sendText(
	r: SubagentRecord,
	text: string,
	wait: boolean,
	timeoutMs: number,
	signal?: AbortSignal,
	steer = false,
): Promise<SendResult> {
	// Steering interrupts a running subagent instead of waiting for it to go
	// idle first. We still wait for the *next* settle so the orchestrator gets
	// the steered response back.
	if (!steer) {
		const idle = await waitForIdle(r, 30000, signal);
		if (idle === "dead") throw new Error(`Subagent ${labelOf(r)} is gone.`);
		if (idle === "timeout") throw new Error(`Subagent ${labelOf(r)} still busy after 30s; retry later.`);
	}

	const settledBefore = readBeaconEvents(r.id).filter((e) => e.event === "settled").length;
	const { inputPath } = sendKeysToPane(r, text);

	if (!wait) {
		// best-effort: delete the input scratch file once the subagent has had time to read it
		if (inputPath) setTimeout(() => { try { fs.unlinkSync(inputPath); } catch {} }, 60000);
		r.status = "busy";
		upsertRecord(r);
		return { timedOut: false };
	}

	const ok = await waitForBeacon(
		r.id,
		(evs) => evs.filter((e) => e.event === "settled").length > settledBefore,
		timeoutMs,
		signal,
	);
	if (inputPath) { try { fs.unlinkSync(inputPath); } catch {} }
	const evs = readBeaconEvents(r.id);
	const settled = evs.filter((e) => e.event === "settled");
	const last = settled[settled.length - 1];
	r.settledCount = settled.length;
	r.lastText = last?.text;
	r.status = "idle";
	upsertRecord(r);
	if (!ok) return { timedOut: true, text: last?.text };
	return { timedOut: false, text: last?.text ?? "" };
}

function readOutput(r: SubagentRecord, raw: boolean): string {
	if (raw) {
		try {
			return tmux(["capture-pane", "-t", r.paneId, "-p", "-S", "-500"]);
		} catch {
			return "(unavailable)";
		}
	}
	const evs = readBeaconEvents(r.id);
	const settled = evs.filter((e) => e.event === "settled");
	const last = settled[settled.length - 1];
	if (last?.text) return last.text;
	return renderDigest(r, evs);
}

/** Pointed, context-light summary of recent activity, used when no settled
 * response exists yet — a replacement for screen-scraping the pane. */
function renderDigest(r: SubagentRecord, evs: BeaconEvent[]): string {
	const lines = [`[${labelOf(r)} — ${r.status}] recent activity:`];
	for (const e of evs.slice(-12)) {
		lines.push("  " + digestLine(e));
	}
	return lines.join("\n");
}

function digestLine(e: BeaconEvent): string {
	switch (e.event) {
		case "ready":
			return "ready";
		case "busy":
			return "busy";
		case "settled":
			return `settled: ${cap(e.text ?? "", 300)}`;
		case "assistant":
			return `said: ${cap(e.text ?? "", 300)}`;
		case "tool":
			return `${e.isError ? "✗" : "•"} ${e.toolName}: ${cap(e.summary ?? "", 120)}${e.snippet ? " — " + cap(e.snippet, 160) : ""}`;
		case "shutdown":
			return "shutdown";
		default:
			return e.event;
	}
}

function destroySession(r: SubagentRecord): void {
	destroySubagent(r);
	forgetSubagent(r.id);
}

// --- target resolution (the "which one?" behavior) ---

async function resolveTarget(
	target: string | undefined,
	ctx: any,
	createIfMissing: boolean,
): Promise<SubagentRecord> {
	if (target) {
		const r = findByIdOrLabel(target);
		if (!r) {
			throw new Error(`No subagent session matching "${target}". Call subagent_list to see sessions.`);
		}
		if (!sessionExists(r.tmuxSession)) {
			destroySession(r);
			throw new Error(`Subagent "${target}" is gone. Create a fresh one with subagent_create.`);
		}
		return r;
	}

	reconcile();
	const persistent = [...registry.values()].filter((r) => r.persistent);

	if (persistent.length === 0) {
		if (!createIfMissing) {
			throw new Error("No subagent sessions exist. Create one with subagent_create or use subagent_run.");
		}
		return await createSession({ persistent: true, cwd: ctx.cwd });
	}

	if (persistent.length === 1) {
		return persistent[0];
	}

	if (ctx.hasUI) {
		const options = persistent.map((r) => labelOf(r));
		options.push("Create a fresh one");
		const choice = await ctx.ui.select("Which subagent session?", options);
		if (!choice) throw new Error("Cancelled session selection.");
		if (choice === "Create a fresh one") {
			return await createSession({ persistent: true, cwd: ctx.cwd });
		}
		const matched = persistent.find((r) => labelOf(r) === choice);
		if (matched) return matched;
		throw new Error("Invalid selection.");
	}

	throw new Error(
		`Multiple subagent sessions exist; specify a target. Available: ${persistent.map(labelOf).join(", ")}`,
	);
}

// --- tool result helpers ---

/** Cap for a subagent's final answer before it lands in the parent's context.
 * Generous — the parent asked for this, so keep the body inline until it gets
 * large, then spill the full text to a file and hand back a preview + path. */
const OUTPUT_CAP = 6000;

function ok(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details };
}

/** Return a subagent's output, spilling to a temp file if it's too large to keep
 * inline. Use for every path that hands subagent-produced text back to the parent. */
function okOutput(text: string, details: Record<string, unknown> = {}) {
	return ok(spill(text, OUTPUT_CAP, `out-${String(details.id ?? "x")}`), details);
}
function err(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details, isError: true };
}

// --- tool schemas ---

const CreateParams = Type.Object({
	label: Type.Optional(Type.String({ description: "Human-readable name for the session." })),
	model: Type.Optional(Type.String({ description: "Model pattern or id, e.g. 'anthropic/claude-sonnet-4-5'." })),
	systemPrompt: Type.Optional(
		Type.String({ description: "Extra system-prompt text appended for the subagent." }),
	),
	tools: Type.Optional(Type.String({ description: "Comma-separated tool allowlist passed to --tools." })),
	skills: Type.Optional(
		Type.Array(Type.String(), { description: "Skill file/dir paths to preload via --skill (in addition to auto-discovered ones)." }),
	),
	cwd: Type.Optional(Type.String({ description: "Working directory for the subagent. Defaults to current cwd." })),
	persistent: Type.Optional(
		Type.Boolean({ description: "Keep the session alive for reuse. Default true." }),
	),
	saveSession: Type.Optional(
		Type.Boolean({ description: "Persist the subagent's session file. Default false (in-memory only)." }),
	),
	extraArgs: Type.Optional(
		Type.Array(Type.String(), { description: "Raw extra args forwarded to the subagent pi (shell-quoted)." }),
	),
});

const SendParams = Type.Object({
	target: Type.Optional(
		Type.String({
			description: "id or label of the session to send to. Omit to pick or auto-create (see createIfMissing).",
		}),
	),
	text: Type.String({ description: "The user message to send to the subagent." }),
	wait: Type.Optional(
		Type.Boolean({ description: "Block until the subagent finishes and return its response. Default true." }),
	),
	timeoutMs: Type.Optional(
		Type.Number({ description: "How long to wait for the response. Default 180000 (3 min)." }),
	),
	createIfMissing: Type.Optional(
		Type.Boolean({ description: "If no target given and none exist, create a fresh persistent one. Default true." }),
	),
	steer: Type.Optional(
		Type.Boolean({
			description:
				"Interrupt and steer a running subagent instead of waiting for it to idle first. Use to correct a subagent flagged by an alert that is losing the plot. Default false.",
		}),
	),
});

const WaitParams = Type.Object({
	target: Type.String({ description: "id or label of the session to wait on." }),
	timeoutMs: Type.Optional(Type.Number({ description: "How long to wait. Default 180000 (3 min)." })),
});

const ReadParams = Type.Object({
	target: Type.String({ description: "id or label of the session to read from." }),
	raw: Type.Optional(
		Type.Boolean({ description: "Return a raw tmux pane snapshot instead of the captured response." }),
	),
});

const DestroyParams = Type.Object({
	target: Type.Optional(Type.String({ description: "id or label to destroy. Required unless all=true." })),
	all: Type.Optional(Type.Boolean({ description: "Destroy every managed session." })),
});

const RunParams = Type.Object({
	text: Type.String({ description: "The task/message for the one-shot subagent." }),
	label: Type.Optional(Type.String({ description: "Optional label for the ephemeral session." })),
	model: Type.Optional(Type.String({ description: "Model pattern or id." })),
	systemPrompt: Type.Optional(Type.String({ description: "Extra system-prompt text." })),
	tools: Type.Optional(Type.String({ description: "Comma-separated tool allowlist." })),
	skills: Type.Optional(Type.Array(Type.String(), { description: "Skill paths to preload." })),
	cwd: Type.Optional(Type.String({ description: "Working directory. Defaults to current cwd." })),
	timeoutMs: Type.Optional(Type.Number({ description: "How long to wait. Default 180000 (3 min)." })),
	keepAlive: Type.Optional(
		Type.Boolean({ description: "Keep the session alive after the run instead of destroying it. Default false." }),
	),
});

const AlertAddParams = Type.Object({
	pattern: Type.String({
		description:
			"Regex to watch for in subagent output (tool snippets, assistant text, settled text). Fires an alert into your context on match.",
	}),
	flags: Type.Optional(Type.String({ description: "Regex flags, e.g. 'i'. Default none." })),
	label: Type.Optional(Type.String({ description: "Human-readable name shown in the alert and /panels." })),
	target: Type.Optional(
		Type.String({
			description: "Optional subagent id or label to watch. Omit to watch all subagents." }),
	),
	cooldownMs: Type.Optional(
		Type.Number({
			description: "Min gap between firings for the same subagent, in ms. Default 30000." }),
	),
});

const AlertRemoveParams = Type.Object({
	id: Type.Optional(Type.String({ description: "Alert id returned by subagent_alert_add." })),
	pattern: Type.Optional(
		Type.String({ description: "Remove every alert whose regex source equals this. Use when you don't have the id." }),
	),
	all: Type.Optional(Type.Boolean({ description: "Remove every alert." })),
});

// --- registration ---

export function registerManager(pi: ExtensionAPI): void {
	// load the persisted registry into memory (no tmux calls at load time)
	hydrateRegistry();

	pi.registerTool({
		name: "subagent_create",
		label: "Subagent: create",
		description:
			"Start a new persistent subagent session: another pi in its own named tmux session in this harness. Shows up in `tau session list`. Returns its id. Send to it with subagent_send, read with subagent_read, destroy with subagent_destroy.",
		parameters: CreateParams,
		async execute(_id, params, _signal, _onUpdate, ctx) {
			try {
				const r = await createSession({
					label: params.label,
					model: params.model,
					systemPrompt: params.systemPrompt,
					tools: params.tools,
					skills: params.skills,
					cwd: params.cwd ?? ctx.cwd,
					persistent: params.persistent ?? true,
					saveSession: params.saveSession ?? false,
					extraArgs: params.extraArgs,
				});
				return ok(
					`Created subagent ${labelOf(r)} (persistent=${r.persistent}, cwd=${r.cwd}).`,
					{ id: r.id, label: r.label, persistent: r.persistent, cwd: r.cwd, paneId: r.paneId },
				);
			} catch (e: any) {
				return err(e.message);
			}
		},
		renderCall(args, theme) {
			return new Text(
				theme.fg("toolTitle", theme.bold("subagent_create ")) +
					theme.fg("accent", args.label ? String(args.label) : "(unlabeled)") +
					theme.fg("muted", args.persistent === false ? " [ephemeral]" : " [persistent]"),
				0,
				0,
			);
		},
	});

	pi.registerTool({
		name: "subagent_send",
		label: "Subagent: send",
		description:
			"Send a message to a subagent session (by id or label). By default waits for its response and returns it. If target is omitted, picks an existing persistent session or creates one; when several exist and UI is available, asks the user which.",
		parameters: SendParams,
		async execute(_id, params, signal, _onUpdate, ctx) {
			if (!isTmuxAvailable()) return err("Not running inside tmux.");
			let r: SubagentRecord;
			try {
				r = await resolveTarget(params.target, ctx, params.createIfMissing ?? true);
			} catch (e: any) {
				return err(e.message);
			}
			return await lock.run(r.id, async () => {
				try {
					const wait = params.wait ?? true;
					const res = await sendText(
						r,
						params.text,
						wait,
						params.timeoutMs ?? 180000,
						signal,
						params.steer === true,
					);
					if (!wait) return ok(`Sent to ${labelOf(r)}.`, { id: r.id, label: r.label });
					const text = res.text ?? "(no output)";
					if (res.timedOut) {
						return err(
							`Timed out waiting for ${labelOf(r)}. Partial response:\n\n${spill(text, OUTPUT_CAP, `out-${r.id}`)}`,
							{ id: r.id, label: r.label, timedOut: true },
						);
					}
					return okOutput(text, { id: r.id, label: r.label });
				} catch (e: any) {
					return err(e.message, { id: r.id, label: r.label });
				}
			});
		},
		renderCall(args, theme) {
			const target = args.target ? String(args.target) : "(auto)";
			const preview = args.text ? (args.text.length > 60 ? String(args.text).slice(0, 60) + "..." : String(args.text)) : "...";
			return new Text(
				theme.fg("toolTitle", theme.bold("subagent_send ")) +
					theme.fg("accent", target) +
					theme.fg("muted", args.wait === false ? " [no-wait]" : "") +
					"\n  " +
					theme.fg("dim", preview),
				0,
				0,
			);
		},
	});

	pi.registerTool({
		name: "subagent_wait",
		label: "Subagent: wait",
		description:
			"Block until the subagent finishes its current run, then return its latest response. Use after a no-wait subagent_send.",
		parameters: WaitParams,
		async execute(_id, params, signal, _onUpdate, _ctx) {
			const r = findByIdOrLabel(params.target);
			if (!r) return err(`No subagent session matching "${params.target}".`);
			if (!sessionExists(r.tmuxSession)) { destroySession(r); return err(`Subagent "${params.target}" is gone.`); }
			return await lock.run(r.id, async () => {
				const settledBefore = readBeaconEvents(r.id).filter((e) => e.event === "settled").length;
				const idle = await waitForIdle(r, 1000, signal);
				if (idle === "dead") return err(`Subagent ${labelOf(r)} is gone.`);
				if (idle === "idle" && readBeaconEvents(r.id).filter((e) => e.event === "settled").length === settledBefore) {
					// already idle with no new run to wait for
					return okOutput(readOutput(r, false) ?? "(no output)", { id: r.id, label: r.label });
				}
				const ok_ = await waitForBeacon(
					r.id,
					(evs) => evs.filter((e) => e.event === "settled").length > settledBefore,
					params.timeoutMs ?? 180000,
					signal,
				);
				const evs = readBeaconEvents(r.id);
				const settled = evs.filter((e) => e.event === "settled");
				const last = settled[settled.length - 1];
				r.settledCount = settled.length;
				r.lastText = last?.text;
				r.status = "idle";
				upsertRecord(r);
				if (!ok_) return err(`Timed out waiting for ${labelOf(r)}.`, { id: r.id, timedOut: true });
				return okOutput(last?.text ?? "(no output)", { id: r.id, label: r.label });
			});
		},
	});

	pi.registerTool({
		name: "subagent_read",
		label: "Subagent: read",
		description:
			"Return the subagent's latest response. With raw=true, return a tmux pane snapshot instead (useful for debugging).",
		parameters: ReadParams,
		async execute(_id, params, _signal, _onUpdate, _ctx) {
			const r = findByIdOrLabel(params.target);
			if (!r) return err(`No subagent session matching "${params.target}".`);
			if (!sessionExists(r.tmuxSession)) { destroySession(r); return err(`Subagent "${params.target}" is gone.`); }
			return okOutput(readOutput(r, params.raw === true), { id: r.id, label: r.label });
		},
	});

	pi.registerTool({
		name: "subagent_list",
		label: "Subagent: list",
		description: "List all managed subagent sessions with their id, label, status, and cwd.",
		parameters: Type.Object({}),
		async execute(_id, _params, _signal, _onUpdate, _ctx) {
			if (!isTmuxAvailable()) return ok("Not running inside tmux; no subagent sessions.");
			reconcile();
			if (registry.size === 0) return ok("No subagent sessions.");
			const rows = [...registry.values()]
				.sort((a, b) => a.createdAt - b.createdAt)
				.map((r) => {
					const lbl = r.label ? `${r.label} ` : "";
					return `- ${lbl}(${r.id}) [${r.status}] persistent=${r.persistent} cwd=${r.cwd}`;
				});
			return ok(rows.join("\n"), { sessions: [...registry.values()] });
		},
	});

	pi.registerTool({
		name: "subagent_destroy",
		label: "Subagent: destroy",
		description: "Kill a subagent session (kills its tmux session). With all=true, destroys every managed session.",
		parameters: DestroyParams,
		async execute(_id, params, _signal, _onUpdate, _ctx) {
			if (!isTmuxAvailable()) return ok("Not running inside tmux; nothing to destroy.");
			if (params.all) {
				reconcile();
				const count = registry.size;
				for (const r of [...registry.values()]) destroySession(r);
				return ok(`Destroyed ${count} subagent session(s).`);
			}
			if (!params.target) return err("Provide target or set all=true.");
			const r = findByIdOrLabel(params.target);
			if (!r) return err(`No subagent session matching "${params.target}".`);
			destroySession(r);
			return ok(`Destroyed ${labelOf(r)}.`);
		},
		renderCall(args, theme) {
			return new Text(
				theme.fg("toolTitle", theme.bold("subagent_destroy ")) +
					theme.fg("accent", args.all ? "(all)" : String(args.target ?? "?")),
				0,
				0,
			);
		},
	});

	pi.registerTool({
		name: "subagent_run",
		label: "Subagent: run (one-shot)",
		description:
			"Ephemeral one-shot: create a subagent, send a single task, wait for the response, then destroy the session. Returns the response. Use for fire-and-forget delegation like 'reformat this text using the voice skill'. Set keepAlive=true to keep the session for follow-ups.",
		parameters: RunParams,
		async execute(_id, params, signal, _onUpdate, ctx) {
			if (!isTmuxAvailable()) return err("Not running inside tmux.");
			let r: SubagentRecord;
			try {
				r = await createSession({
					label: params.label,
					model: params.model,
					systemPrompt: params.systemPrompt,
					tools: params.tools,
					skills: params.skills,
					cwd: params.cwd ?? ctx.cwd,
					persistent: false,
					saveSession: false,
				});
			} catch (e: any) {
				return err(e.message);
			}
			try {
				const res = await lock.run(r.id, () =>
					sendText(r, params.text, true, params.timeoutMs ?? 180000, signal),
				);
				const text = res.text ?? "(no output)";
				if (res.timedOut) {
					return err(`Timed out. Partial response:\n\n${spill(text, OUTPUT_CAP, `out-${r.id}`)}`, {
						id: r.id,
						timedOut: true,
					});
				}
				return okOutput(text, { id: r.id, label: r.label });
			} catch (e: any) {
				return err(e.message, { id: r.id });
			} finally {
				if (!params.keepAlive) destroySession(r);
			}
		},
		renderCall(args, theme) {
			const preview = args.text ? (args.text.length > 60 ? String(args.text).slice(0, 60) + "..." : String(args.text)) : "...";
			return new Text(
				theme.fg("toolTitle", theme.bold("subagent_run ")) +
					theme.fg("accent", args.label ? String(args.label) : "(ephemeral)") +
					"\n  " +
					theme.fg("dim", preview),
				0,
				0,
			);
		},
	});

	// /subagents: quick human-readable summary
	pi.registerCommand("subagents", {
		description: "List managed subagent sessions",
		handler: async (_args, ctx) => {
			if (!isTmuxAvailable()) { ctx.ui.notify("Not running inside tmux.", "warning"); return; }
			reconcile();
			if (registry.size === 0) { ctx.ui.notify("No subagent sessions.", "info"); return; }
			const rows = [...registry.values()]
				.sort((a, b) => a.createdAt - b.createdAt)
				.map((r) => {
					const lbl = r.label ? `${r.label} ` : "";
					return `${lbl}(${r.id}) [${r.status}] ${r.persistent ? "persistent" : "ephemeral"} cwd=${r.cwd}`;
				});
			ctx.ui.notify(rows.join("\n"), "info");
		},
	});

	registerPanel(pi);

	pi.registerTool({
		name: "subagent_alert_add",
		label: "Subagent: add alert",
		description:
			"Register a regex that watches live subagent output (tool snippets, assistant text, settled text) and fires a pointed alert into your context on match, with a steer hint — so you can correct a subagent that is losing the plot without waiting for it to finish. Patterns can be added and removed while subagents run. Use cooldownMs to avoid spam.",
		parameters: AlertAddParams,
		async execute(_id, params, _signal, _onUpdate, _ctx) {
			try {
				const ap = addAlert({
					pattern: params.pattern,
					flags: params.flags,
					label: params.label,
					target: params.target,
					cooldownMs: params.cooldownMs,
				});
				const where = ap.target ? ` watching ${ap.target}` : " watching all subagents";
				return ok(
					`Alert ${ap.id} registered for /${ap.pattern}/${ap.flags}${where}.` +
						` Fires on match (cooldown ${ap.cooldownMs}ms). Remove with subagent_alert_remove.`,
					{ id: ap.id, pattern: ap.pattern, flags: ap.flags, label: ap.label, target: ap.target, cooldownMs: ap.cooldownMs },
				);
			} catch (e: any) {
				return err(`Invalid regex: ${e.message}`);
			}
		},
		renderCall(args, theme) {
			return new Text(
				theme.fg("toolTitle", theme.bold("subagent_alert_add ")) +
					theme.fg("accent", `/${String(args.pattern)}/${String(args.flags ?? "")}`),
				0,
				0,
			);
		},
	});

	pi.registerTool({
		name: "subagent_alert_remove",
		label: "Subagent: remove alert",
		description:
			"Deregister an alert by id (from subagent_alert_add) or by pattern source, or all of them. Takes effect on the next poll tick — safe to call while subagents run.",
		parameters: AlertRemoveParams,
		async execute(_id, params, _signal, _onUpdate, _ctx) {
			if (params.all) {
				const n = clearAlerts();
				return ok(n === 0 ? "No alerts to remove." : `Removed ${n} alert(s).`);
			}
			if (params.id) {
				if (!removeAlert(params.id)) return err(`No alert with id "${params.id}".`);
				return ok(`Removed alert ${params.id}.`);
			}
			if (params.pattern) {
				const n = removeAlertsByPattern(params.pattern);
				return ok(n === 0 ? `No alerts matching /${params.pattern}/.` : `Removed ${n} alert(s) matching /${params.pattern}/.`);
			}
			return err("Provide id, pattern, or all=true.");
		},
	});

	pi.registerTool({
		name: "subagent_alert_list",
		label: "Subagent: list alerts",
		description: "List registered alert regexes (id, pattern, flags, label, target, cooldown). Also shown in the /panels Alerts tab.",
		parameters: Type.Object({}),
		async execute(_id, _params, _signal, _onUpdate, _ctx) {
			const alerts = getAlerts();
			if (alerts.length === 0) return ok("No alerts registered.");
			const rows = alerts.map((a: AlertPattern) => {
				const lbl = a.label ? ` "${a.label}"` : "";
				const where = a.target ? ` -> ${a.target}` : " -> all";
				return `- ${a.id}${lbl}: /${a.pattern}/${a.flags}${where} (cd ${a.cooldownMs}ms)`;
			});
			return ok(rows.join("\n"), { alerts });
		},
	});
}
