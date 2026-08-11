/**
 * Alert engine: watches live subagents' beacon streams for user-registered
 * regex patterns and injects a pointed notification into the orchestrator's
 * session on a match, so the orchestrator can steer a subagent that is losing
 * the plot without waiting for it to finish.
 *
 * Lightweight by design — context-bloat-aware:
 *   - Polls each live subagent's beacon JSONL for *new* events only.
 *   - Tests only the text-bearing events (tool snippets, assistant text,
 *     settled text) against the current pattern set.
 *   - On a match, injects a minimal notification: which subagent, which alert,
 *     a short slice of the matched text, and a steer hint. The parent already
 *     knows why it set the alert, so we don't re-explain — anything past the
 *     slice spills to a file the parent can grep. Not the whole transcript.
 *   - Per-(pattern, subagent) cooldown prevents repeat spam for the same
 *     signal; patterns can be added/removed mid-run and take effect on the
 *     next tick.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { type AlertPattern, getAlerts } from "./alert-store.ts";
import { type BeaconEvent, cap, readBeaconEvents, spillToFile } from "./beacon-store.ts";
import { type SubagentRecord, getRegistry, labelOf } from "./registry.ts";
import { sessionExists } from "./tmux.ts";

const POLL_MS = 2000;
/** Alerts are meant to inform, not to carry payload. Keep the matched slice short;
 * the parent set the alert and can grep the spill file for the rest. */
const ALERT_SLICE = 200;

/** Per-subagent index of how many beacon events have been consumed. */
const lastSeen = new Map<string, number>();
/** Last-fired timestamp per `${alertId}:${subagentId}`, for cooldown. */
const lastFired = new Map<string, number>();

// Stored on globalThis so a `/reload` (which re-instantiates this module and
// resets the module-level `timer`) can detect and stop a poller left running by
// the previous instance — otherwise reloads stack duplicate pollers and every
// match fires twice.
const TIMER_KEY = "__subagentAlertTimer";
function getTimer(): ReturnType<typeof setInterval> | undefined {
	return (globalThis as Record<string, unknown>)[TIMER_KEY] as ReturnType<typeof setInterval> | undefined;
}
function setTimer(t: ReturnType<typeof setInterval> | undefined): void {
	if (t) (globalThis as Record<string, unknown>)[TIMER_KEY] = t;
	else delete (globalThis as Record<string, unknown>)[TIMER_KEY];
}

/** The text a pattern is tested against for a given event. Empty for non-text events. */
function eventText(e: BeaconEvent): string {
	switch (e.event) {
		case "tool":
			return [e.summary, e.snippet].filter((s): s is string => !!s).join("\n");
		case "assistant":
		case "settled":
			return e.text ?? "";
		default:
			return "";
	}
}

/** Whether an alert's optional target filter applies to this subagent. */
function targetMatches(alert: AlertPattern, r: SubagentRecord): boolean {
	if (!alert.target) return true;
	const t = alert.target.toLowerCase();
	return r.id.toLowerCase() === t || (!!r.label && r.label.toLowerCase() === t);
}

function buildNotification(r: SubagentRecord, alert: AlertPattern, e: BeaconEvent): string {
	const matched = eventText(e);
	const re = safeCompile(alert);
	// Show only the matched region. If the full event text is large, spill it to a
	// file and append the path so the parent can grep it — it set this alert and
	// knows why, so we don't re-explain or dump the transcript.
	const region = re ? firstMatchSlice(re, matched, ALERT_SLICE) : cap(matched, ALERT_SLICE);
	const file = matched.length > ALERT_SLICE ? spillToFile(matched, `alert-${r.id}`) : undefined;
	const more = file ? ` (full: ${file})` : "";
	const target = r.label ?? r.id;
	return [
		`⚠️ Alert "${alert.label ?? alert.pattern}" tripped on ${labelOf(r)} (${e.event}): ${region}${more}`,
		`Steer: subagent_send({ target: "${target}", text: "<correction>", steer: true })`,
	].join("\n");
}

function safeCompile(alert: AlertPattern): RegExp | undefined {
	try {
		return new RegExp(alert.pattern, alert.flags);
	} catch {
		return undefined;
	}
}

/** Return the first match plus a little surrounding text, capped to `n` chars. */
function firstMatchSlice(re: RegExp, text: string, n: number): string {
	const m = re.exec(text);
	if (!m) return cap(text, n);
	const start = Math.max(0, m.index - 40);
	const end = Math.min(text.length, m.index + m[0].length + 80);
	return cap(text.slice(start, end), n);
}

function checkSubagent(pi: ExtensionAPI, r: SubagentRecord): void {
	if (!sessionExists(r.tmuxSession)) return;
	const evs = readBeaconEvents(r.id);
	// First time we see a subagent (e.g. after a /reload reset lastSeen), skip its
	// existing backlog so we only alert on activity from here forward — never
	// re-fire on historical matches.
	if (!lastSeen.has(r.id)) {
		lastSeen.set(r.id, evs.length);
		return;
	}
	const from = lastSeen.get(r.id) ?? 0;
	if (evs.length < from) {
		// Beacon file was reset (new session reusing an id) — resume from 0.
		lastSeen.set(r.id, evs.length);
		return;
	}
	const alerts = getAlerts();
	for (let i = from; i < evs.length; i++) {
		const e = evs[i];
		const text = eventText(e);
		if (!text) continue;
		for (const alert of alerts) {
			if (!targetMatches(alert, r)) continue;
			const re = safeCompile(alert);
			if (!re || !re.test(text)) continue;
			const key = `${alert.id}:${r.id}`;
			const now = Date.now();
			const last = lastFired.get(key) ?? 0;
			if (now - last < alert.cooldownMs) continue;
			lastFired.set(key, now);
			// steer = interrupt the orchestrator immediately if mid-run; when
			// idle this just starts a turn with the alert.
			pi.sendUserMessage(buildNotification(r, alert, e), { deliverAs: "steer" });
		}
	}
	lastSeen.set(r.id, evs.length);
}

function tick(pi: ExtensionAPI): void {
	for (const r of getRegistry().values()) checkSubagent(pi, r);
}

export function startAlertPoller(pi: ExtensionAPI): void {
	// Stop any poller left running by a previous module instance (e.g. after /reload).
	const existing = getTimer();
	if (existing) clearInterval(existing);
	const timer = setInterval(() => {
		try {
			tick(pi);
		} catch {
			/* never let the poller kill the session */
		}
	}, POLL_MS);
	// Don't keep the event loop alive solely for alert polling.
	if (typeof timer.unref === "function") timer.unref();
	setTimer(timer);
}

export function stopAlertPoller(): void {
	const timer = getTimer();
	if (timer) {
		clearInterval(timer);
		setTimer(undefined);
	}
	lastSeen.clear();
	lastFired.clear();
}

/** Drop lastSeen for a subagent so its history isn't re-scanned if the id is reused. */
export function forgetSubagent(id: string): void {
	lastSeen.delete(id);
	for (const key of lastFired.keys()) {
		if (key.endsWith(`:${id}`)) lastFired.delete(key);
	}
}
