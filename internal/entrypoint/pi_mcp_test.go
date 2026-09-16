package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestPiMcpSurfaceRendersConfiguredServers(t *testing.T) {
	home := t.TempDir()
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

	mcpPath := filepath.Join(home, ".pi", "agent", "mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading pi mcp.json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling pi mcp.json: %v", err)
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
		t.Fatalf("pi mcp.json = %#v, want %#v", got, want)
	}
}
