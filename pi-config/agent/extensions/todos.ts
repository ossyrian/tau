/**
 * Todos Extension — a `write_todos` planning tool, adapted from the deepagents
 * TodoListMiddleware design (https://github.com/langchain-ai/deepagents) to pi.
 *
 * What I kept from deepagents:
 *   - One `write_todos` tool that REPLACES the whole list each call (not add/toggle).
 *   - Todo = { content, status: "pending" | "in_progress" | "completed" }.
 *   - Behavioral guidance split into a tool description (when/how/when-not) and
 *     short rules injected into the system prompt.
 *   - Reject parallel `write_todos` calls in one assistant message, because
 *     replace-whole-list + parallel = ambiguous precedence.
 *
 * How it maps onto pi:
 *   - System-prompt injection  -> `promptSnippet` + `promptGuidelines` on the tool.
 *   - Parallel-call rejection  -> `tool_call` event reads the assistant message's
 *                                  ToolCall blocks from ctx.sessionManager.
 *   - Graph state "todos"      -> tool result `details`, replayed from session
 *                                  entries on load. This gives pi something
 *                                  deepagents' graph state does not: the todo
 *                                  list is automatically correct for whichever
 *                                  branch of the session tree you are on.
 *   - Multiple plans           -> optional `plan` name on each call; the most
 *                                  recently written plan is the "active" one.
 *   - Visibility               -> the widget auto-hides when the active plan is
 *                                  fully done and auto-shows when new work
 *                                  appears; ctrl+alt+t toggles it manually.
 *   - Plan cycling             -> ctrl+alt+n / ctrl+alt+b cycle which plan the
 *                                  widget displays; /todos opens a full viewer
 *                                  where ←/→ cycle plans.
 */

import { StringEnum } from "@earendil-works/pi-ai";
import type { AssistantMessage, ToolCall as AiToolCall } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import { Container, Key, matchesKey, Text, truncateToWidth } from "@earendil-works/pi-tui";
import { Type } from "typebox";

// The panel (Ctrl+Alt+V) and this widget are separate extensions with separate
// module graphs, so they coordinate over pi's shared event bus. The panel emits
// PANEL_CHANNEL on open/close; we mirror the open state here and force a widget
// rebuild so the plan list never shows on both surfaces at once.
const PANEL_CHANNEL = "tau:panel";
let panelOpen = false;
const isPanelOpen = () => panelOpen;

type TodoStatus = "pending" | "in_progress" | "completed";

interface Todo {
	content: string;
	status: TodoStatus;
}

interface TodoDetails {
	todos: Todo[];
	plan: string;
}

const STATUS_VALUES = ["pending", "in_progress", "completed"] as const;
const TOOL_NAME = "write_todos";
const DEFAULT_PLAN = "default";

const TodoParams = Type.Object({
	plan: Type.Optional(
		Type.String({
			description:
				"Optional name for this plan. Use distinct names to maintain separate plans for separate objectives " +
				"(e.g. 'refactor-auth', 'fix-tests'). Omit to update the default plan. The most recently written plan " +
				"becomes the active one shown in the UI.",
		}),
	),
	todos: Type.Array(
		Type.Object({
			content: Type.String({ description: "Short, actionable description of the task." }),
			status: StringEnum(STATUS_VALUES, {
				description:
					"pending = not started; in_progress = actively working on it; completed = done.",
			}),
		}),
		{ description: "The FULL todo list for this plan. Each call replaces the entire list — send the whole plan, not just the change." },
	),
});

const PARALLEL_ERROR =
	"Error: The `write_todos` tool should never be called multiple times in parallel. " +
	"Please call it only once per turn to update the todo list.";

const TOOL_DESCRIPTION = `Maintain a structured todo list for the current task. Each call REPLACES the entire list for the named plan — send the full, current plan every time, not just the change. Use this to break down and track multi-step work.

## When to use
- The task has 3 or more distinct steps or multiple objectives.
- The user asks for a plan, a breakdown, or a todo list.
- The user gives several tasks at once (numbered or comma-separated).
- The plan will likely change as early steps reveal new tasks.

## When NOT to use
- A single straightforward task.
- Fewer than 3 trivial steps.
- Purely conversational or informational requests.
If the task is simple, just do it directly — don't call this tool.

## How to use
1. Mark a task in_progress BEFORE you start working on it.
2. Mark it completed the moment it's done — don't batch completions.
3. Revise freely: add newly discovered tasks, drop ones no longer relevant, reorder. Don't edit already-completed tasks.
4. Keep at least one task in_progress unless everything is completed.
5. Only mark completed when fully done. If blocked or partial, leave it in_progress and add a task for the blocker.

## Multiple plans
Pass \`plan\` to keep a separate list for a distinct objective (e.g. "refactor-auth", "fix-tests"). Omit it to update the default plan. The plan you write becomes the active one shown to the user; switch between plans by naming the one you are updating. Keep one \`write_todos\` call per turn.

## Output
This tool only tracks progress — it does not answer the user. Deliver the actual result (data, summary, code, analysis) as text in the message AFTER your last write_todos call.`;

const GUIDELINES = [
	"Use write_todos when a task breaks into 3+ steps or multiple objectives, or when the user asks for a plan or todo list. Skip it for trivial few-step work.",
	"Each write_todos call replaces the whole list for that plan — always send the full current plan, never just the delta.",
	"With write_todos, mark a task in_progress before starting it and completed the instant it's done; don't batch completions.",
	"Keep at least one task in_progress unless all are completed; never mark a task completed while it's blocked or partial.",
	"Call write_todos at most once per turn — parallel calls are rejected because they would conflict.",
	"Pass write_todos a `plan` name to maintain a separate list per distinct objective; the plan you write becomes the active one shown to the user.",
	"write_todos only tracks progress. Give the real answer in the message after your last write_todos call.",
];

/** In-memory state; rebuilt from session entries so it matches the current branch. */
let plans = new Map<string, Todo[]>();
let activePlan = DEFAULT_PLAN;
/** Which plan the widget displays. null = follow the active plan. */
let viewedPlan: string | null = null;
/** Master visibility flag. Auto-managed from completion state; user can toggle. */
let expanded = true;

const normalize = (t: Partial<Todo>): Todo | null => {
	const content = String(t.content ?? "").trim();
	const status = STATUS_VALUES.includes(t.status as TodoStatus) ? (t.status as TodoStatus) : "pending";
	if (!content) return null;
	return { content, status };
};

const listHasIncomplete = (list: Todo[]): boolean =>
	list.length > 0 && list.some((t) => t.status !== "completed");

const planLabel = (name: string): string => (name === DEFAULT_PLAN ? "Plan" : name);

const planNamesInOrder = (): string[] => [...plans.keys()];

const displayedList = (): Todo[] => plans.get(viewedPlan ?? activePlan) ?? [];

/** Plain-text rendering sent back to the LLM as the tool result. */
const renderListText = (list: Todo[], planName: string): string => {
	const label = planName === DEFAULT_PLAN ? "Todo list" : `Plan "${planName}"`;
	if (list.length === 0) return `${label} is empty.`;
	const done = list.filter((t) => t.status === "completed").length;
	const inProg = list.filter((t) => t.status === "in_progress").length;
	const pending = list.length - done - inProg;
	const header = `${label} — ${done} completed, ${inProg} in_progress, ${pending} pending (${list.length} total):`;
	const lines = list.map((t, i) => {
		const mark = t.status === "completed" ? "x" : t.status === "in_progress" ? "~" : " ";
		return `  [${mark}] ${i + 1}. ${t.content}`;
	});
	return [header, ...lines].join("\n");
};

const statusIcon = (status: TodoStatus, theme: Theme): string =>
	status === "completed"
		? theme.fg("success", "✓")
		: status === "in_progress"
			? theme.fg("accent", "●")
			: theme.fg("dim", "○");

const statusText = (t: Todo, theme: Theme): string =>
	t.status === "completed"
		? theme.fg("dim", t.content)
		: t.status === "in_progress"
			? theme.fg("text", t.content)
			: theme.fg("muted", t.content);

/**
 * Build the live plan widget shown above the editor. With one plan it is a
 * single header plus its todos. With several plans it becomes a row per plan
 * (● active / ○ inactive, ▸ on the one in view) and only the viewed plan's
 * todos expand beneath it — the same collapse-all-but-viewed model as the
 * panel's Plans tab, so an inactive plan never renders permanently expanded.
 */
// Latest UI-bearing context, captured so the event-bus handler (which gets no
// ctx of its own) can refresh the widget when the panel opens or closes.
let lastCtx: ExtensionContext | undefined;

const buildWidget = (theme: Theme): Container => {
	const c = new Container();
	const names = planNamesInOrder();
	const viewing = viewedPlan ?? activePlan;

	// The panel (Ctrl+Alt+V) is the full plan view when open; the widget defers to
	// it so the plan list isn't rendered on both surfaces at once.
	if (isPanelOpen()) {
		c.addChild(new Text(theme.fg("dim", "Plans shown in the panel · Ctrl+Alt+V to close"), 0, 0));
		return c;
	}

	const renderTodos = (list: Todo[], indent: string) => {
		for (const t of list) {
			c.addChild(new Text(`${indent}${statusIcon(t.status, theme)} ${statusText(t, theme)}`, 0, 0));
		}
	};

	if (names.length <= 1) {
		const list = plans.get(viewing) ?? [];
		const done = list.filter((t) => t.status === "completed").length;
		let header = theme.fg("accent", "● ") + theme.fg("accent", theme.bold(planLabel(viewing)));
		header += theme.fg("success", "  active");
		header += theme.fg("text", `   ${done}/${list.length} done`);
		c.addChild(new Text(header, 0, 0));
		renderTodos(list, "  ");
		c.addChild(new Text(theme.fg("dim", "Ctrl+Alt+V · open panel"), 0, 0));
		return c;
	}

	for (const name of names) {
		const list = plans.get(name) ?? [];
		const done = list.filter((t) => t.status === "completed").length;
		const cursor = name === viewing ? theme.fg("accent", "▸ ") : "  ";
		const dot = name === activePlan ? theme.fg("success", "● active") : theme.fg("dim", "○ inactive");
		const label = name === viewing ? theme.fg("accent", theme.bold(planLabel(name))) : theme.fg("muted", planLabel(name));
		c.addChild(new Text(`${cursor}${dot}  ${label}  ${theme.fg("dim", `${done}/${list.length} done`)}`, 0, 0));
		if (name === viewing) renderTodos(list, "      ");
	}
	c.addChild(new Text(theme.fg("dim", "Ctrl+Alt+V · open panel"), 0, 0));
	return c;
};

const buildCompactWidget = (theme: Theme): Container => {
	const c = new Container();
	const names = planNamesInOrder();
	if (names.length === 0) return c;

	const completedCount = names.filter(
		(n) => !listHasIncomplete(plans.get(n) ?? []) && (plans.get(n)?.length ?? 0) > 0,
	).length;
	const inProgressCount = names.filter(
		(n) => (plans.get(n) ?? []).some((t) => t.status === "in_progress"),
	).length;

	const activeTag = theme.bold(theme.fg("accent", planLabel(activePlan))) + theme.fg("dim", " active");
	const parts = [activeTag];
	if (inProgressCount > 0) {
		parts.push(theme.fg("accent", `${inProgressCount} in progress`));
	}
	if (completedCount > 0) {
		parts.push(theme.fg("success", `${completedCount} completed`));
	}
	const line = `${theme.fg("accent", "●")} ${parts.join(theme.fg("dim", " · "))}`;
	c.addChild(new Text(line, 0, 0));
	return c;
};

const refreshWidget = (ctx: ExtensionContext) => {
	if (!ctx.hasUI) return;
	lastCtx = ctx;
	if (plans.size === 0) {
		ctx.ui.setWidget(TOOL_NAME, undefined);
		return;
	}
	if (expanded) {
		const list = displayedList();
		if (list.length === 0) {
			ctx.ui.setWidget(TOOL_NAME, (_tui, theme) => buildCompactWidget(theme), { placement: "aboveEditor" });
			return;
		}
		ctx.ui.setWidget(TOOL_NAME, (_tui, theme) => buildWidget(theme), { placement: "aboveEditor" });
	} else {
		ctx.ui.setWidget(TOOL_NAME, (_tui, theme) => buildCompactWidget(theme), { placement: "aboveEditor" });
	}
};

/** Cycle which plan the widget displays. dir = +1 / -1. Wraps around. */
const cycleViewedPlan = (ctx: ExtensionContext, dir: 1 | -1) => {
	if (!ctx.hasUI) return;
	const names = planNamesInOrder();
	if (names.length === 0) {
		ctx.ui.notify("No plans yet.", "info");
		return;
	}
	if (names.length < 2) {
		ctx.ui.notify(`Only one plan: ${planLabel(names[0])}`, "info");
		return;
	}
	const current = viewedPlan ?? activePlan;
	let idx = names.indexOf(current);
	if (idx === -1) idx = names.indexOf(activePlan);
	if (idx === -1) idx = 0;
	idx = (idx + dir + names.length) % names.length;
	viewedPlan = names[idx];
	// Landing back on the active plan means "follow active" again.
	if (viewedPlan === activePlan) viewedPlan = null;
	expanded = true;
	refreshWidget(ctx);
	const list = plans.get(viewedPlan ?? activePlan) ?? [];
	const done = list.filter((t) => t.status === "completed").length;
	ctx.ui.notify(`Viewing ${planLabel(viewedPlan ?? activePlan)} — ${done}/${list.length} done`, "info");
};

const toggleExpanded = (ctx: ExtensionContext) => {
	if (!ctx.hasUI) return;
	if (plans.size === 0) {
		ctx.ui.notify("No plans yet.", "info");
		return;
	}
	expanded = !expanded;
	refreshWidget(ctx);
	ctx.ui.notify(expanded ? "Todos expanded" : "Todos collapsed", "info");
};

/** Replay state from the current branch's tool results (last write_todos wins). */
const reconstructState = (ctx: ExtensionContext) => {
	plans = new Map();
	activePlan = DEFAULT_PLAN;
	viewedPlan = null;
	for (const entry of ctx.sessionManager.getBranch()) {
		if (entry.type !== "message") continue;
		const msg = entry.message;
		if (msg.role !== "toolResult" || msg.toolName !== TOOL_NAME) continue;
		const details = msg.details as TodoDetails | undefined;
		const planName = (details?.plan ?? DEFAULT_PLAN) || DEFAULT_PLAN;
		if (Array.isArray(details?.todos)) {
			plans.set(
				planName,
				details.todos.map(normalize).filter((t): t is Todo => t !== null),
			);
			activePlan = planName;
		}
	}
	expanded = listHasIncomplete(plans.get(activePlan) ?? []);
	refreshWidget(ctx);
};

/**
 * Count `write_todos` ToolCall blocks in the assistant message currently being
 * executed. ctx.sessionManager is current through that message when tool_call
 * fires, so every sibling write_todos handler sees the same count and we can
 * reject all parallel calls (matching deepagents' after_model check).
 */
const countWriteTodosInCurrentAssistant = (ctx: ExtensionContext): number => {
	const branch = ctx.sessionManager.getBranch();
	for (let i = branch.length - 1; i >= 0; i--) {
		const entry = branch[i];
		if (entry.type !== "message") continue;
		if (entry.message.role !== "assistant") continue;
		const content = (entry.message as AssistantMessage).content;
		return content.filter(
			(b): b is AiToolCall => b.type === "toolCall" && b.name === TOOL_NAME,
		).length;
	}
	return 0;
};

/** Full-screen viewer for the /todos command. ←/→ cycle plans when >1 exist. */
class TodoViewerComponent {
	private theme: Theme;
	private onClose: () => void;
	private selection: string;
	private cachedWidth?: number;
	private cachedLines?: string[];

	constructor(theme: Theme, onClose: () => void) {
		this.theme = theme;
		this.onClose = onClose;
		this.selection = viewedPlan ?? activePlan;
	}

	private list(): Todo[] {
		return plans.get(this.selection) ?? [];
	}

	handleInput(data: string): void {
		if (matchesKey(data, "escape") || matchesKey(data, "ctrl+c")) {
			this.onClose();
			return;
		}
		if (planNamesInOrder().length > 1) {
			if (matchesKey(data, "right")) {
				this.shift(1);
				return;
			}
			if (matchesKey(data, "left")) {
				this.shift(-1);
				return;
			}
		}
	}

	private shift(dir: 1 | -1): void {
		const names = planNamesInOrder();
		let idx = names.indexOf(this.selection);
		if (idx === -1) idx = 0;
		idx = (idx + dir + names.length) % names.length;
		this.selection = names[idx];
		this.invalidate();
	}

	render(width: number): string[] {
		if (this.cachedLines && this.cachedWidth === width) return this.cachedLines;
		const th = this.theme;
		const names = planNamesInOrder();
		const list = this.list();
		const lines: string[] = [];

		lines.push("");
		const title = th.fg("accent", " Todos ");
		const header = th.fg("borderMuted", "─".repeat(3)) + title + th.fg("borderMuted", "─".repeat(Math.max(0, width - 10)));
		lines.push(truncateToWidth(header, width));
		lines.push("");

		if (plans.size === 0) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "No todos. Ask the agent to plan a multi-step task.")}`, width));
		} else {
			const done = list.filter((t) => t.status === "completed").length;
			const isActive = this.selection === activePlan;
			const tag = isActive ? th.fg("accent", " active") : th.fg("dim", " viewing");
			lines.push(
				truncateToWidth(
					`  ${th.fg("muted", planLabel(this.selection))}${tag}  ${th.fg("dim", `${done}/${list.length} done`)}`,
					width,
				),
			);
			lines.push("");
			list.forEach((t, i) => {
				const line = `  ${statusIcon(t.status, th)} ${th.fg("dim", `${i + 1}.`)} ${statusText(t, th)}`;
				lines.push(truncateToWidth(line, width));
			});
			if (names.length > 1) {
				lines.push("");
				const summary = names
					.map((n) => (n === activePlan ? `${planLabel(n)}*` : planLabel(n)))
					.join("  ·  ");
				lines.push(truncateToWidth(`  ${th.fg("dim", `Plans: ${summary}`)}`, width));
			}
		}

		lines.push("");
		lines.push(truncateToWidth(`  ${th.fg("dim", "ctrl+alt+t toggle on-screen list")}`, width));
		if (names.length > 1) {
			lines.push(truncateToWidth(`  ${th.fg("dim", "← / → cycle plans · Esc to close")}`, width));
		} else {
			lines.push(truncateToWidth(`  ${th.fg("dim", "Esc to close")}`, width));
		}
		lines.push("");

		this.cachedWidth = width;
		this.cachedLines = lines;
		return lines;
	}

	invalidate(): void {
		this.cachedWidth = undefined;
		this.cachedLines = undefined;
	}
}

export default function (pi: ExtensionAPI) {
	// When the panel opens it becomes the authoritative plan view; rebuild the
	// widget so buildWidget re-reads isPanelOpen() and collapses to a hint.
	pi.events.on(PANEL_CHANNEL, (data) => {
		panelOpen = (data as { open?: boolean } | undefined)?.open === true;
		if (lastCtx) refreshWidget(lastCtx);
	});

	// Replay state on (re)load and on branch navigation.
	pi.on("session_start", async (_event, ctx) => reconstructState(ctx));
	pi.on("session_tree", async (_event, ctx) => reconstructState(ctx));
	pi.on("session_shutdown", async (_event, ctx) => {
		if (ctx.hasUI) ctx.ui.setWidget(TOOL_NAME, undefined);
	});

	// Reject parallel write_todos calls within one assistant message.
	pi.on("tool_call", async (event, ctx) => {
		if (event.toolName !== TOOL_NAME) return;
		if (countWriteTodosInCurrentAssistant(ctx) > 1) {
			return { block: true, reason: PARALLEL_ERROR };
		}
	});

	pi.registerTool({
		name: TOOL_NAME,
		label: "Todos",
		description: TOOL_DESCRIPTION,
		promptSnippet:
			"Track a multi-step task as a todo list (pending / in_progress / completed); each call replaces the whole list for a named plan",
		promptGuidelines: GUIDELINES,
		parameters: TodoParams,

		async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
			const next: Todo[] = (params.todos ?? [])
				.map((t) => normalize(t))
				.filter((t): t is Todo => t !== null);
			const planName =
				typeof params.plan === "string" && params.plan.trim() ? params.plan.trim() : DEFAULT_PLAN;

			const wasMulti = plans.size > 1;
			const prevList = plans.get(planName) ?? [];
			const becameComplete = listHasIncomplete(prevList) && !listHasIncomplete(next) && next.length > 0;

			plans.set(planName, next);
			activePlan = planName;
			viewedPlan = null; // follow the active plan again
			expanded = listHasIncomplete(next);
			refreshWidget(ctx);

			if (ctx.hasUI) {
				if (!wasMulti && plans.size > 1) {
					ctx.ui.notify("Multiple plans — ctrl+alt+n / ctrl+alt+b to switch, ctrl+alt+t to toggle", "info");
				} else if (becameComplete) {
					ctx.ui.notify("Plan complete — ctrl+alt+t to expand", "info");
				}
			}

			return {
				content: [{ type: "text", text: renderListText(next, planName) }],
				details: { todos: [...next], plan: planName } as TodoDetails,
			};
		},

		renderCall(args, theme) {
			const n = args.todos?.length ?? 0;
			const plan =
				typeof args.plan === "string" && args.plan.trim() ? args.plan.trim() : DEFAULT_PLAN;
			const planPart = plan !== DEFAULT_PLAN ? theme.fg("dim", ` · ${plan}`) : "";
			return new Text(
				theme.fg("toolTitle", theme.bold("write_todos ")) +
					theme.fg("muted", `${n} item${n === 1 ? "" : "s"}`) +
					planPart,
				0,
				0,
			);
		},

		renderResult(result, { expanded }, theme) {
			const details = result.details as TodoDetails | undefined;
			if (!details || details.todos.length === 0) {
				const block = result.content[0];
				return new Text(block?.type === "text" ? block.text : "", 0, 0);
			}
			const list = details.todos;
			const done = list.filter((t) => t.status === "completed").length;
			const planPart =
				details.plan && details.plan !== DEFAULT_PLAN ? theme.fg("dim", ` · ${details.plan}`) : "";
			let out = theme.fg("muted", `${done}/${list.length} done`) + planPart;
			const show = expanded ? list : list.slice(0, 6);
			for (const t of show) {
				out += `\n  ${statusIcon(t.status, theme)} ${statusText(t, theme)}`;
			}
			if (!expanded && list.length > 6) {
				out += `\n  ${theme.fg("dim", `… ${list.length - 6} more`)}`;
			}
			return new Text(out, 0, 0);
		},
	});

	pi.registerCommand("todos", {
		description: "Show the current todo list (cycle plans with ←/→)",
		handler: async (_args, ctx) => {
			if (ctx.mode !== "tui") {
				const list = plans.get(activePlan) ?? [];
				ctx.ui.notify(renderListText(list, activePlan), "info");
				return;
			}
			await ctx.ui.custom<void>((_tui, theme, _kb, done) => {
				return new TodoViewerComponent(theme, () => done());
			});
		},
	});

	// Toggle widget expanded / compact.
	pi.registerShortcut(Key.ctrlAlt("t"), {
		description: "Toggle todo list expanded / compact",
		handler: async (ctx) => toggleExpanded(ctx),
	});

	// Cycle which plan the widget displays.
	pi.registerShortcut(Key.ctrlAlt("n"), {
		description: "Show next todo plan",
		handler: async (ctx) => cycleViewedPlan(ctx, 1),
	});
	pi.registerShortcut(Key.ctrlAlt("b"), {
		description: "Show previous todo plan",
		handler: async (ctx) => cycleViewedPlan(ctx, -1),
	});
}
