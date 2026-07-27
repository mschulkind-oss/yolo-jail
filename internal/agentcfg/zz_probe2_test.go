package agentcfg_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

// Does RMW-as-Compose preserve VALUES byte-faithfully? Numbers are the suspect:
// encoding/json decodes every number to float64.
func TestRMWByteFidelityOnLiveClaudeJSON(t *testing.T) {
	raw, err := os.ReadFile("/home/agent/.claude.json")
	if err != nil {
		t.Skip(err)
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: in})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Encoded)

	// Look for exponent-notation numbers in the OUTPUT that were plain integers in the input.
	expRe := regexp.MustCompile(`: (-?\d+(\.\d+)?e[+-]\d+)`)
	hits := expRe.FindAllString(out, -1)
	t.Logf("exponent-notation numbers in output: %d %v", len(hits), hits)

	// Check specific big-int fields
	for _, k := range []string{"firstStartTime", "numStartups", "changelogLastFetched", "promptQueueUseCount"} {
		inRe := regexp.MustCompile(`"` + k + `":\s*([^,}\n]+)`)
		im := inRe.FindStringSubmatch(string(raw))
		om := inRe.FindStringSubmatch(out)
		t.Logf("%s: in=%v out=%v", k, im, om)
	}
	// Report first differing bytes on the key ORDER question
	t.Logf("input first 3 keys as written: %v", firstKeysAsWritten(string(raw), 4))
	t.Logf("output first 3 keys as written: %v", firstKeysAsWritten(out, 4))
}

func firstKeysAsWritten(s string, n int) []string {
	re := regexp.MustCompile(`(?m)^  "([^"]+)":`)
	ms := re.FindAllStringSubmatch(s, n)
	var out []string
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// Compare: what does the CURRENT bespoke path (jsonx) produce for the same file?
func TestJsonxRoundTripNumbers(t *testing.T) {
	raw, err := os.ReadFile("/home/agent/.claude.json")
	if err != nil {
		t.Skip(err)
	}
	// Find every big numeric literal in the source
	re := regexp.MustCompile(`:\s*(\d{10,})`)
	ms := re.FindAllStringSubmatch(string(raw), -1)
	seen := map[string]bool{}
	var uniq []string
	for _, m := range ms {
		if !seen[m[1]] {
			seen[m[1]] = true
			uniq = append(uniq, m[1])
		}
	}
	t.Logf("large integer literals in live file (%d unique): %v", len(uniq), uniq[:min(6, len(uniq))])
	t.Logf("any float-ish: %v", strings.Contains(string(raw), "e+"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ATTACK: under RMW-as-Compose with NO sidecar, does a previously-yolo-managed
// MCP server that is no longer configured get pruned?
func TestRMWDoesNotPruneRetiredMCPServer(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{
		"machineID": "abc",
		"mcpServers": map[string]any{
			"tavily":  map[string]any{"command": "node", "args": []any{"tavily"}},
			"retired": map[string]any{"command": "node", "args": []any{"retired"}},
		},
	}
	// yolo config no longer has "retired". Computed = the freshly configured set only.
	computed := map[string]any{
		"mcpServers": map[string]any{
			"tavily": map[string]any{"command": "node", "args": []any{"tavily"}},
		},
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live, Computed: computed})
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := res.ConfigMap()["mcpServers"].(map[string]any)
	_, retiredStillThere := mcp["retired"]
	t.Logf("retired server survives with no tombstone: %v (mcpServers=%v)", retiredStillThere, keysOf(mcp))
	if !retiredStillThere {
		t.Errorf("expected retired to survive (proving state is needed for the prune)")
	}
}

// ATTACK: deep-merge vs updateFrom. writeClaudeJSON does mcpServers.Set(name, entry)
// -- a WHOLE-ENTRY replace. Compose deep-merges. Does a stale sub-key survive?
func TestRMWDeepMergeLeaksStaleSubkeyThatBespokeWipes(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{
		"mcpServers": map[string]any{
			"tavily": map[string]any{
				"command": "OLD",
				"args":    []any{"old"},
				"env":     map[string]any{"STALE_KEY": "leftover"},
			},
		},
	}
	computed := map[string]any{
		"mcpServers": map[string]any{
			"tavily": map[string]any{"command": "NEW", "args": []any{"new"}},
		},
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live, Computed: computed})
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := res.ConfigMap()["mcpServers"].(map[string]any)
	tv, _ := mcp["tavily"].(map[string]any)
	t.Logf("tavily entry after compose: %v", tv)
	if _, leaked := tv["env"]; leaked {
		t.Logf("DIVERGENCE: stale env survives deep-merge; bespoke updateFrom would have replaced the whole entry")
	}
}

// ATTACK: does hasTrustDialogAccepted=false in the live file survive (setDefault semantics)?
func TestRMWSetDefaultSemantics(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	live := map[string]any{
		"projects": map[string]any{
			"/workspace": map[string]any{"hasTrustDialogAccepted": false},
		},
	}
	res, err := agentcfg.Compose(agentcfg.Inputs{Surface: s, Overlay: live})
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := res.ConfigMap()["projects"].(map[string]any)
	ws, _ := pj["/workspace"].(map[string]any)
	t.Logf("hasTrustDialogAccepted=%v enableAllProjectMcpServers=%v", ws["hasTrustDialogAccepted"], ws["enableAllProjectMcpServers"])
}

// ATTACK: the surface hardcodes "/workspace". What if the workspace is elsewhere?
func TestRMWWorkspaceKeyHardcoded(t *testing.T) {
	s, _ := agentcfg.BuiltinManifest().Lookup("claude", "config")
	res, _ := agentcfg.Compose(agentcfg.Inputs{Surface: s})
	pj, _ := res.ConfigMap()["projects"].(map[string]any)
	t.Logf("projects keys from a bare render: %v", keysOf(pj))
}

func keysOf(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}
