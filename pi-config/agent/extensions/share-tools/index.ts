/**
 * share-tools — hand files back to the user's host machine.
 *
 * ~/share in the container is bind-mounted from ~/.tau/share on the host, so
 * anything written there appears on the user's machine immediately. This is
 * the ONE sanctioned channel out of the sandbox for files: the container is
 * otherwise isolated, and the user's real project trees aren't reachable
 * unless mounted as workspaces.
 *
 * The tool copies a file (or a whole directory) from anywhere the agent can
 * read into ~/share, optionally under a subpath, and reports the host path the
 * user will see. Writing into ~/share directly with the write/edit tools works
 * too — this tool just makes "give this to the user" a single obvious verb,
 * handles directories, and refuses to clobber silently.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

const SHARE_DIR = process.env.TAU_SHARE_DIR || path.join(os.homedir(), "share");
const HOST_SHARE = "~/.tau/share"; // where it lands on the user's machine

function ok(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details };
}
function err(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details, isError: true };
}

/** Resolve the destination inside SHARE_DIR, rejecting escapes via .. or absolute dest. */
function resolveDest(dest: string | undefined, fallbackName: string): string | null {
	const rel = (dest ?? "").trim() || fallbackName;
	if (path.isAbsolute(rel)) return null;
	const full = path.resolve(SHARE_DIR, rel);
	if (full !== SHARE_DIR && !full.startsWith(SHARE_DIR + path.sep)) return null;
	return full;
}

function hostView(fullPath: string): string {
	const rel = path.relative(SHARE_DIR, fullPath);
	return path.join(HOST_SHARE, rel);
}

export default function (pi: ExtensionAPI): void {
	pi.registerTool({
		name: "share_file",
		label: "Share: file to host",
		description:
			"Copy a file or directory into the host share dropbox (~/.tau/share on the user's machine, ~/share in the container) so the user can pick it up. This is the sanctioned way to hand a deliverable — a report, export, generated artifact, log bundle — back to the user's real machine. Use when the user says \"give me\", \"share\", \"hand me\", \"export\", \"drop this where I can get it\", or otherwise wants a file OUT of the sandbox. For git-tracked project changes, ship through the no-mistakes pipeline instead (see tau-git-shipping).",
		parameters: Type.Object({
			source: Type.String({
				description: "Absolute path (or path relative to cwd) of the file or directory to share.",
			}),
			dest: Type.Optional(
				Type.String({
					description:
						"Optional path under the share dir, e.g. 'reports/q2.md' or a bare filename to rename. Defaults to the source's basename at the top of the share dir. Must stay inside the share dir.",
				}),
			),
			overwrite: Type.Optional(
				Type.Boolean({
					description: "Replace an existing destination. Defaults to false (refuses to clobber).",
				}),
			),
		}),
		async execute(
			_id: string,
			params: { source: string; dest?: string; overwrite?: boolean },
		) {
			const source = path.resolve(params.source);
			let st: fs.Stats;
			try {
				st = fs.statSync(source);
			} catch {
				return err(`Source not found: ${params.source}`);
			}

			const full = resolveDest(params.dest, path.basename(source));
			if (!full) return err("dest must be a relative path that stays inside the share dir.");

			if (fs.existsSync(full) && !params.overwrite)
				return err(
					`${hostView(full)} already exists. Pass overwrite: true to replace it, or choose another dest.`,
				);

			try {
				fs.mkdirSync(path.dirname(full), { recursive: true });
				fs.cpSync(source, full, {
					recursive: st.isDirectory(),
					force: params.overwrite ?? false,
					errorOnExist: !(params.overwrite ?? false),
				});
			} catch (e) {
				return err(`Copy failed: ${(e as Error).message}`);
			}

			const kind = st.isDirectory() ? "directory" : "file";
			return ok(
				`Shared ${kind} to ${hostView(full)} (the user can find it at that path on their machine).`,
				{ hostPath: hostView(full), containerPath: full, kind },
			);
		},
	});

	pi.registerTool({
		name: "share_list",
		label: "Share: list dropbox",
		description:
			"List what's currently in the host share dropbox (~/share in the container). Use to confirm a deliverable landed or to see what you've already handed the user.",
		parameters: Type.Object({}),
		async execute() {
			let entries: fs.Dirent[];
			try {
				entries = fs.readdirSync(SHARE_DIR, { withFileTypes: true });
			} catch {
				return ok(`Share dropbox is empty (${HOST_SHARE}).`);
			}
			if (entries.length === 0) return ok(`Share dropbox is empty (${HOST_SHARE}).`);
			const rows = entries
				.filter((e) => !e.name.startsWith("."))
				.sort((a, b) => a.name.localeCompare(b.name))
				.map((e) => `- ${e.name}${e.isDirectory() ? "/" : ""}`);
			return ok(`Share dropbox (${HOST_SHARE}):\n${rows.join("\n")}`, {
				entries: rows.length,
			});
		},
	});
}
