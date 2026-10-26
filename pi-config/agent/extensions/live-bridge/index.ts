/**
 * live-bridge — makes impeccable's browser "live mode" work in a harness where
 * the agent only acts inside a tool call or after a user message.
 *
 * Problem: the browser's Go/Accept click is queued on the helper until the
 * agent runs `impeccable live-poll`. If the agent ended its turn, the click
 * sits there until the human types "did you get my feedback?".
 *
 * Fix: while this session is working with live mode, a light watcher checks the
 * helper(s). When an event is queued, no poll is running, and the agent is
 * idle, it injects a user message telling the agent to poll. Events are never
 * consumed here; the agent still handles them through the normal poll loop.
 *
 * Also provides `live_stop` / `/live-stop` to close every running live session
 * (same as the host's `tau live stop`).
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { isToolCallEventType } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { existsSync } from "node:fs";
import { describeAll, findServers, IMPECCABLE, statusOf, stopAll } from "./core.ts";

const TICK_MS = 1500;
/** Don't re-wake for the same unchanged queue more often than this. */
const RENEUDGE_MS = 120_000;
/** A bash command that touches impeccable live mode marks this session as the live owner. */
const LIVE_CMD = /impeccable[^\n]*\blive/;

const TIMER_KEY = "__liveBridgeTimer";
const g = globalThis as Record<string, unknown>;

function ok(text: string) {
	return { content: [{ type: "text" as const, text }], details: {} };
}

export default function (pi: ExtensionAPI) {
	// Only relevant when the impeccable skill is mounted.
	if (!existsSync(IMPECCABLE)) return;

	let owner = false;
	let ctx: ExtensionContext | undefined;
	let busy = false;
	const nudged = new Map<string, { sig: string; at: number }>();

	async function tick() {
		if (!owner || !ctx || busy) return;
		busy = true;
		try {
			if (!ctx.isIdle() || ctx.hasPendingMessages()) return;
			for (const server of findServers()) {
				const st = await statusOf(server);
				if (!st || st.agentPolling || st.pendingEvents.length === 0) continue;
				if (!ctx.isIdle()) return; // agent started something while we awaited
				const sig = JSON.stringify(st.pendingEvents).slice(0, 400);
				const prev = nudged.get(server.root);
				if (prev && prev.sig === sig && Date.now() - prev.at < RENEUDGE_MS) continue;
				nudged.set(server.root, { sig, at: Date.now() });
				pi.sendUserMessage(
					`[live-bridge] Your browser has ${st.pendingEvents.length} queued impeccable live event(s) and nothing is polling. ` +
						`Run \`impeccable live-poll\` (foreground, default timeout) from ${server.root} now to receive it, handle it per the ` +
						`impeccable live reference, reply, and keep polling after every event. Don't wait for the user.`,
				);
				return;
			}
		} catch {
			/* never let the watcher kill the session */
		} finally {
			busy = false;
		}
	}

	pi.on("session_start", async (_e, c) => {
		ctx = c;
		const old = g[TIMER_KEY] as ReturnType<typeof setInterval> | undefined;
		if (old) clearInterval(old);
		const t = setInterval(() => void tick(), TICK_MS);
		t.unref?.();
		g[TIMER_KEY] = t;
	});
	pi.on("session_shutdown", async () => {
		const t = g[TIMER_KEY] as ReturnType<typeof setInterval> | undefined;
		if (t) clearInterval(t);
		delete g[TIMER_KEY];
		ctx = undefined;
	});

	pi.on("tool_call", async (event) => {
		if (isToolCallEventType("bash", event) && LIVE_CMD.test(event.input.command)) owner = true;
	});

	async function stop(keepInject?: boolean) {
		owner = false;
		nudged.clear();
		return (await stopAll({ keepInject })).join("\n");
	}

	pi.registerTool({
		name: "live_stop",
		label: "Live: stop all debug sessions",
		description:
			"Close every running impeccable live-mode session in this container: stops each live helper, strips the injected live.js tag from the project's HTML, and kills stray live-poll processes. Use when the user says to close/stop/end the live or debug session(s), or when live mode is done. Does not touch the app's own dev server. Also reports leftovers worth a manual look.",
		parameters: Type.Object({
			keepInject: Type.Optional(
				Type.Boolean({ description: "Leave the live.js script tag in the HTML (quick restart). Default false." }),
			),
		}),
		async execute(_id: string, params: { keepInject?: boolean }) {
			return ok(await stop(params.keepInject));
		},
	});

	pi.registerCommand("live-stop", {
		description: "Close all running impeccable live/debug sessions",
		handler: async (_args, c) => {
			const text = await stop();
			if (c.hasUI) c.ui.notify(text, "info");
		},
	});

	pi.registerCommand("live-status", {
		description: "Show running impeccable live helpers",
		handler: async (_args, c) => {
			const text = (await describeAll()).join("\n");
			if (c.hasUI) c.ui.notify(text, "info");
		},
	});
}
