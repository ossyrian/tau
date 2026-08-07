/**
 * Git guard: mechanical enforcement of tau's git workflow rules.
 *
 * Intercepts bash tool calls and blocks:
 *   1. ANY git command touching ~/share (the host<->container dropbox is not
 *      a git working tree; the user is the git gate for anything there).
 *   2. `git push` to anything other than the `no-mistakes` remote — work
 *      ships through the gate pipeline, not straight to origin.
 *   3. Merges of PRs (`gh pr merge`, merge via `gh api`) — merging is the
 *      user's hand, always.
 *
 * Block reasons re-teach the correct path at the moment it's needed. This
 * bounds accidents, not adversaries — an agent can still script around a
 * regex. GitHub branch protection is the real backstop.
 *
 * Escape hatch: the user (not the agent — slash commands are TUI input) can
 * run `/git-guard off` to disable for the current session, `/git-guard on`
 * to re-enable, `/git-guard` to check status.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { isToolCallEventType } from "@earendil-works/pi-coding-agent";

// The share dropbox: ~/share in the container, bind-mounted from ~/.tau/share
// on the host. Match both the literal path and the ~ form the agent might type.
const SHARE_PATHS = ["/home/pi/share", "/root/share"];
const SHARE_TARGET = "~/share";

/** Split a shell command into segments at unquoted-ish separators. Heuristic:
 * we don't fully parse shell; quoted separators cause over-splitting, which
 * for a guard errs in the safe direction (more scrutiny, not less). */
function splitSegments(command: string): string[] {
	return command
		.split(/(?:&&|\|\||;|\||\n)/)
		.map((s) => s.trim())
		.filter(Boolean);
}

/** Does this segment invoke git? Catches `git`, `command git`, `env X=y git`,
 * absolute paths like /usr/bin/git. */
function isGitInvocation(segment: string): boolean {
	return /(?:^|\s|\/)git\s/.test(` ${segment} `);
}

interface Verdict {
	block: boolean;
	reason?: string;
}

export function analyzeCommand(command: string, cwd: string): Verdict {
	const segments = splitSegments(command);
	const mentionsShare = command.includes(SHARE_TARGET) || SHARE_PATHS.some((p) => command.includes(p));
	const cwdInShare = SHARE_PATHS.some((p) => cwd === p || cwd.startsWith(`${p}/`));

	const anyGit = segments.some(isGitInvocation);

	// Rule 1: no git in the share dropbox — not even read-only. Blocks any
	// command that both invokes git and touches ~/share (cwd or mention), even
	// when the git segment isn't the one naming it (`cd ~/share && git init`).
	// Over-blocks mixed commands like `git log && cat ~/share/x` — fine: the
	// agent can split them. The share dir is a file dropbox, not a repo.
	if (anyGit && (cwdInShare || mentionsShare)) {
		return {
			block: true,
			reason:
				"git is forbidden in ~/share — it's the host dropbox, not a git working tree. " +
				"Use the share_file tool (or just write files into ~/share) to hand things to the user; " +
				"if you genuinely need git on a repo, work in /workspace, not ~/share. " +
				"(If your git command was for a different path, split it from anything mentioning ~/share.)",
		};
	}

	for (const segment of segments) {
		const git = isGitInvocation(segment);

		// Rule 2: pushes go through the no-mistakes gate.
		if (git && /\bpush\b/.test(segment)) {
			// Allowed: pushing to the gate remote.
			const pushesToGate = /\bpush\b[^|;&]*\bno-mistakes\b/.test(segment);
			if (!pushesToGate) {
				return {
					block: true,
					reason:
						"Direct pushes are blocked — ship through the gate instead: commit on a branch, then " +
						'`no-mistakes axi run --intent "<the user\'s goal>"` (or `git push no-mistakes <branch>`). ' +
						"The run ends with a PR open and CI green; stop there and tell the user. " +
						"If this push is genuinely gate-exempt (e.g. first push of a brand-new repo), ask the user — " +
						"they can disable this guard for the session with /git-guard off.",
				};
			}
		}

		// Rule 3: merging is the user's hand.
		if (/\bgh\s+pr\s+merge\b/.test(segment) || (/\bgh\s+api\b/.test(segment) && /\/merge\b/.test(segment))) {
			return {
				block: true,
				reason:
					"Merging PRs is reserved for the user. Your stopping point is the open PR with CI green — " +
					"report it and stop.",
			};
		}
	}

	return { block: false };
}

export default function (pi: ExtensionAPI) {
	let enabled = true;

	pi.registerCommand("git-guard", {
		description: "git-guard [on|off] — toggle or show the git workflow guard for this session",
		handler: async (args, ctx) => {
			const arg = (args ?? "").trim();
			if (arg === "off") {
				enabled = false;
			} else if (arg === "on") {
				enabled = true;
			}
			const status = `git-guard is ${enabled ? "ON" : "OFF"} for this session`;
			if (ctx.hasUI) ctx.ui.notify(status, "info");
		},
	});

	pi.on("tool_call", async (event) => {
		if (!enabled) return;
		if (!isToolCallEventType("bash", event)) return;

		const verdict = analyzeCommand(event.input.command, process.cwd());
		if (verdict.block) {
			return { block: true, reason: verdict.reason };
		}
	});
}
