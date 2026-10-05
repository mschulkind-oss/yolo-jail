package macosuser

// mcpbootstrap_test.go pins the macos-user delivery of the `mcp` contribution kind
// (docs/design/mcp-presets-removal.md §7, OQ-MP4): the plan builder composes the staged packs'
// entries for the sandbox account's home under the config's own mcp_servers, and bakes that table
// into the bootstrap argv, which every agent's MCP file renders from — the container launch's
// YOLO_MCP_SERVERS, for this backend's home.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// stagedShippedTree is a host-side staged pack tree holding every shipped pack, in the shape the
// run pipeline stages (<root>/_official/<name>).
func stagedShippedTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	official := filepath.Join(root, "_official")
	if err := os.MkdirAll(official, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, problems := packload.MaterializeEmbedded(officialpacks.FS, official); len(problems) > 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	return root
}

func bootstrapMCPServers(t *testing.T, cfg *jsonx.OrderedMap, hostPackRoot string) string {
	t.Helper()
	plan := BuildRunPlan("/Users/Shared/proj", cfg, []string{"claude"}, []string{"claude"},
		"/opt/yolo-jail/bin/yolo", hostPackRoot, HomeOverlay{}, HostContext{}, nil, nil, nil)
	got, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_MCP_SERVERS")
	if !ok {
		t.Fatalf("YOLO_MCP_SERVERS is not baked into the bootstrap argv: %v", plan.BootstrapArgv)
	}
	return got
}

func TestTheMacosUserBootstrapCarriesThePacksMCPEntry(t *testing.T) {
	root := stagedShippedTree(t)
	wrapper := SandboxHome() + "/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper"

	got := bootstrapMCPServers(t, jsonx.NewOrderedMap(), root)
	if !strings.Contains(got, `"chrome-devtools"`) || !strings.Contains(got, wrapper) ||
		!strings.Contains(got, `"/bin/sh"`) {
		t.Errorf("the bootstrap's MCP table does not run the pack's wrapper under %s: %s",
			SandboxHome(), got)
	}

	cfg, err := jsonx.Decode([]byte(`{"mcp_servers":{"chrome-devtools":null,` +
		`"mine":{"command":"/usr/local/bin/mine"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got = bootstrapMCPServers(t, cfg.(*jsonx.OrderedMap), root)
	if strings.Contains(got, wrapper) || !strings.Contains(strings.ReplaceAll(got, " ", ""), `"chrome-devtools":null`) ||
		!strings.Contains(got, `"mine"`) {
		t.Errorf("your null must remove the pack's entry and your own must stay: %s", got)
	}

	if got := bootstrapMCPServers(t, jsonx.NewOrderedMap(), ""); got != "{}" {
		t.Errorf("a launch that staged no packs carries %s, want your table alone ({})", got)
	}
}
