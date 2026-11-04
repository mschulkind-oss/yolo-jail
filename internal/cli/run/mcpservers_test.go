package run

// mcpservers_test.go pins the container launch's delivery of the `mcp` contribution kind
// (docs/design/mcp-presets-removal.md OQ-MP4: composed host-side, like providers): the argv's
// YOLO_MCP_SERVERS is the selected packs' entries joined to the jail's home under the config's own
// mcp_servers, which is what every agent's derive in the jail renders.

import (
	"os"
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

func TestTheLaunchArgvDoesNotSendPackHeadersToAnOverriddenDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	packDir := t.TempDir()
	secret := "SYNTHETIC_MCP_AUTHORIZATION_SECRET"
	manifest := `{"name":"remote-pack","contributes":[{"kind":"mcp","name":"linear","config":{"url":"https://old.example/mcp","headers":{"Authorization":"Bearer ` + secret + `"}}}]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(packDir, "remote-pack")
	if pack == nil || len(problems) != 0 {
		t.Fatalf("load fixture pack: pack=%v problems=%v", pack, problems)
	}
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, pack)
	linear := jsonx.NewOrderedMap()
	linear.Set("url", "https://foreign.example/mcp")
	servers := jsonx.NewOrderedMap()
	servers.Set("linear", linear)
	in.cfg.Set("mcp_servers", servers)
	got := argvMCPServers(t, o.assembleRunCmd(in))
	if strings.Contains(got, secret) || strings.Contains(got, "Authorization") {
		t.Fatalf("launch argv delivered a pack header to the overridden endpoint: %s", got)
	}
	if !strings.Contains(got, "https://foreign.example/mcp") {
		t.Fatalf("launch argv lost the selected destination: %s", got)
	}
}

func TestTheLaunchArgvBindsPackHeadersToTheEffectiveTransport(t *testing.T) {
	for _, tc := range []struct {
		name, manifestConfig, override, wantHeader, notWantHeader string
	}{
		{
			name:           "transport-only change clears inherited authorization",
			manifestConfig: `"url":"https://mcp.example/mcp","headers":{"Authorization":"Bearer inherited"}`,
			override:       `{"transport":"sse"}`,
			notWantHeader:  "Bearer inherited",
		},
		{
			name:           "same URL with omitted transport inherits SSE headers",
			manifestConfig: `"url":"https://mcp.example/mcp","transport":"sse","headers":{"Authorization":"Bearer inherited"}`,
			override:       `{"url":"https://mcp.example/mcp"}`,
			wantHeader:     "Bearer inherited",
		},
		{
			name:           "explicit higher headers replace inherited credentials",
			manifestConfig: `"url":"https://mcp.example/mcp","headers":{"Authorization":"Bearer inherited"}`,
			override:       `{"transport":"sse","headers":{"Authorization":"Bearer replacement"}}`,
			wantHeader:     "Bearer replacement",
			notWantHeader:  "Bearer inherited",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			packDir := t.TempDir()
			manifest := `{"name":"remote-pack","contributes":[{"kind":"mcp","name":"linear","config":{` + tc.manifestConfig + `}}]}`
			if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			pack, problems := packload.LoadDir(packDir, "remote-pack")
			if pack == nil || len(problems) != 0 {
				t.Fatalf("load fixture pack: pack=%v problems=%v", pack, problems)
			}
			in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
			in.packs = append(in.packs, pack)
			overrideValue, err := jsonx.Decode([]byte(tc.override))
			if err != nil {
				t.Fatal(err)
			}
			override := overrideValue.(*jsonx.OrderedMap)
			servers := jsonx.NewOrderedMap()
			servers.Set("linear", override)
			in.cfg.Set("mcp_servers", servers)
			got := argvMCPServers(t, o.assembleRunCmd(in))
			for _, want := range []string{`"transport": "sse"`, `"url": "https://mcp.example/mcp"`} {
				if strings.Contains(tc.override, `"transport"`) || strings.Contains(tc.manifestConfig, `"transport"`) {
					if !strings.Contains(got, want) {
						t.Errorf("launch argv lacks %s: %s", want, got)
					}
				}
			}
			if tc.wantHeader != "" && !strings.Contains(got, tc.wantHeader) {
				t.Errorf("launch argv lost expected header %q: %s", tc.wantHeader, got)
			}
			if tc.notWantHeader != "" && strings.Contains(got, tc.notWantHeader) {
				t.Errorf("launch argv retained unsafe header %q: %s", tc.notWantHeader, got)
			}
		})
	}
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
