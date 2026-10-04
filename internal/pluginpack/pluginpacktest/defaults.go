// Package pluginpacktest writes plugin trees for tests. Nothing in a shipped binary imports it.
//
// It exists so the three reports a wrapped plugin's code reaches — the footprint, the launch's
// jail-code line and `yolo host apply`'s delivery lines — are tested against ONE tree, in three
// packages, rather than three hand-written trees that can drift apart.
package pluginpacktest

import (
	"os"
	"path/filepath"
	"testing"
)

// defaultLocationFiles is a plugin whose code sits ONLY where Claude Code looks when the manifest
// is silent: the "Standard layout" table of https://code.claude.com/docs/en/plugins-reference
// (hooks/hooks.json, .mcp.json, monitors/monitors.json, bin/, workflows/). Mode 0 means 0o644.
//
// hooks/hooks.json carries both hook forms: a classic command hook, and a `modules` entry naming
// a JavaScript or TypeScript hooks module (a "mod", Claude Code 2.1.287 or later), which runs
// inside the agent process rather than as a child of it.
var defaultLocationFiles = []struct {
	rel, body string
	mode      os.FileMode
}{
	{"hooks/hooks.json", `{"hooks":{"PostToolUse":[{"matcher":"Write|Edit","hooks":[` +
		`{"type":"command","command":"\"${CLAUDE_PLUGIN_ROOT}/scripts/format.sh\""}]}]},` +
		`"modules":["./register.ts"]}`, 0},
	{"hooks/register.ts", "export default function register() {}\n", 0},
	{"scripts/format.sh", "#!/bin/sh\nexit 0\n", 0o755},
	{".mcp.json", `{"mcpServers":{"db":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server.js"]}}}`, 0},
	{"monitors/monitors.json", `[{"name":"error-log","command":"tail -F ./logs/error.log",` +
		`"description":"Application error log"}]`, 0},
	{"bin/acme-tool", "#!/bin/sh\necho acme\n", 0o755},
	{"workflows/review.js", "export const meta = {name: 'review', description: 'd'}\n", 0},
	{"skills/deep/SKILL.md", "---\nname: deep\ndescription: d\n---\nbody\n", 0},
}

// WriteDefaultLocationPlugin writes that plugin at dir, the plugin root, with a manifest that
// names the plugin and declares NO component field. Every code-running component it carries is
// therefore found by its location alone: hooks, mcpServers, monitors, bin and workflows.
func WriteDefaultLocationPlugin(t testing.TB, dir, name string) {
	t.Helper()
	write(t, dir, ".claude-plugin/plugin.json",
		`{"name":"`+name+`","description":"code at default locations only"}`, 0)
	for _, f := range defaultLocationFiles {
		write(t, dir, f.rel, f.body, f.mode)
	}
}

// EveryDefaultLocation is every component Claude Code loads from a plugin whose manifest names
// none, by the name yolo reports it under, mapped to the plugin-relative location it loads it
// from. Skills are left out: yolo delivers them itself rather than reporting them.
//
// MEASURED against Claude Code 2.1.289's shipped JavaScript (strings of its binary, 2026-10-03):
// the plugin loader stats the directories "commands", "agents", "skills", "output-styles",
// "themes" and "workflows", and reads hooks/hooks.json, monitors/monitors.json and
// settings.json, keeping from settings.json only the keys of the allowlist
// ["agent","subagentStatusLine"]; the MCP and LSP readers take .mcp.json and .lsp.json at the
// plugin root; and the plugin's bin/ goes on the PATH of the Bash tool's shell. Its install
// check names the same set: "expected .claude-plugin/ or a commands/, skills/, agents/, hooks/,
// themes/, output-styles/, monitors/, workflows/, SKILL.md, .mcp.json, or .lsp.json at the top
// level".
var EveryDefaultLocation = map[string]string{
	"hooks":              "hooks/hooks.json",
	"mcpServers":         ".mcp.json",
	"lspServers":         ".lsp.json",
	"monitors":           "monitors/monitors.json",
	"bin":                "bin",
	"workflows":          "workflows",
	"subagentStatusLine": "settings.json",
	"commands":           "commands",
	"agents":             "agents",
	"outputStyles":       "output-styles",
	"themes":             "themes",
	"agent":              "settings.json",
}

// everyOtherDefaultFile is what WriteEveryDefaultLocationPlugin adds to the code-only plugin so
// that every entry of EveryDefaultLocation is present.
var everyOtherDefaultFile = []struct{ rel, body string }{
	{".lsp.json", `{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`},
	{"settings.json", `{"agent":"reviewer",` +
		`"subagentStatusLine":{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/scripts/row.sh"}}`},
	{"commands/status.md", "# status\n"},
	{"agents/reviewer.md", "---\nname: reviewer\ndescription: d\n---\nreview\n"},
	{"output-styles/terse.md", "---\nname: terse\ndescription: d\n---\nbe terse\n"},
	{"themes/dusk.json", `{"name":"dusk","base":"dark","overrides":{}}`},
}

// WriteEveryDefaultLocationPlugin writes WriteDefaultLocationPlugin's plugin plus a file at every
// other location in EveryDefaultLocation, still with a manifest that declares no component.
func WriteEveryDefaultLocationPlugin(t testing.TB, dir, name string) {
	t.Helper()
	WriteDefaultLocationPlugin(t, dir, name)
	for _, f := range everyOtherDefaultFile {
		write(t, dir, f.rel, f.body, 0)
	}
}

// write writes body at dir/rel, creating the parents. Mode 0 means 0o644.
func write(t testing.TB, dir, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}
