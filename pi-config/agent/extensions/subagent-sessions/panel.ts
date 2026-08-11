/**
 * Unified modal panel: one vim-ish "mode" for inspecting Plans, Subagents,
 * Sessions, and Alerts without leaving the orchestrator's session.
 *
 * Opened via `/panels` or Ctrl+Alt+V (the same shortcut closes it). Tab cycling
 * mirrors vim: h/l (or tab) switch tabs, j/k move within, g/G top/bottom, d acts
 * on the selection (destroy subagent / remove alert), Enter/s switches the tmux
 * client to the selected subagent or session, r refreshes, q/Esc returns to insert.
 *
 * Live behavior: a 1s tick re-reads the live stores (registry reconciled against
 * tmux, alerts reloaded from disk, tmux sessions re-listed) so completions,
 * destructions, and new sessions show up without a manual refresh. Renders are
 * driven only when the visible content actually changes, so nothing repaints —
 * or double-repaints — on an idle tick.
 *
 * Plans are read from the current session branch's `write_todos` tool results
 * (same reconstruction the todos extension uses).
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Key, matchesKey, truncateToWidth, visibleWidth } from "@earendil-works/pi-tui";
import { type AlertPattern, getAlerts, loadAlerts, removeAlert } from "./alert-store.ts";
import { type BeaconEvent, cap, readBeaconEvents } from "./beacon-store.ts";
import { type SubagentRecord, destroySubagent, getRegistry, labelOf, reconcile } from "./registry.ts";
import { type TmuxSession, listTmuxSessions, switchTmuxClient } from "./tmux.ts";
import { forgetSubagent } from "./alerts.ts";

type Tab = "plans" | "subagents" | "sessions" | "alerts";
const TABS: Tab[] = ["plans", "subagents", "sessions", "alerts"];
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
// Collapse newlines/tabs into single spaces so a value can never span more than
// one terminal row. The Component contract requires render() to return one
// physical line per array entry; a stray \n silently breaks the differential
// renderer's line accounting and corrupts every subsequent frame.
function oneLine(s: string): string {
	return s.replace(/\s+/g, " ").trim();
}

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

/** Human-readable "time ago" for a ms epoch. */
function ago(ms: number): string {
	if (!ms) return "?";
	const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
	if (s < 60) return `${s}s ago`;
	const m = Math.round(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.round(m / 60);
	if (h < 24) return `${h}h ago`;
	return `${Math.round(h / 24)}d ago`;
}

/** Consistent status token used across the Subagents and Sessions tabs. */
function statusToken(th: any, status: SubagentRecord["status"]): string {
	switch (status) {
		case "busy":
			return th.fg("warning", "● busy");
		case "idle":
			return th.fg("success", "● idle");
		case "starting":
			return th.fg("accent", "● starting");
		case "dead":
			return th.fg("dim", "○ dead");
		default:
			return th.fg("dim", `○ ${status}`);
	}
}

class PanelsComponent {
	private ctx: ExtensionContext;
	private theme: any;
	private tui: { requestRender: () => void };
	private onClose: () => void;
	private tab: Tab = "plans";
	private selected = 0;
	private plans: Map<string, Todo[]>;
	private activePlan: string;
	private subagents: SubagentRecord[] = [];
	private sessions: TmuxSession[] = [];
	private alerts: AlertPattern[] = [];
	private tick?: ReturnType<typeof setInterval>;
	private cachedWidth?: number;
	private cachedLines?: string[];
	/** Signature of the last snapshot we rendered, to suppress no-op repaints. */
	private lastSignature = "";

	constructor(ctx: ExtensionContext, theme: any, tui: { requestRender: () => void }, onClose: () => void) {
		this.ctx = ctx;
		this.theme = theme;
		this.tui = tui;
		this.onClose = onClose;
		this.plans = new Map();
		this.activePlan = DEFAULT_PLAN;
		this.refreshData();
	}

	/**
	 * Pull every live store into local snapshots. Cheap enough (one tmux
	 * list-sessions + reconcile) to run on the 1s tick so the panel reflects
	 * completions, destructions, and new sessions without a manual refresh.
	 */
	private refreshData(): void {
		const snap = snapshotPlans(this.ctx);
		this.plans = snap.plans;
		this.activePlan = snap.active;
		// reconcile() shells to tmux and drops dead subagents + refreshes status
		// from beacons, so a completed or destroyed subagent updates here.
		reconcile();
		this.subagents = [...getRegistry().values()].sort((a, b) => a.createdAt - b.createdAt);
		this.sessions = listTmuxSessions().sort((a, b) => a.createdAt - b.createdAt);
		// Alerts can be added/removed by tools mid-session; reload from disk so the
		// tab stays truthful even when the change came from another code path.
		loadAlerts();
		this.alerts = getAlerts();
	}

	/**
	 * Content signature over everything the panel can display. If it is unchanged
	 * between ticks we skip both the cache-busting and the requestRender, which is
	 * what prevents the idle double/triple repaints.
	 */
	private signature(): string {
		const parts: string[] = [this.tab, String(this.selected)];
		for (const [name, todos] of this.plans) {
			parts.push(`p:${name}:${todos.map((t) => t.status[0] + t.content).join("|")}`);
		}
		for (const r of this.subagents) {
			parts.push(`a:${r.id}:${r.status}:${r.settledCount}:${r.lastText?.length ?? 0}`);
		}
		for (const s of this.sessions) {
			parts.push(`s:${s.name}:${s.current ? 1 : 0}:${s.attached ? 1 : 0}:${s.windows}`);
		}
		for (const a of this.alerts) {
			parts.push(`x:${a.id}:${a.pattern}:${a.flags}:${a.target ?? ""}`);
		}
		return parts.join("\n");
	}

	startLive(): void {
		this.lastSignature = this.signature();
		this.tick = setInterval(() => {
			this.refreshData();
			this.clampSelection();
			const sig = this.signature();
			if (sig !== this.lastSignature) {
				this.lastSignature = sig;
				this.invalidate();
				this.tui.requestRender();
			}
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
		if (this.tab === "subagents") return Math.max(this.subagents.length, 1);
		if (this.tab === "sessions") return Math.max(this.sessions.length, 1);
		return Math.max(this.alerts.length, 1);
	}

	private clampSelection(): void {
		const n = this.items();
		if (this.selected >= n) this.selected = n - 1;
		if (this.selected < 0) this.selected = 0;
	}

	/**
	 * Mark the render cache dirty after a state change. The repaint itself is
	 * driven exactly once by the input wrapper (after handleInput) or by the live
	 * tick — never from here — so a single keypress never triggers two renders.
	 * We also refresh lastSignature so the next tick doesn't re-fire for a change
	 * the keypress already painted.
	 */
	private touch(): void {
		this.lastSignature = this.signature();
		this.invalidate();
	}

	handleInput(data: string): void {
		// Ctrl+Alt+V (the open shortcut) toggles the panel closed.
		if (matchesKey(data, Key.ctrlAlt("v"))) {
			this.close();
			return;
		}
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
			this.touch();
			return;
		}
		if (matchesKey(data, "k") || matchesKey(data, Key.up)) {
			this.selected = Math.max(0, this.selected - 1);
			this.touch();
			return;
		}
		if (matchesKey(data, "g")) {
			this.selected = 0;
			this.touch();
			return;
		}
		if (matchesKey(data, "shift+g")) {
			this.selected = this.items() - 1;
			this.touch();
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
		if (matchesKey(data, Key.enter) || matchesKey(data, "s")) {
			this.switchToSelected();
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
		this.touch();
	}

	private refresh(): void {
		this.refreshData();
		this.clampSelection();
		this.touch();
		this.ctx.ui.notify("Refreshed", "info");
	}

	private actOnSelected(): void {
		if (this.tab === "alerts") {
			const ap = this.alerts[this.selected];
			if (!ap) return;
			removeAlert(ap.id);
			this.refreshData();
			this.clampSelection();
			this.touch();
			this.ctx.ui.notify(`Removed alert ${ap.label ?? ap.pattern}`, "info");
			return;
		}
		if (this.tab === "subagents") {
			const r = this.subagents[this.selected];
			if (!r) return;
			destroySubagent(r);
			forgetSubagent(r.id);
			this.refreshData();
			this.clampSelection();
			this.touch();
			this.ctx.ui.notify(`Destroyed ${labelOf(r)}`, "info");
			return;
		}
		// Plans and Sessions have no destructive action here.
	}

	/**
	 * Switch the current tmux client to the selected subagent or session, then
	 * close the panel — the orchestrator pi stays alive in the background.
	 */
	private switchToSelected(): void {
		let target: string | undefined;
		let label: string | undefined;
		if (this.tab === "subagents") {
			const r = this.subagents[this.selected];
			if (r) {
				target = r.tmuxSession;
				label = labelOf(r);
			}
		} else if (this.tab === "sessions") {
			const s = this.sessions[this.selected];
			if (s) {
				if (s.current) {
					this.ctx.ui.notify("Already attached to this session.", "info");
					return;
				}
				target = s.name;
				label = s.name;
			}
		} else {
			return;
		}
		if (!target) return;
		if (switchTmuxClient(target)) {
			this.ctx.ui.notify(`Switched to ${label}`, "info");
			this.close();
		} else {
			this.ctx.ui.notify(`Could not switch to ${label} (session gone or not attached).`, "warning");
			this.refresh();
		}
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
		const tabsPlain = tabParts.join("  ");
		const fillWidth = Math.max(0, width - visibleWidth(tabsPlain) - 4);
		const title = `${th.fg("borderMuted", "─")} ${tabsPlain} ${th.fg("borderMuted", "─".repeat(fillWidth))}`;
		lines.push(truncateToWidth(title, width));
		lines.push("");

		if (this.tab === "plans") this.renderPlans(lines, width);
		else if (this.tab === "subagents") this.renderSubagents(lines, width);
		else if (this.tab === "sessions") this.renderSessions(lines, width);
		else this.renderAlerts(lines, width);

		lines.push("");
		lines.push(truncateToWidth(`  ${th.fg("dim", this.helpText())}`, width));
		lines.push(truncateToWidth(`  ${th.fg("dim", "Ctrl+Alt+V toggles this panel")}`, width));
		lines.push("");

		this.cachedWidth = width;
		this.cachedLines = lines;
		return lines;
	}

	/** Per-tab help so the visible actions always match the current tab. */
	private helpText(): string {
		const nav = "h/l tabs · j/k move · g/G top/bottom · r refresh · q/Esc back to insert";
		if (this.tab === "subagents") return `${nav} · Enter/s switch to session · d destroy`;
		if (this.tab === "sessions") return `${nav} · Enter/s switch to session`;
		if (this.tab === "alerts") return `${nav} · d remove alert`;
		return nav;
	}

	// One row per plan with a cursor, mirroring the Subagents tab: j/k moves the
	// selection and only the selected plan expands its todos below. This keeps the
	// full set of plans visible at once and no plan stays permanently expanded.
	private renderPlans(lines: string[], width: number): void {
		const th = this.theme;
		if (this.plans.size === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No plans. Ask the agent to plan a multi-step task.")}`, width));
			return;
		}
		const names = [...this.plans.keys()];
		names.forEach((name, i) => {
			const list = this.plans.get(name) ?? [];
			const done = list.filter((t) => t.status === "completed").length;
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const tag = name === this.activePlan ? th.fg("success", "● active") : th.fg("dim", "○ inactive");
			lines.push(
				truncateToWidth(
					`${cursor}${tag}  ${th.fg("accent", planLabel(name))}  ${th.fg("dim", `${done}/${list.length} done`)}`,
					width,
				),
			);
			if (i !== this.selected) return;
			if (list.length === 0) {
				lines.push(truncateToWidth(`      ${th.fg("dim", "(empty)")}`, width));
				return;
			}
			list.forEach((t, n) => {
				const mark = (c: string, s: string) => th.fg(c, s);
				const text =
					t.status === "completed"
						? th.fg("dim", t.content)
						: t.status === "in_progress"
							? th.fg("text", t.content)
							: th.fg("muted", t.content);
				lines.push(truncateToWidth(`      ${statusIcon(t.status, mark)} ${th.fg("dim", `${n + 1}.`)} ${text}`, width));
			});
		});
	}

	private renderSubagents(lines: string[], width: number): void {
		const th = this.theme;
		if (this.subagents.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No subagent sessions. Use subagent_create or subagent_run.")}`, width));
			return;
		}
		this.subagents.forEach((r, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const status = statusToken(th, r.status);
			const lbl = r.label ? th.fg("accent", r.label) + th.fg("dim", ` (${r.id})`) : th.fg("accent", r.id);
			const kind = r.persistent ? "persistent" : "ephemeral";
			lines.push(truncateToWidth(`${cursor}${status}  ${lbl}  ${th.fg("dim", `${kind} · ${r.settledCount} settled`)}`, width));
			if (i === this.selected) {
				const evs = readBeaconEvents(r.id);
				for (const e of evs.slice(-3)) {
					lines.push(truncateToWidth(`      ${th.fg("dim", oneLine(beaconDigest(e)))}`, width));
				}
				const origin = r.spawnedBy ? `spawned by ${r.spawnedBy} · ` : "";
				lines.push(truncateToWidth(`      ${th.fg("dim", `${origin}cwd=${r.cwd}`)}`, width));
			}
		});
	}

	private renderSessions(lines: string[], width: number): void {
		const th = this.theme;
		if (this.sessions.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No tmux sessions found.")}`, width));
			return;
		}
		this.sessions.forEach((s, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const here = s.current ? th.fg("success", "● here") : s.attached ? th.fg("accent", "● attached") : th.fg("dim", "○ detached");
			const kind = s.kind === "subagent" ? th.fg("muted", "subagent") : th.fg("muted", "session");
			const name = th.fg("accent", s.name);
			lines.push(
				truncateToWidth(
					`${cursor}${here}  ${name}  ${kind} ${th.fg("dim", `· ${s.windows} win · active ${ago(s.lastActivity)}`)}`,
					width,
				),
			);
		});
	}

	private renderAlerts(lines: string[], width: number): void {
		const th = this.theme;
		if (this.alerts.length === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No alerts registered. Use subagent_alert_add.")}`, width));
			return;
		}
		this.alerts.forEach((a: AlertPattern, i) => {
			const cursor = i === this.selected ? th.fg("accent", "▸ ") : "  ";
			const lbl = a.label ? th.fg("accent", `"${a.label}"`) + " " : "";
			const where = a.target ? th.fg("dim", ` → ${a.target}`) : th.fg("dim", " → all");
			lines.push(
				truncateToWidth(
					`${cursor}${lbl}${th.fg("muted", `/${a.pattern}/${a.flags}`)}${where} ${th.fg("dim", `· ${a.cooldownMs}ms cooldown`)}`,
					width,
				),
			);
		});
	}

	invalidate(): void {
		this.cachedWidth = undefined;
		this.cachedLines = undefined;
	}
}

// Guard so a stray Ctrl+Alt+V (or a double /panels) can't stack two live
// panels — which would double the render tick and repaint everything twice.
let panelOpen = false;

// The todos widget lives in a separate extension (separate module graph), so we
// tell it to stand down / re-expand over pi's shared event bus. Captured at
// registration since openPanels only receives ctx, not the ExtensionAPI.
const PANEL_CHANNEL = "tau:panel";
let emitPanelOpen: (open: boolean) => void = () => {};

function openPanels(ctx: ExtensionContext): void {
	if (ctx.mode !== "tui") {
		ctx.ui.notify("Panels viewer is TUI-only.", "warning");
		return;
	}
	if (panelOpen) return;
	panelOpen = true;
	// Tell the todos widget to stand down so the plan list isn't shown twice.
	emitPanelOpen(true);
	ctx.ui.custom<void>((tui, theme, _kb, done) => {
		const comp = new PanelsComponent(ctx, theme, tui, () => {
			panelOpen = false;
			emitPanelOpen(false);
			// Repaint so the todos widget re-expands now that the panel is gone.
			tui.requestRender();
			done();
		});
		comp.startLive();
		return {
			render: (width) => comp.render(width),
			// Every key mutation repaints through comp.touch(); this extra
			// requestRender covers keys comp ignores so focus/echo stays clean.
			handleInput: (data) => {
				comp.handleInput(data);
				tui.requestRender();
			},
			invalidate: () => comp.invalidate(),
		};
	});
}

export function registerPanel(pi: ExtensionAPI): void {
	emitPanelOpen = (open) => pi.events.emit(PANEL_CHANNEL, { open });

	pi.registerCommand("panels", {
		description: "Open the unified Plans / Subagents / Sessions / Alerts panel (vim-ish; q/Esc returns to insert)",
		handler: async (_args, ctx) => openPanels(ctx),
	});

	pi.registerShortcut(Key.ctrlAlt("v"), {
		description: "Open/close the Plans / Subagents / Sessions / Alerts panel",
		handler: async (ctx) => openPanels(ctx),
	});
}
