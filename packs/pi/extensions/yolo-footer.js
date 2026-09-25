// yolo's segment in pi's footer (docs/design/agent-footer.md §2): one status entry, keyed
// "yolo", that pi's own footer shows beside its other extension statuses. This file adds that
// entry and does nothing else. It calls ctx.ui.setStatus and never ctx.ui.setFooter, which would
// replace pi's stock footer (DIR-FT2).
//
// The text comes from `yolo internal footer`, the one renderer every agent's footer runs, so
// this file carries only pi's arguments: the agent name the profile table is keyed by, the
// words for pi's own login and for the providers pi's profiles select, the routes pi reaches
// through the wire bridge, and the template.
//
// FOOTER_ARGS is written as JSON on purpose. internal/footer's adapter tests read the array
// out of this file and run it against the renderer, and TestBridgedRoutesAreMarked checks the
// --bridged list against the shipped providers, so the flags cannot drift from what the
// renderer reads without a test failing.
//
// If yolo is not on PATH, or the renderer prints nothing, no entry is set and pi's footer is
// exactly what it was.
import { execFile } from "node:child_process";

const FOOTER_ARGS = [
	"internal", "footer",
	"--agent", "pi",
	"--login", "pi's own login",
	"--words", "openai-codex=ChatGPT subscription",
	"--words", "bedrock=Bedrock",
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
