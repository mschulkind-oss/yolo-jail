package pluginpacktest

import (
	"strings"
	"testing"
)

// EveryDeclaredPath is every component whose manifest field names plugin paths, by the name yolo
// reports it under, mapped to a file at the location WriteEveryDeclaredPathPlugin's manifest names
// for it. No location is a component's default one, nor spelled like its field, so a file here is
// left out of a root skill's flat copy only because the manifest names its path.
var EveryDeclaredPath = map[string]string{
	"hooks":        "hk/hooks.json",
	"mcpServers":   "servers.json",
	"lspServers":   "langs.json",
	"monitors":     "watch/monitors.json",
	"workflows":    "flows/review.js",
	"commands":     "cmds/status.md",
	"agents":       "crew/reviewer.md",
	"outputStyles": "styles/terse.md",
	"themes":       "looks/dusk.json",
}

// everyDeclaredPathManifest names each EveryDeclaredPath location in the manifest field Claude
// Code reads it from, beside `skills: ["./"]`, the layout the plugin scaffolder emits, in which
// the plugin's root is itself a skill.
const everyDeclaredPathManifest = `{"name":"%NAME%","skills":["./"],
	"hooks":"./hk/hooks.json","mcpServers":"./servers.json","lspServers":"./langs.json",
	"monitors":"./watch/monitors.json","workflows":["./flows"],"commands":"./cmds",
	"agents":["./crew"],"outputStyles":["./styles"],"themes":"./looks"}`

var everyDeclaredPathFile = map[string]string{
	"SKILL.md":            "---\nname: %NAME%\ndescription: d\n---\nroot skill\n",
	"hk/hooks.json":       `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
	"servers.json":        `{"mcpServers":{"db":{"command":"node"}}}`,
	"langs.json":          `{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`,
	"watch/monitors.json": `[{"name":"log","command":"tail -F ./log","description":"d"}]`,
	"flows/review.js":     "export const meta = {name: 'review', description: 'd'}\n",
	"cmds/status.md":      "# status\n",
	"crew/reviewer.md":    "---\nname: reviewer\ndescription: d\n---\nreview\n",
	"styles/terse.md":     "---\nname: terse\ndescription: d\n---\nbe terse\n",
	"looks/dusk.json":     `{"name":"dusk","base":"dark","overrides":{}}`,
}

// WriteEveryDeclaredPathPlugin writes, at dir, a plugin whose root is a skill and whose manifest
// declares every component of EveryDeclaredPath at its location there, with a file at each.
func WriteEveryDeclaredPathPlugin(t testing.TB, dir, name string) {
	t.Helper()
	write(t, dir, ".claude-plugin/plugin.json", named(everyDeclaredPathManifest, name), 0)
	for rel, body := range everyDeclaredPathFile {
		write(t, dir, rel, named(body, name), 0)
	}
}

// named substitutes the plugin's name for %NAME%.
func named(s, name string) string { return strings.ReplaceAll(s, "%NAME%", name) }
