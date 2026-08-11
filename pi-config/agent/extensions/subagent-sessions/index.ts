/**
 * subagent-sessions extension entry point.
 *
 * Every pi in the tau tmux server is identical — they are all just tmux
 * sessions. "Subagent" is not a kind of process; it is a relationship the
 * spawner remembers (the id it handed out). The spawned pi only differs by
 * carrying a beacon return address (PI_SUBAGENT_ID, set via `tmux
 * new-session -e`) so its spawner can hear from it. registerBeacon self-gates
 * on that address, so every session registers the same full surface —
 * management tools, alert poller, and the unified panel — and any session can
 * spawn, watch, and switch between others.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { startAlertPoller, stopAlertPoller } from "./alerts.ts";
import { registerBeacon } from "./beacon.ts";
import { registerManager } from "./manager.ts";
import { registerPanel } from "./panel.ts";
import { loadAlerts } from "./alert-store.ts";

export default function (pi: ExtensionAPI) {
	registerBeacon(pi);
	loadAlerts();
	registerManager(pi);
	registerPanel(pi);
	startAlertPoller(pi);
	pi.on("session_shutdown", async () => stopAlertPoller());
}
