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
import { appendBeacon, ensureStateDir } from "./shared.ts";

let lastAssistantText = "";
let settledSeq = 0;
let readyWritten = false;

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
		if (texts.length > 0) lastAssistantText = texts.join("\n");
	});

	pi.on("agent_settled", async () => {
		settledSeq += 1;
		appendBeacon(id, { event: "settled", seq: settledSeq, ts: Date.now(), text: lastAssistantText });
	});

	pi.on("session_shutdown", async () => {
		appendBeacon(id, { event: "shutdown", ts: Date.now() });
	});
}
