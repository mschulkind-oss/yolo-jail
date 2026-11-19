package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func provisioningOriginals(names ...string) fstest.MapFS {
	fixture := fstest.MapFS{"fixture-agent/pack.toml": {Data: []byte(`name="fixture-agent"
[native_sign_in]
formats=["claude-plugin"]
[[skills]]
agent="fixture-agent"
into=".fixture/skills"
`)}}
	for _, name := range names {
		fixture[name+"/pack.toml"] = &fstest.MapFile{Data: []byte(`name="` + name + `"
[[mcp_sign_in_bundle]]
bundle="original"
format="claude-plugin"
origin="https://example.invalid/fixture"
revision="0123456789abcdef0123456789abcdef01234567"
license="original/LICENSE"
`)}
		fixture[name+"/original/LICENSE"] = &fstest.MapFile{Data: []byte("Synthetic fixture license\n")}
		fixture[name+"/original/.claude-plugin/plugin.json"] = &fstest.MapFile{Data: []byte(`{"name":"native-fixture"}`)}
		fixture[name+"/original/.mcp.json"] = &fstest.MapFile{Data: []byte(`{"fixture-key":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":31111}}}`)}
	}
	return fixture
}

func TestMCPAuthStatusProvisioningAdviceThroughMain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_VERSION", "")
	old := packload.EmbeddedFS()
	packload.SetEmbeddedFS(provisioningOriginals("fixture-provision"))
	t.Cleanup(func() { packload.SetEmbeddedFS(old) })
	writeUserConfig(t, `{"packs":["fixture-agent"],"mcp_servers":{"fixture-server":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","oauth":{"client_id":"fixture-client"}}}}`)
	before := managedMCPTreeSnapshot(t, home)
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "status", "fixture-server", "--json"}) })
	if rc != 0 || !strings.Contains(out, "yolo pack add fixture-provision, then relaunch") || !strings.Contains(out, "https://example.invalid/fixture") || !strings.Contains(out, "0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("missing derived provisioning advice rc=%d out=%s err=%s", rc, out, errs)
	}
	if !reflect.DeepEqual(before, managedMCPTreeSnapshot(t, home)) {
		t.Fatal("advice materialized/wrote state")
	}
	for _, unexpected := range []string{"native-fixture", "fixture-key", "managed, placed", "owner", "Bearer "} {
		if strings.Contains(out, unexpected) {
			t.Fatalf("advice promoted %q", unexpected)
		}
	}
	if !strings.Contains(out, "yolo config set mcp_servers.fixture-server.oauth.client_id") {
		t.Fatal("own-client alternative absent")
	}
}
