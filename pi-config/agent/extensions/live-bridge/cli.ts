/**
 * Host-facing entry: `tau live status|stop [--keep-inject]` runs this inside
 * the container via `docker exec`. Run with `node --no-warnings cli.ts <cmd>`.
 */

import { describeAll, stopAll } from "./core.ts";

const [cmd = "status", ...rest] = process.argv.slice(2);

if (cmd === "stop") {
	for (const l of await stopAll({ keepInject: rest.includes("--keep-inject") })) console.log(l);
} else if (cmd === "status") {
	for (const l of await describeAll()) console.log(l);
} else {
	console.error("usage: tau live {status|stop [--keep-inject]}");
	process.exit(2);
}
