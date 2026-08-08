/**
 * Unified modal panel: one vim-ish "mode" for inspecting Plans, Subagents, and
 * Alerts without leaving the orchestrator's session.
 *
 * Opened via `/panels` or ctrl+alt+v. Tab cycling mirrors vim: h/l (or tab)
 * switch panels, j/k move within, g/G top/bottom, d acts on the selection
 * (destroy subagent / remove alert), r refreshes, q/Esc returns to insert.
 *
 * Plans are read from the current session branch's `write_todos` tool results
 * (same reconstruction the todos extension uses). Subagents and Alerts read the
 * live module-level stores, and a 1s render tick keeps them current while open.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Key, matchesKey, truncateToWidth } from "@earendil-works/pi-tui";
import {
	type AlertPattern,
	type BeaconEvent,
	type SubagentRecord,
	cap,
	destroySubagent,
	getAlerts,
	getRegistry,
	labelOf,
	readBeaconEvents,
	reconcile,
	removeAlert,
} from "./shared.ts";
import { forgetSubagent } from "./alerts.ts";

type Tab = "plans" | "subagents" | "alerts";
const TABS: Tab[] = ["plans", "subagents", "alerts"];
const DEFAULT_PLAN = "default";

type TodoStatus = "pending" | "in_progress" | "completed";
interface Todo {
	content: string;
	status: TodoStatus;
}
interface TodoDetails {
	todos: Todo[];
	plan: string;
}

/** Walk the current branch and rebuild the set of todo plans (last write per plan wins). */
function snapshotPlans(ctx: ExtensionContext): { plans: Map<string, Todo[]>; active: string } {
	const plans = new Map<string, Todo[]>();
	let active = DEFAULT_PLAN;
	for (const entry of ctx.sessionManager.getBranch()) {
		if (entry.type !== "message") continue;
		const msg = (entry as { message: { role: string; toolName?: string; details?: TodoDetails } }).message;
		if (msg.role !== "toolResult" || msg.toolName !== "write_todos") continue;
		const details = msg.details;
		const planName = (details?.plan ?? DEFAULT_PLAN) || DEFAULT_PLAN;
		if (Array.isArray(details?.todos)) {
			plans.set(planName, details.todos.filter((t) => t && t.content));
			active = planName;
		}
	}
	return { plans, active };
}

function statusIcon(status: TodoStatus, mark: (c: string, s: string) => string): string {
	if (status === "completed") return mark("success", "✓");
	if (status === "in_progress") return mark("accent", "●");
	return mark("dim", "○");
}

function planLabel(name: string): string {
	return name === DEFAULT_PLAN ? "Plan" : name;
}

/** One-line digest of a beacon event for the Subagents tab's activity preview. */
function beaconDigest(e: BeaconEvent): string {
	switch (e.event) {
		case "ready":
			return "ready";
		case "busy":
			return "busy";
		case "settled":
			return `settled: ${cap(e.text ?? "", 120)}`;
		case "assistant":
			return `said: ${cap(e.text ?? "", 120)}`;
		case "tool":
			return `${e.isError ? "✗" : "•"} ${e.toolName}: ${cap(e.summary ?? "", 80)}`;
		case "shutdown":
			return "shutdown";
		default:
			return e.event;
	}
}

class PanelsComponent {
	private ctx: ExtensionContext;
	private theme: any;
	private onClose: () => void;
	private tab: Tab = "plans";
	private selected = 0;
	private plans: Map<string, Todo[]>;
	private activePlan: string;
	private tick?: ReturnType<typeof setInterval>;
	private cachedWidth?: number;
	private cachedLines?: string[];
	private lastReconcile = 0;

	constructor(ctx: ExtensionContext, theme: any, onClose: () => void) {
		this.ctx = ctx;
		this.theme = theme;
		this.onClose = onClose;
		const snap = snapshotPlans(ctx);
		this.plans = snap.plans;
		this.activePlan = snap.active;
	}

	startLive(tui: { requestRender: () => void }): void {
		this.tick = setInterval(() => {
			this.invalidate();
			tui.requestRender();
		}, 1000);
		if (typeof this.tick.unref === "function") this.tick.unref();
	}

	private stopLive(): void {
		if (this.tick) {
			clearInterval(this.tick);
			this.tick = undefined;
		}
	}

	close(): void {
		this.stopLive();
		this.onClose();
	}

	private items(): number {
		if (this.tab === "plans") return Math.max(this.plans.size, 1);
		if (this.tab === "subagents") return Math.max(getRegistry().size, 1);
		return Math.max(getAlerts().length, 1);
	}

	private clampSelection(): void {
		const n = this.items();
		if (this.selected >= n) this.selected = n - 1;
		if (this.selected < 0) this.selected = 0;
	}

	handleInput(data: string): void {
		// Tab switching (vim h/l, plus tab/shift+tab).
		if (matchesKey(data, "l") || matchesKey(data, Key.right) || matchesKey(data, Key.tab)) {
			this.switchTab(1);
			return;
		}
		if (matchesKey(data, "h") || matchesKey(data, Key.left) || matchesKey(data, "shift+tab")) {
			this.switchTab(-1);
			return;
		}
		// Vertical movement (vim j/k).
		if (matchesKey(data, "j") || matchesKey(data, Key.down)) {
			this.selected = Math.min(this.items() - 1, this.selected + 1);
			this.invalidate();
			return;
		}
		if (matchesKey(data, "k") || matchesKey(data, Key.up)) {
			this.selected = Math.max(0, this.selected - 1);
			this.invalidate();
			return;
		}
		if (matchesKey(data, "g")) {
			this.selected = 0;
			this.invalidate();
			return;
		}
		if (matchesKey(data, "shift+g")) {
			this.selected = this.items() - 1;
			this.invalidate();
			return;
		}
		if (matchesKey(data, "r")) {
			this.refresh();
			return;
		}
		if (matchesKey(data, "d")) {
			this.actOnSelected();
			return;
		}
		if (matchesKey(data, "escape") || matchesKey(data, "q") || matchesKey(data, Key.ctrl("c"))) {
			this.close();
			return;
		}
	}

	private switchTab(dir: 1 | -1): void {
		const idx = TABS.indexOf(this.tab);
		this.tab = TABS[(idx + dir + TABS.length) % TABS.length];
		this.selected = 0;
		this.invalidate();
	}

	private refresh(): void {
		if (this.tab === "plans") {
			const snap = snapshotPlans(this.ctx);
			this.plans = snap.plans;
			this.activePlan = snap.active;
		} else if (this.tab === "subagents") {
			reconcile();
		}
		this.clampSelection();
		this.invalidate();
		this.ctx.ui.notify("Refreshed", "info");
	}

	private actOnSelected(): void {
		if (this.tab === "alerts") {
			const alerts = getAlerts();
			const ap = alerts[this.selected];
			if (!ap) return;
			removeAlert(ap.id);
			this.clampSelection();
			this.invalidate();
			this.ctx.ui.notify(`Removed alert ${ap.label ?? ap.pattern}`, "info");
			return;
		}
		if (this.tab === "subagents") {
			const records = [...getRegistry().values()].sort((a, b) => a.createdAt - b.createdAt);
			const r = records[this.selected];
			if (!r) return;
			destroySubagent(r);
			forgetSubagent(r.id);
			this.clampSelection();
			this.invalidate();
			this.ctx.ui.notify(`Destroyed ${labelOf(r)}`, "info");
			return;
		}
		// Plans: no destructive action.
	}

	render(width: number): string[] {
		if (this.cachedLines && this.cachedWidth === width) return this.cachedLines;
		const th = this.theme;
		const lines: string[] = [];
		lines.push("");

		// Title bar with tabs; the active tab is highlighted.
		const tabParts = TABS.map((t) => {
			const label = t.charAt(0).toUpperCase() + t.slice(1);
			return t === this.tab ? th.fg("accent", th.bold(`[${label}]`)) : th.fg("dim", ` ${label} `);
		});
		const title = th.fg("borderMuted", "─") + " " + tabParts.join("  ") + " " + th.fg("borderMuted", "─".repeat(Math.max(0, width - tabParts.join("  ").length - 4)));
		lines.push(truncateToWidth(title, width));
		lines.push("");

		if (this.tab === "plans") this.renderPlans(lines, width);
		else if (this.tab === "subagents") this.renderSubagents(lines, width);
		else this.renderAlerts(lines, width);

		lines.push("");
		lines.push(truncateToWidth(`  ${th.fg("dim", "h/l tabs · j/k move · g/G top/bottom · d act · r refresh · q/Esc back to insert")}`, width));
		lines.push("");

		this.cachedWidth = width;
		this.cachedLines = lines;
		return lines;
	}

	private renderPlans(lines: string[], width: number): void {
		const th = this.theme;
		if (this.plans.size === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No plans. Ask the agent to plan a multi-step task.")}`, width));
			return;
		}
		const names = [...this.plans.keys()];
		const showing = names[Math.min(this.selected, names.length - 1)] ?? this.activePlan;
		const list = this.plans.get(showing) ?? [];
		const done = list.filter((t) => t.status === "completed").length;
		const isActive = showing === this.activePlan;
		const tag = isActive ? th.fg("accent", " active") : th.fg("dim", " viewing");
		lines.push(truncateToWidth(`  ${th.fg("muted", planLabel(showing))}${tag}  ${th.fg("dim", `${done}/${list.length} done`)}`, width));
		lines.push("");
		if (list.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "(empty)")}`, width));
			return;
		}
		list.forEach((t, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const mark = (c: string, s: string) => th.fg(c, s);
			const text = t.status === "completed" ? th.fg("dim", t.content) : t.status === "in_progress" ? th.fg("text", t.content) : th.fg("muted", t.content);
			lines.push(truncateToWidth(`${cursor}${statusIcon(t.status, mark)} ${th.fg("dim", `${i + 1}.`)} ${text}`, width));
		});
		if (names.length > 1) {
			lines.push("");
			lines.push(truncateToWidth(`  ${th.fg("dim", `Plans: ${names.map((n) => (n === this.activePlan ? planLabel(n) + "*" : planLabel(n))).join("  ·  ")}`)}`, width));
		}
	}

	private renderSubagents(lines: string[], width: number): void {
		const th = this.theme;
		// reconcile() shells out to tmux; gate it so a 1s render tick doesn't
		// spawn a subprocess every frame. Status text stays live from the
		// registry (sendText/waitForBeacon keep it current); this only catches
		// sessions killed outside our tools.
		if (Date.now() - this.lastReconcile > 3000) {
			reconcile();
			this.lastReconcile = Date.now();
		}
		const records = [...getRegistry().values()].sort((a, b) => a.createdAt - b.createdAt);
		if (records.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No subagent sessions. Use subagent_create or subagent_run.")}`, width));
			return;
		}
		records.forEach((r, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const status = r.status === "busy" ? th.fg("warning", "busy") : r.status === "idle" ? th.fg("success", "idle") : th.fg("dim", r.status);
			const lbl = r.label ? th.fg("accent", r.label) + th.fg("dim", ` (${r.id})`) : th.fg("accent", r.id);
			const kind = r.persistent ? "persistent" : "ephemeral";
			lines.push(truncateToWidth(`${cursor}${status}  ${lbl}  ${th.fg("dim", `${kind} · ${r.settledCount} settled`)}`, width));
			if (i === this.selected) {
				const evs = readBeaconEvents(r.id);
				for (const e of evs.slice(-3)) {
					lines.push(truncateToWidth(`      ${th.fg("dim", beaconDigest(e))}`, width));
				}
				lines.push(truncateToWidth(`      ${th.fg("dim", `cwd=${r.cwd}`)}`, width));
			}
		});
	}

	private renderAlerts(lines: string[], width: number): void {
		const th = this.theme;
		const alerts = getAlerts();
		if (alerts.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No alerts registered. Use subagent_alert_add.")}`, width));
			return;
		}
		alerts.forEach((a: AlertPattern, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const lbl = a.label ? th.fg("accent", `"${a.label}"`) + " " : "";
			const where = a.target ? th.fg("dim", ` -> ${a.target}`) : th.fg("dim", " -> all");
			lines.push(truncateToWidth(`${cursor}${lbl}${th.fg("muted", `/${a.pattern}/${a.flags}`)}${where} ${th.fg("dim", `${a.cooldownMs}ms`)}`, width));
		});
		lines.push("");
		lines.push(truncateToWidth(`  ${th.fg("dim", "d removes the selected alert")}`, width));
	}

	invalidate(): void {
		this.cachedWidth = undefined;
		this.cachedLines = undefined;
	}
}

function openPanels(ctx: ExtensionContext): void {
	if (ctx.mode !== "tui") {
		ctx.ui.notify("Panels viewer is TUI-only.", "warning");
		return;
	}
	ctx.ui.custom<void>((tui, theme, _kb, done) => {
		const comp = new PanelsComponent(ctx, theme, () => done());
		comp.startLive(tui);
		return comp;
	});
}

export function registerPanel(pi: ExtensionAPI): void {
	pi.registerCommand("panels", {
		description: "Open the unified Plans / Subagents / Alerts panel (vim-ish; q/Esc returns to insert)",
		handler: async (_args, ctx) => openPanels(ctx),
	});

	pi.registerShortcut(Key.ctrlAlt("v"), {
		description: "Open the Plans / Subagents / Alerts panel",
		handler: async (ctx) => openPanels(ctx),
	});
}
