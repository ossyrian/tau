// Replace pi's startup header with a "TAU" banner, only inside the tau container.

import { existsSync } from "node:fs";
import type { ExtensionAPI, Theme } from "@earendil-works/pi-coding-agent";
import { VERSION } from "@earendil-works/pi-coding-agent";

// /etc/tau/tmux.conf is created by the Dockerfile; absent when pi runs on the host.
const IN_TAU = existsSync("/etc/tau/tmux.conf");

function tauBanner(theme: Theme): string[] {
	const a = (text: string) => theme.fg("accent", text);
	return [
		"",
		a("  ████████╗  █████╗   ██╗   ██╗"),
		a("  ╚══██╔══╝ ██╔══██╗  ██║   ██║"),
		a("     ██║    ███████║  ██║   ██║"),
		a("     ██║    ██╔══██║  ██║   ██║"),
		a("     ██║    ██║  ██║  ╚██████╔╝"),
		a("     ╚═╝    ╚═╝  ╚═╝   ╚═════╝ "),
		"",
	];
}

export default function (pi: ExtensionAPI) {
	if (!IN_TAU) return;

	pi.on("session_start", async (_event, ctx) => {
		if (ctx.mode !== "tui") return;

		const apply = () =>
			ctx.ui.setHeader((_tui, theme) => ({
				render(_width: number): string[] {
					const subtitle = `${theme.fg("muted", "  the tau sandbox")}${theme.fg("dim", ` · pi v${VERSION}`)}`;
					return [...tauBanner(theme), subtitle];
				},
				invalidate() {},
			}));

		// setHeader is last-writer-wins in load order; a theme package's header may
		// load after us. Re-apply on the next tick so tau's lands last.
		apply();
		setTimeout(apply, 0);
	});
}
