/**
 * Alert patterns: the regexes the parent watches subagent output for. This is
 * the persistent data model (backed by ALERTS_FILE + an in-memory map). The
 * matching/notification loop that consumes these lives in alerts.ts.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { STATE_DIR, ensureStateDir, genId } from "./state.ts";

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
