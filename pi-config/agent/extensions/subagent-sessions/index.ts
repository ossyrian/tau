/**
 * subagent-sessions extension entry point.
 *
 * One file is loaded by both the parent pi and every subagent pi it spawns.
 * The PI_SUBAGENT_ID env var (set on `tmux new-session -e`) selects the role:
 *   - present  -> this process IS a subagent; register only the beacon hooks.
 *   - absent   -> this process is a parent; register the management tools,
 *                 the alert poller, and the unified panel.
 *
 * Keeping the beacon in the same module guarantees a subagent always reports
 * back to its parent, with no extra install step.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { startAlertPoller, stopAlertPoller } from "./alerts.ts";
import { registerBeacon } from "./beacon.ts";
import { registerManager } from "./manager.ts";
import { loadAlerts } from "./alert-store.ts";

export default function (pi: ExtensionAPI) {
	if (process.env.PI_SUBAGENT_ID) {
		registerBeacon(pi);
		return;
	}
	loadAlerts();
	registerManager(pi);
	startAlertPoller(pi);
	pi.on("session_shutdown", async () => stopAlertPoller());
}
