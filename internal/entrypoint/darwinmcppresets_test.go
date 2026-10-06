package entrypoint

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// darwinmcppresets_test.go pins the half of the macos-user preset refusal that lives in the
// agents' configs: an environment that does not generate the preset wrappers
// (Env.SkipMCPPresets) writes no server entry pointing at one.

// materializedPack is the shipped pack of that name, materialized into a temp dir of the
// test's own.
func materializedPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	all, problems := packload.MaterializeEmbedded(officialpacks.FS, resolvedDir(t))
	if len(problems) != 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	for _, p := range all {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no shipped pack named %q", name)
	return nil
}

// The darwin translation keeps both presets out of the MCP table while a container Env built
// from the same variables still expands them, and the user's own mcp_servers entry survives
// the skip.
//
// MUTATION: drop the `!e.SkipMCPPresets` gate in mcpServersWith and the darwin half goes red.
func TestTheDarwinEnvWritesNoPresetServerEntry(t *testing.T) {
	vars := func() map[string]string {
		return map[string]string{
			"JAIL_HOME":        resolvedDir(t),
			"YOLO_MCP_PRESETS": `["chrome-devtools","sequential-thinking"]`,
			"YOLO_MCP_SERVERS": `{"mine":{"command":"/usr/local/bin/mine"}}`,
		}
	}
	container := sharedMCPNames(NewEnv(vars()))
	for _, name := range []string{"chrome-devtools", "sequential-thinking", "mine"} {
		if !container[name] {
			t.Fatalf("premise: the container MCP table lacks %q: %v", name, container)
		}
	}
	v := vars()
	darwin := sharedMCPNames(DarwinEnvFrom(v, v["JAIL_HOME"]))
	for _, name := range []string{"chrome-devtools", "sequential-thinking"} {
		if darwin[name] {
			t.Errorf("macos-user's MCP table carries preset %q, whose wrapper that backend never writes", name)
		}
	}
	if !darwin["mine"] {
		t.Error("skipping the presets also dropped the user's own mcp_servers entry")
	}
}

// Through the claude pack's real render: on the darwin translation ~/.claude.json names no
// mcp-wrappers path at all, which is what an agent would otherwise try to spawn.
func TestTheDarwinClaudeConfigNamesNoPresetWrapper(t *testing.T) {
	home := resolvedDir(t)
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME":             home,
		"YOLO_MCP_PRESETS":      `["chrome-devtools","sequential-thinking"]`,
		"YOLO_MCP_SERVERS":      `{"mine":{"command":"/usr/local/bin/mine"}}`,
		"YOLO_DARWIN_WORKSPACE": resolvedDir(t),
	}, home)
	var term strings.Builder
	e.Stderr = &term
	ConfigurePackSurfaces(e, []*packload.Pack{materializedPack(t, "claude")})
	raw, err := os.ReadFile(e.ClaudeJSONPath())
	if err != nil {
		t.Fatalf("claude's render wrote no ~/.claude.json: %v\n%s", err, term.String())
	}
	if strings.Contains(string(raw), "mcp-wrappers") || strings.Contains(string(raw), "chrome-devtools") {
		t.Errorf("claude's config on macos-user names the preset wrapper this backend never writes:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"mine"`) {
		t.Errorf("claude's config lost the user's own MCP server:\n%s", raw)
	}
}
