/** share-tools: give files to the user through ~/share. */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

const SHARE_DIR = path.join(os.homedir(), "share");

function reply(text: string, isError = false) {
	return { content: [{ type: "text" as const, text }], details: {}, ...(isError && { isError }) };
}

export default function (pi: ExtensionAPI): void {
	pi.registerTool({
		name: "share_file",
		label: "Share a file",
		description: "Copy a file or directory into ~/share to give it to the user.",
		parameters: Type.Object({
			source: Type.String({ description: "The file or directory to share." }),
			dest: Type.Optional(Type.String({ description: "The path inside ~/share. Default: the source name." })),
			overwrite: Type.Optional(Type.Boolean({ description: "Replace an existing file. Default: false." })),
		}),
		async execute(_id: string, params: { source: string; dest?: string; overwrite?: boolean }) {
			const source = path.resolve(params.source);
			if (!fs.existsSync(source)) return reply(`Not found: ${params.source}`, true);

			const target = path.resolve(SHARE_DIR, params.dest?.trim() || path.basename(source));
			if (!target.startsWith(SHARE_DIR + path.sep)) return reply("dest must stay inside ~/share.", true);
			if (fs.existsSync(target) && !params.overwrite) {
				return reply(`${target} exists. Set overwrite to replace it.`, true);
			}

			try {
				fs.mkdirSync(path.dirname(target), { recursive: true });
				fs.cpSync(source, target, { recursive: true, force: true });
			} catch (e) {
				return reply(`Copy failed: ${(e as Error).message}`, true);
			}
			return reply(`Shared ${target}`);
		},
	});

	pi.registerTool({
		name: "share_list",
		label: "List shared files",
		description: "List the files in ~/share.",
		parameters: Type.Object({}),
		async execute() {
			const names = fs.existsSync(SHARE_DIR) ? fs.readdirSync(SHARE_DIR).filter((n) => !n.startsWith(".")) : [];
			return reply(names.length ? names.sort().join("\n") : "~/share is empty.");
		},
	});
}
