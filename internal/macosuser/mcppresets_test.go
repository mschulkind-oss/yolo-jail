package macosuser

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// renderClaude renders the shipped claude pack's surfaces into e's home — the pack's real
// render, from a copy of the shipped tree materialized into a temp dir of the test's own.
func renderClaude(t *testing.T, e *entrypoint.Env) {
	t.Helper()
	all, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	for _, p := range all {
		if p.Name == "claude" {
			entrypoint.ConfigurePackSurfaces(e, []*packload.Pack{p})
			return
		}
	}
	t.Fatal("no shipped claude pack")
}

// A LAUNCH THAT ASKED FOR PRESETS WRITES NO ENTRY POINTING AT A WRAPPER IT NEVER GENERATES,
// asked across the boundary: the plan the orchestrator builds, the preset list it relays to
// the bootstrap (which still feeds the bootstrap's warning), and the bootstrap's own Env
// translation over that argv, rendering claude's ~/.claude.json. Before Env.SkipMCPPresets
// reached the MCP table, that file named `~/.local/bin/mcp-wrappers/node` for chrome-devtools
// on every macos-user launch, and the file does not exist there.
func TestAMacosUserLaunchWritesNoPresetServerEntry(t *testing.T) {
	opts := newOpts("/Users/Shared/proj")
	cfg, err := jsonx.Decode([]byte(`{"mcp_presets": ["chrome-devtools", "sequential-thinking"]}`))
	if err != nil {
		t.Fatal(err)
	}
	opts.Config = cfg.(*jsonx.OrderedMap)
	plan := buildPlan(mockDeps(nil), opts, nil)
	vars := bootstrapVars(t, plan.BootstrapArgv)
	if !strings.Contains(vars["YOLO_MCP_PRESETS"], "chrome-devtools") {
		t.Fatalf("premise: the launch no longer relays the preset list its bootstrap warns from: %q",
			vars["YOLO_MCP_PRESETS"])
	}
	home := t.TempDir()
	vars["JAIL_HOME"], vars["HOME"] = home, home
	vars["YOLO_DARWIN_WORKSPACE"] = t.TempDir()
	vars["MISE_DATA_DIR"] = t.TempDir() // the plan's names the real sandbox home
	delete(vars, SandboxEnvFileEnv)     // the plan's path is under /var/yolo-jail, absent here

	e := entrypoint.DarwinEnvFrom(vars, home)
	var term strings.Builder
	e.Stderr = &term
	renderClaude(t, e)
	raw, err := os.ReadFile(e.ClaudeJSONPath())
	if err != nil {
		t.Fatalf("no ~/.claude.json rendered: %v\n%s", err, term.String())
	}
	for _, s := range []string{"mcp-wrappers", "chrome-devtools", "sequential-thinking"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("claude's config on macos-user names %q, a preset this backend never delivers:\n%s", s, raw)
		}
	}
}
