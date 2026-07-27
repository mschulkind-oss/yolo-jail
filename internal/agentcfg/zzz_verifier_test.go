package agentcfg_test

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

func vkeys(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

// V1: does RMW-as-Compose prune a retired MCP server WITHOUT a tombstone source?
func TestV1RMWNeedsStateToPrune(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{
		"machineID": "abc",
		"mcpServers": map[string]any{
			"tavily":  map[string]any{"command": "node"},
			"retired": map[string]any{"command": "node"},
		},
	}
	computed := map[string]any{
		"mcpServers": map[string]any{"tavily": map[string]any{"command": "node"}},
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live, Computed: computed})
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := res.ConfigMap()["mcpServers"].(map[string]any)
	_, still := mcp["retired"]
	t.Logf("retired survives without an explicit tombstone: %v; mcpServers=%v", still, vkeys(mcp))
}

// V2: deep-merge vs whole-entry replace divergence from writeClaudeJSON.
func TestV2DeepMergeLeaksStaleSubkey(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{"mcpServers": map[string]any{"tavily": map[string]any{
		"command": "OLD", "args": []any{"old"}, "env": map[string]any{"STALE": "x"}}}}
	computed := map[string]any{"mcpServers": map[string]any{"tavily": map[string]any{
		"command": "NEW", "args": []any{"new"}}}}
	res, _ := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live, Computed: computed})
	mcp, _ := res.ConfigMap()["mcpServers"].(map[string]any)
	tv, _ := mcp["tavily"].(map[string]any)
	t.Logf("tavily=%v  (bespoke updateFrom would REPLACE the whole entry, dropping env)", tv)
}

// V3: setDefault semantics -- live false must survive; managed true must win.
func TestV3SetDefaultVsManaged(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{"projects": map[string]any{"/workspace": map[string]any{
		"hasTrustDialogAccepted": false, "enableAllProjectMcpServers": false}}}
	res, _ := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live})
	pj, _ := res.ConfigMap()["projects"].(map[string]any)
	ws, _ := pj["/workspace"].(map[string]any)
	t.Logf("hasTrustDialogAccepted=%v (bespoke setDefault keeps false) enableAll=%v (bespoke forces true)",
		ws["hasTrustDialogAccepted"], ws["enableAllProjectMcpServers"])
}

// V4: RMW residue -- yolo stops asserting a key; nothing removes it.
func TestV4StopAssertingLeavesResidue(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	s.Managed = nil
	s.Defaults = nil
	afterBoot1 := map[string]any{"projects": map[string]any{"/workspace": map[string]any{
		"enableAllProjectMcpServers": true}}}
	res, _ := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: afterBoot1})
	pj, _ := res.ConfigMap()["projects"].(map[string]any)
	ws, _ := pj["/workspace"].(map[string]any)
	t.Logf("residue after yolo stops asserting: enableAllProjectMcpServers=%v", ws["enableAllProjectMcpServers"])
}

// V5: is a live value able to override a pack's DEFAULT under RMW? (yes -- that's the point)
// but can it override a pack's MANAGED? (must be no)
func TestV5CopilotDefaultsAndManaged(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("copilot", "config")
	t.Logf("copilot/config defaults=%v managed=%v", s.Defaults, s.Managed)
	live := map[string]any{"yolo": false, "copilot_tokens": map[string]any{"t": "secret"}}
	res, _ := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live})
	m := res.ConfigMap()
	t.Logf("yolo=%v prov=%v tokens=%v", m["yolo"], res.Provenance["yolo"], m["copilot_tokens"] != nil)
}

// V6: mise/config is TOML. Can RMW read a live TOML file back as a layer? Codec check.
func TestV6MiseTOMLAsRMW(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("mise", "config")
	t.Logf("mise/config codec=%s defaults=%v managed=%v", s.Codec, s.Defaults, s.Managed)
	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface:   s,
		HostBytes: []byte("[tools]\nnode = \"22\"\n"),
		Computed:  map[string]any{"tools": map[string]any{"go": "latest"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encoded=%q", string(res.Encoded))
}
