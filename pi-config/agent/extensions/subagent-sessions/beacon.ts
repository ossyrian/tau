/**
 * Beacon: hooks registered inside each subagent pi process.
 *
 * Active only when PI_SUBAGENT_ID is set (the parent sets it via
 * `tmux new-session -e`). The beacon writes a small JSONL event stream to
 * <STATE_DIR>/beacon-<id>.jsonl that the parent polls:
 *
 *   ready     -> session_start      (the subagent is up and at a prompt)
 *   busy      -> agent_start        (a run started)
 *   settled   -> agent_settled      (the run finished; `text` is the final
 *                                    assistant response, captured verbatim)
 *   shutdown  -> session_shutdown   (the subagent pi is exiting)
 *
 * The parent distinguishes "idle" (last event ready/settled) from "busy"
 * (last event busy) without screen-scraping, and reads the final response
 * straight from the settled event instead of capturing the pane.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { appendBeacon, cap } from "./beacon-store.ts";
import { ensureStateDir } from "./state.ts";

let lastAssistantText = "";
let settledSeq = 0;
let readyWritten = false;

/** Short, human-readable rendering of a tool call for digests and alert matching. */
function toolSummary(toolName: string, input: Record<string, unknown>): string {
	const cmd = typeof input.command === "string" ? input.command : "";
	const path = typeof input.path === "string" ? input.path : "";
	const pattern = typeof input.pattern === "string" ? input.pattern : "";
	switch (toolName) {
		case "bash":
			return cap(cmd, 200);
		case "read":
		case "edit":
		case "write":
		case "ls":
			return path;
		case "grep":
			return pattern ? `/${pattern}/` + (path ? ` in ${path}` : "") : toolName;
		case "find":
			return path || pattern || toolName;
		default:
			return toolName;
	}
}

/** Flatten a tool result's content blocks to a capped text snippet. */
function contentToSnippet(content: unknown, n = 3000): string {
	if (!Array.isArray(content)) return "";
	const texts: string[] = [];
	for (const block of content) {
		if (block && typeof block === "object" && (block as any).type === "text") {
			const t = (block as any).text;
			if (typeof t === "string") texts.push(t);
		}
	}
	return cap(texts.join("\n"), n);
}

export function registerBeacon(pi: ExtensionAPI): void {
	const id = process.env.PI_SUBAGENT_ID;
	if (!id) return;

	ensureStateDir();

	pi.on("session_start", async () => {
		if (readyWritten) return;
		readyWritten = true;
		appendBeacon(id, { event: "ready", ts: Date.now() });
	});

	pi.on("agent_start", async () => {
		lastAssistantText = "";
		appendBeacon(id, { event: "busy", ts: Date.now() });
	});

	pi.on("message_end", async (event: any) => {
		const msg = event?.message;
		if (!msg || msg.role !== "assistant") return;
		const parts: any[] = Array.isArray(msg.content) ? msg.content : [];
		const texts = parts.filter((p) => p && p.type === "text").map((p) => p.text as string);
		if (texts.length === 0) return;
		const text = texts.join("\n");
		lastAssistantText = text;
		appendBeacon(id, { event: "assistant", ts: Date.now(), text: cap(text, 6000) });
	});

	pi.on("tool_result", async (event: any) => {
		const toolName = typeof event?.toolName === "string" ? event.toolName : "tool";
		const input = (event?.input ?? {}) as Record<string, unknown>;
		appendBeacon(id, {
			event: "tool",
			ts: Date.now(),
			toolName,
			summary: toolSummary(toolName, input),
			isError: event?.isError === true,
			snippet: contentToSnippet(event?.content, 3000),
		});
	});

	pi.on("agent_settled", async () => {
		settledSeq += 1;
		appendBeacon(id, { event: "settled", seq: settledSeq, ts: Date.now(), text: lastAssistantText });
	});

	pi.on("session_shutdown", async () => {
		appendBeacon(id, { event: "shutdown", ts: Date.now() });
	});
}
