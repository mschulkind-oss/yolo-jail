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
// (hooks/hooks.json, .mcp.json, monitors/monitors.json, bin/). Mode 0 means 0o644.
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
	{"skills/deep/SKILL.md", "---\nname: deep\ndescription: d\n---\nbody\n", 0},
}

// WriteDefaultLocationPlugin writes that plugin at dir, the plugin root, with a manifest that
// names the plugin and declares NO component field. Every code-running component it carries is
// therefore found by its location alone: hooks, mcpServers, monitors and bin.
func WriteDefaultLocationPlugin(t testing.TB, dir, name string) {
	t.Helper()
	write := func(rel, body string, mode os.FileMode) {
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
	write(".claude-plugin/plugin.json",
		`{"name":"`+name+`","description":"code at default locations only"}`, 0)
	for _, f := range defaultLocationFiles {
		write(f.rel, f.body, f.mode)
	}
}
