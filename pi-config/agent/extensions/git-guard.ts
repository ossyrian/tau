/**
 * Git guard: mechanical enforcement of tau's git workflow rules.
 *
 * Intercepts bash tool calls and blocks:
 *   1. ANY git command touching /workspace/tau_share (the user is the git
 *      gate there, by design).
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

const TAU_SHARE = "tau_share";

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
	const mentionsTauShare = command.includes(TAU_SHARE);
	const cwdInTauShare = cwd.includes(`/${TAU_SHARE}`);

	const anyGit = segments.some(isGitInvocation);

	// Rule 1: no git in tau_share — not even read-only. Blocks any command
	// that both invokes git and touches tau_share (cwd or mention), even when
	// the git segment isn't the one naming it (`cd tau_share && git diff`).
	// Over-blocks mixed commands like `git log && cat tau_share/x` — fine:
	// the agent can split them.
	if (anyGit && (cwdInTauShare || mentionsTauShare)) {
		return {
			block: true,
			reason:
				"git is forbidden in /workspace/tau_share — the user is the gate for every git operation there. " +
				"Do the non-git work, then hand the user the exact commands, files, and commit message to run themselves. " +
				"(If your git command was for a different repo, split it from anything mentioning tau_share.)",
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
