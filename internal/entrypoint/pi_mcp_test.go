package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The render writes yolo's MCP table to mcp-adapter.json and LEAVES pi's mcp.json ALONE.
//
// mcp.json is not yolo's file. yolo wrote its table there until it moved to mcp-adapter.json,
// and the pi pack then listed mcp.json in retireOnFirstRender, which a computed surface runs
// after every write, so every boot deleted it. But the name is a vendor one: pi-subagents
// 0.35.1 reads agentDir/mcp.json (`src/runs/shared/mcp-direct-tool-allowlist.ts`,
// `getConfigPaths`) to resolve its `mcp:` direct tools, pi-mcp-adapter 3.1.0 reserves it for
// pi's own built-in MCP, and the user may have written it. yolo cannot tell its own leftover
// from theirs by name (docs/design/agent-directory-map.md, Appendix B), so the name alone
// deletes neither. What does delete yolo's leftover is its content: the pi/mcp surface retires
// an mcp.json that holds exactly its render (AM-R1, pi_subagents_mcp_test.go), and this one,
// a different server set with a `settings` key, is not that.
func TestPiMcpSurfaceRendersConfiguredServers(t *testing.T) {
	home := t.TempDir()
	piAgentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(piAgentDir, 0o755); err != nil {
		t.Fatalf("mkdir pi agent dir: %v", err)
	}
	userMcpPath := filepath.Join(piAgentDir, "mcp.json")
	userMcp := []byte(`{"mcpServers":{"mine":{"command":"mine"}},"settings":{"directTools":true}}`)
	if err := os.WriteFile(userMcpPath, userMcp, 0o644); err != nil {
		t.Fatalf("writing the user's mcp.json: %v", err)
	}

	e := &Env{
		Home:      home,
		Workspace: t.TempDir(),
		Vars: map[string]string{
			"YOLO_MCP_SERVERS": `{"probe-mcp":{"command":"/bin/probe-mcp","args":["--stdio"]}}`,
		},
	}
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatalf("embedded pi: %v", err)
	}
	ConfigurePackSurfaces(e, []*packload.Pack{pi})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}

	mcpPath := filepath.Join(home, ".pi", "agent", "mcp-adapter.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading pi mcp-adapter.json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling pi mcp-adapter.json: %v", err)
	}
	want := map[string]any{
		"mcpServers": map[string]any{
			"probe-mcp": map[string]any{
				"command": "/bin/probe-mcp",
				"args":    []any{"--stdio"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pi mcp-adapter.json = %#v, want %#v", got, want)
	}

	after, err := os.ReadFile(userMcpPath)
	if err != nil {
		t.Fatalf("the boot render deleted ~/.pi/agent/mcp.json (%v). It is pi's and the "+
			"user's file, which pi-subagents reads, not a sidecar yolo owns: take it out of "+
			"the pi mcp surface's retireOnFirstRender", err)
	}
	if string(after) != string(userMcp) {
		t.Errorf("the boot render rewrote ~/.pi/agent/mcp.json:\n got %s\nwant %s", after, userMcp)
	}
}
