package agentcfg_test

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

func live(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("no live file %s: %v", p, err)
	}
	return b
}

func keys(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Probe A: replicate the claimed "first-migration stateful render drops 32 keys".
func TestStatefulFirstMigrationOnLiveClaudeJSON(t *testing.T) {
	raw := live(t, "/home/agent/.claude.json")
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	s, ok := agentcfg.PackManifestForTest(t).Lookup("claude", "config")
	if !ok {
		t.Fatal("no claude/config surface")
	}
	out, err := agentcfg.ComposeStateful(agentcfg.StatefulInputs{
		Base:              agentcfg.Inputs{Surface: s},
		CurrentBytes:      raw,
		LastRenderPresent: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Result.ConfigMap()
	t.Logf("in=%d out=%d firstMigration=%v", len(in), len(got), out.FirstMigration)
	t.Logf("out keys: %v", keys(got))
}

// Probe B: RMW-as-Compose. Layer order defaults < current-file(overlay) < computed < managed.
func TestRMWAsComposeOnLiveClaudeJSON(t *testing.T) {
	raw := live(t, "/home/agent/.claude.json")
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	s, _ := agentcfg.PackManifestForTest(t).Lookup("claude", "config")

	// Simulate writeClaudeJSON: previously-managed name "stale" removed, fresh set added.
	computed := map[string]any{
		"mcpServers": map[string]any{
			"stale":               nil,
			"sequential-thinking": map[string]any{"command": "node", "args": []any{"x"}},
		},
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface:  s,
		Overlay:  in,
		Computed: computed,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := res.ConfigMap()
	t.Logf("in=%d out=%d", len(in), len(got))
	// which keys were lost?
	for _, k := range keys(in) {
		if _, ok := got[k]; !ok {
			t.Logf("LOST: %s", k)
		}
	}
	t.Logf("prov machineID=%q projects=%q mcpServers=%q", res.Provenance["machineID"], res.Provenance["projects"], res.Provenance["mcpServers"])
	// Check projects["/workspace"]
	pj, _ := got["projects"].(map[string]any)
	ws, _ := pj["/workspace"].(map[string]any)
	t.Logf("projects[/workspace] keys=%d: %v", len(ws), keys(ws))
	// check mcpServers content
	mcp, _ := got["mcpServers"].(map[string]any)
	t.Logf("mcpServers keys: %v", keys(mcp))
	// Did the live mcpServers entries survive?
	liveMCP, _ := in["mcpServers"].(map[string]any)
	t.Logf("live mcpServers keys: %v", keys(liveMCP))
}

// Probe C: same shape on copilot config.
func TestRMWAsComposeOnLiveCopilotConfig(t *testing.T) {
	raw := live(t, "/home/agent/.copilot/config.json")
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	s, ok := agentcfg.PackManifestForTest(t).Lookup("copilot", "config")
	if !ok {
		t.Fatal("no copilot/config")
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: in})
	if err != nil {
		t.Fatal(err)
	}
	got := res.ConfigMap()
	t.Logf("in=%d out=%d tokens_present=%v", len(in), len(got), got["copilot_tokens"] != nil)
}

// Probe D: the STATEFUL path on live copilot config with no last_render.
func TestStatefulFirstMigrationOnLiveCopilotConfig(t *testing.T) {
	raw := live(t, "/home/agent/.copilot/config.json")
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	s, _ := agentcfg.PackManifestForTest(t).Lookup("copilot", "config")
	out, err := agentcfg.ComposeStateful(agentcfg.StatefulInputs{
		Base: agentcfg.Inputs{Surface: s}, CurrentBytes: raw, LastRenderPresent: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("in=%d out=%d keys=%v", len(in), len(out.Result.ConfigMap()), keys(out.Result.ConfigMap()))
}

var _ = manifest.Surface{}
