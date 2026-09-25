// yolo's segment in omp's status line (docs/design/agent-footer.md §2): one status entry, keyed
// "yolo", that omp shows beside its other extension statuses (its `statusLine.showHookStatus`
// setting, on unless you turn it off). This file adds that entry and does nothing else. It calls
// ctx.ui.setStatus and never ctx.ui.setFooter, so omp's stock status line stays (DIR-FT2).
//
// omp loads it from ~/.oh-omp/agent/extensions/yolo-footer/index.js (its native discovery takes
// a direct `*.js` or a subdirectory's `index.js`) and runs its default export with the same
// extension API pi has; packs/pi ships the same file with pi's arguments. The subdirectory is
// yolo's alone on purpose: ~/.oh-omp is workspace state, and the one-time pack-file mountpoint
// migration archives every unclaimed empty file in the directory holding a single-file target,
// so a file placed directly in extensions/ would have that migration scan the user's own
// extensions. Disable it with omp's `disabledExtensions: [extension-module:yolo-footer]`.
// The text comes from
// `yolo internal footer`, the one renderer every agent's footer runs, so this file carries only
// omp's arguments: the agent name the profile table is keyed by (omp's CLI is `oh-omp`), the
// words for omp's own login and for the providers a profile may select, the routes omp reaches
// through the wire bridge, and the template.
//
// FOOTER_ARGS is written as JSON on purpose. internal/footer's adapter tests read the array
// out of this file and run it against the renderer, and TestBridgedRoutesAreMarked checks the
// --bridged list against the shipped providers, so the flags cannot drift from what the
// renderer reads without a test failing.
//
// If yolo is not on PATH, or the renderer prints nothing, no entry is set and omp's status
// line is exactly what it was.
import { execFile } from "node:child_process";

const FOOTER_ARGS = [
	"internal", "footer",
	"--agent", "oh-omp",
	"--login", "omp's own login",
	"--words", "openai-codex=ChatGPT subscription",
	"--words", "bedrock=Bedrock",
	"--bridged", "openai-codex",
	"--template", "yolo: {yolo.billing} · {yolo.notch}"
];

function renderFooter() {
	return new Promise((resolve) => {
		let child;
		try {
			child = execFile("yolo", FOOTER_ARGS, { encoding: "utf8", timeout: 2000 }, (error, stdout) => {
				resolve(error ? "" : String(stdout).trim());
			});
		} catch {
			resolve("");
			return;
		}
		// The template names no stdin field, so the renderer reads none; closing the pipe
		// anyway means a later template that does cannot wait on an open stdin.
		child.stdin?.end();
	});
}

export default function registerYoloFooter(pi) {
	pi.on("session_start", async (_event, ctx) => {
		if (!ctx.hasUI) return;
		const line = await renderFooter();
		if (line) ctx.ui.setStatus("yolo", line);
	});
}
