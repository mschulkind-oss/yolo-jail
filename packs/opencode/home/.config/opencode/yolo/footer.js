// yolo's segment in opencode's prompt row (docs/reference/agent-footer.md, "Each agent's adapter"): a TUI plugin that
// adds one text element, `yolo: <notch>`, at the right end of the row where opencode's prompt
// names the model and its provider. It shows the notch alone because that row already names
// the provider ("Each agent's adapter"'s table). It adds and never replaces (DIR-FT2): both slots it fills are
// APPEND slots, which keep the host's own content and add a plugin's after it, while opencode's
// `replace` and `single_winner` slots put a plugin's content where opencode's own was.
//
// HOW OPENCODE FINDS IT (opencode 1.18.32, read from the shipped `@opencode-ai/plugin` types
// and the binary's loader): the TUI loads the specs in the `plugin` lists of tui.json and
// tui.jsonc, each resolved against its file's directory. The opencode pack's tui surface lists
// "./yolo/footer.js" in ~/.config/opencode/tui.jsonc (never tui.json, whose existence stops
// opencode's one-time move of a theme out of opencode.json), and the pack's `files`
// contribution delivers this file there, in a directory only yolo uses.
// Kept out of ~/.config/opencode/plugin(s)/, which opencode's SERVER scans for server plugins.
//
// THE MODULE CONTRACT: a file plugin default-exports `{ id, tui }`. The id is required for a
// path plugin, and a module exporting both `server` and `tui` is refused. `tui(api)` registers
// slot renderers through `api.slots.register`, and a renderer returns an OpenTUI element.
// `@opentui/solid/jsx-runtime` is not installed anywhere: opencode maps that specifier, for
// any plugin file outside node_modules, to the copy compiled into its own binary. So `jsx()`
// here builds the same element a compiled `<text>` would, with no JSX transform.
//
// FOOTER_ARGS is written as JSON on purpose, as in the pi and omp extensions: internal/footer's
// adapter tests read the array out of this file and run it against the renderer.
//
// If yolo is not on PATH, or the renderer prints nothing, no slot is registered and opencode's
// prompt row is exactly what it was.
import { execFile } from "node:child_process";
import { jsx } from "@opentui/solid/jsx-runtime";

const FOOTER_ARGS = [
	"internal", "footer",
	"--agent", "opencode",
	"--template", "yolo: {yolo.setup}"
];

// The right end of the prompt's info row: on the home screen, and in a session.
const SLOTS = ["home_prompt_right", "session_prompt_right"];

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

// tui() does not await the renderer: opencode's loader waits on the plugin's tui() before the TUI
// is usable, and a cold `yolo internal footer` costs ~1.7 s. The slots are registered once the
// renderer answers, and only if it printed a line, so a plugin that never answers registers
// nothing. The plugin keeps no session state: its slots are the prompt row's, not a session's, so
// there is no superseded or shut-down session whose line would need dropping.
export default {
	id: "yolo-footer",
	tui(api) {
		void renderFooter().then((line) => {
			if (!line) return;
			const slots = {};
			for (const name of SLOTS) {
				slots[name] = (ctx) => {
					// The muted color opencode gives the provider name in the same row. A theme that
					// does not answer leaves the text in the default color.
					const muted = ctx?.theme?.current?.textMuted;
					return jsx("text", muted === undefined ? { children: line } : { fg: muted, children: line });
				};
			}
			api.slots.register({ slots });
		});
	},
};
