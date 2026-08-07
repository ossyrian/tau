/**
 * workspace-tools — read-only visibility into the tau workspaces.
 *
 * A workspace at /workspace/<name> (added host-side with `tau workspace add`)
 * is either a live bind into the user's real project or a copy (snapshot) in
 * tau's staging dir. This extension only READS them: it never creates, removes,
 * or edits a workspace. Adding/removing is the user's job via the tau CLI, by
 * design — the agent must not delete its own workspaces.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as fs from "node:fs";
import * as path from "node:path";

const ROOT = "/workspace";

function ok(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details };
}
function err(text: string, details: Record<string, unknown> = {}) {
	return { content: [{ type: "text" as const, text }], details, isError: true };
}

function listWorkspaces(): string[] {
	try {
		return fs
			.readdirSync(ROOT, { withFileTypes: true })
			.filter((d: fs.Dirent) => d.isDirectory())
			.map((d: fs.Dirent) => d.name)
			.sort();
	} catch {
		return [];
	}
}

/**
 * A workspace is either a live bind into the user's real project, or a copy
 * (snapshot) sitting in tau's workspace staging dir. A live bind has its own
 * entry in /proc/self/mountinfo with a root field pointing at the host project
 * path; a copy has no entry of its own and is surfaced through the parent
 * /workspace mount. Edits to a live bind reach the original project; edits to a
 * copy do not.
 */
function mountInfoFor(
	mountPoint: string,
): { root: string; source: string; fsType: string } | null {
	try {
		const data = fs.readFileSync("/proc/self/mountinfo", "utf8");
		for (const line of data.split("\n")) {
			if (!line) continue;
			const parts = line.split(/\s+/);
			const sep = parts.indexOf("-");
			if (sep < 0) continue;
			// fields: 0:id 1:parentId 2:major:minor 3:root 4:mountPoint 5:opts ... - fsType source superOpts
			if (parts[4] === mountPoint) {
				return {
					root: parts[3] ?? "",
					source: parts[sep + 2] ?? "",
					fsType: parts[sep + 1] ?? "",
				};
			}
		}
	} catch {
		/* not linux / no mountinfo — treat as unknown (copy) */
	}
	return null;
}

function workspaceKind(name: string): { kind: "live" | "copy"; hostPath?: string } {
	const info = mountInfoFor(path.join(ROOT, name));
	if (info) return { kind: "live", hostPath: info.root };
	return { kind: "copy" };
}

export default function (pi: ExtensionAPI): void {
	pi.registerTool({
		name: "workspace_list",
		label: "Workspace: list",
		description:
			"List the project workspaces under /workspace/<name> (read-only), noting whether each is a live bind into the user's project or a copy (snapshot). Added host-side with `tau workspace add`; \"go to <name>\" means work under /workspace/<name>.",
		parameters: Type.Object({}),
		async execute() {
			const ws = listWorkspaces();
			if (ws.length === 0)
				return ok("No workspaces under /workspace. Ask the user to run `tau workspace add`.");
			const rows = ws.map((n) => {
				let count = 0;
				try {
					count = fs.readdirSync(path.join(ROOT, n)).length;
				} catch {
					/* unreadable — report as 0 */
				}
				const { kind, hostPath } = workspaceKind(n);
				const tail = kind === "live" && hostPath ? `live bind → ${hostPath}` : "copy";
				return `- ${n}  (/workspace/${n}, ${tail}, ${count} entries)`;
			});
			return ok(rows.join("\n"), {
				workspaces: ws.map((n) => ({ name: n, ...workspaceKind(n) })),
			});
		},
	});

	pi.registerTool({
		name: "workspace_path",
		label: "Workspace: path",
		description:
			'Resolve a workspace name to its absolute path under /workspace, verifying it exists (read-only), and report whether it is a live bind or a copy. Use when the user says "go to <name>" to get the directory to work in.',
		parameters: Type.Object({
			name: Type.String({ description: "Workspace name (the <name> in /workspace/<name>)." }),
		}),
		async execute(_id: string, params: { name: string }) {
			const p = path.join(ROOT, params.name);
			if (p !== ROOT && !p.startsWith(ROOT + "/"))
				return err(`Invalid workspace name '${params.name}'.`);
			try {
				if (!fs.statSync(p).isDirectory())
					return err(`'${params.name}' is not a workspace directory.`);
			} catch {
				return err(`No workspace '${params.name}'. Run workspace_list to see what exists.`);
			}
			const { kind, hostPath } = workspaceKind(params.name);
			const tail = kind === "live" && hostPath ? ` (${kind} bind → ${hostPath})` : " (copy)";
			return ok(p + tail, { path: p, name: params.name, kind, hostPath });
		},
	});
}
