package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

// stageShippedPacksNamed is stageShippedPacks with every shipped pack but the named ones
// removed, so a whole-boot test renders the agents it asserts on and nothing else.
func stageShippedPacksNamed(t *testing.T, keep ...string) string {
	t.Helper()
	root := stageShippedPacks(t)
	official := filepath.Join(root, "_official")
	entries, err := os.ReadDir(official)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, k := range keep {
		want[k] = true
	}
	for _, ent := range entries {
		if ent.IsDir() && !want[ent.Name()] {
			if err := os.RemoveAll(filepath.Join(official, ent.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// THE macos-user BOOT GATES requires_env PER AGENT, AND SAYS WHAT IT SKIPPED — as the container
// boot does, because it is the same code: configure_pack_surfaces has no notDarwin, so the darwin
// bootstrap runs loadMCPTables, which reads each agent's env file under ~/.config/yolo-agent-env
// and prints its notice on the boot's stderr. On this backend the host writes those files into
// the workspace sidecar (writeMacosUserAgentEnvFiles, <sidecar>/config/yolo-agent-env), and the
// home layout links ~/.config to <sidecar>/config, so the gate finds them through the link.
//
// The setup census once recorded this cell as a SILENT drop ("removed for every agent … with no
// line"), and the user guide said the same. Through RunDarwinBootstrap, the whole boot rather than
// the gate alone, so a darwin step that stopped running the gate, or a layout that stopped
// linking ~/.config, fails here; and the census's cell is read back, so it cannot say silent
// while this boot speaks.
func TestDarwinBootstrapGatesRequiresEnvPerAgentAndSaysSo(t *testing.T) {
	home := t.TempDir()
	sidecar := t.TempDir()
	var stderr strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME":          home,
		"YOLO_PACK_ROOT":     stageShippedPacksNamed(t, "claude", "codex"),
		DarwinHomeSidecarEnv: sidecar,
		"YOLO_MCP_SERVERS": `{"zai-search": {"command": "zai-mcp", "requires_env": ["ZAI_API_KEY"]},
			"gh": {"command": "gh-mcp", "requires_env": ["ZZ_NOT_SET_TOKEN"]}}`,
	})
	e.Workspace = t.TempDir()
	e.Stderr = &stderr

	// Where the macos-user launch writes claude's own file: the container vehicle's writer,
	// into the sidecar rather than the home (internal/cli/run agentenvfiles.go).
	envFile := filepath.Join(sidecar, strings.TrimPrefix(AgentEnvDirRel, "."), "claude.sh")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("export ZAI_API_KEY=${ZAI_API_KEY:-'tok-zai'}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	claudeJSON := string(mustRead(t, e.ClaudeJSONPath()))
	if !strings.Contains(claudeJSON, `"zai-search"`) {
		t.Errorf("claude's own file carries ZAI_API_KEY, so its config must name the server:\n%s", claudeJSON)
	}
	if strings.Contains(claudeJSON, `"gh"`) {
		t.Errorf("no agent holds ZZ_NOT_SET_TOKEN, so no config may name its server:\n%s", claudeJSON)
	}
	out := stderr.String()
	for _, want := range []string{
		"'zai-search' configured only for claude",
		"MCP server 'gh' skipped — required env not set: ZZ_NOT_SET_TOKEN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the darwin boot must say %q:\n%s", want, out)
		}
	}

	cell, ok := setupcensus.Find("mcp_servers.requires_env")
	if !ok {
		cell, _ = setupcensus.Find("mcp_servers")
	}
	if d := cell.MacosUser.Disposition; !d.Works() {
		t.Errorf("the darwin boot gates requires_env per agent and names what it skips, and the setup "+
			"census says %s on macos-user (%s): fix the census cell in internal/setupcensus", d,
			cell.MacosUser.Reason)
	}
}
