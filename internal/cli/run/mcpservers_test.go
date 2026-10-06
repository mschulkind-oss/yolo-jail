package run

// mcpservers_test.go pins the container launch's delivery of the `mcp` contribution kind
// (docs/design/mcp-presets-removal.md OQ-MP4: composed host-side, like providers): the argv's
// YOLO_MCP_SERVERS is the selected packs' entries joined to the jail's home under the config's own
// mcp_servers, which is what every agent's derive in the jail renders.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

func chromeDevtoolsFixture(t *testing.T) *packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == "chrome-devtools" {
			return p
		}
	}
	t.Fatal("no official chrome-devtools pack")
	return nil
}

func argvMCPServers(t *testing.T, argv []string) string {
	t.Helper()
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, "YOLO_MCP_SERVERS="); ok {
			return v
		}
	}
	t.Fatalf("no YOLO_MCP_SERVERS on the argv: %v", argv)
	return ""
}

func TestTheLaunchArgvCarriesTheSelectedPacksMCPEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, chromeDevtoolsFixture(t))
	mine := jsonx.NewOrderedMap()
	mine.Set("command", "/usr/local/bin/mine")
	servers := jsonx.NewOrderedMap()
	servers.Set("mine", mine)
	in.cfg.Set("mcp_servers", servers)

	got := argvMCPServers(t, o.assembleRunCmd(in))
	for _, want := range []string{`"chrome-devtools"`, `"/bin/sh"`,
		"/home/agent/.local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper", `"mine"`} {
		if !strings.Contains(got, want) {
			t.Errorf("YOLO_MCP_SERVERS lacks %s: %s", want, got)
		}
	}
	// The config the launch read is not written through by the composition.
	if v, _ := in.cfg.Get("mcp_servers"); v.(*jsonx.OrderedMap).Len() != 1 {
		t.Errorf("composing wrote into the launch's own config: %v", v)
	}
}
