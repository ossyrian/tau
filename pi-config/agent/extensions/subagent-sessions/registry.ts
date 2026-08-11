/**
 * The parent's book of live subagents. Backed by REGISTRY_FILE and mirrored in
 * an in-memory map so the alert poller and the panel UI can read live subagents
 * without importing the manager.
 *
 * Also owns each subagent's scratch files (system prompt, per-turn input
 * payloads) and the reconcile pass that drops dead entries and refreshes status
 * from the beacon stream.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { STATE_DIR, ensureStateDir } from "./state.ts";
import { readBeaconEvents, removeBeacon } from "./beacon-store.ts";
import { sessionExists, tmux } from "./tmux.ts";

export const REGISTRY_FILE = path.join(STATE_DIR, "registry.json");

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
