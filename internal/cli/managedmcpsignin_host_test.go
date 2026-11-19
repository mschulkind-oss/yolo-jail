package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestManagedMCPHostApplyResolvesOnlyActualWholeTreePlacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "integration")
	agentRoot := filepath.Join(filepath.Dir(root), "agent")
	manifest := `name="fixture"
skills_tier="namespaced"
[[mcp_sign_in_bundle]]
bundle="skills/original"
format="claude-plugin"
origin="https://example.invalid/original"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/original/LICENSE"
[[skills]]
from="skills"

`
	for rel, body := range map[string]string{"pack.toml": manifest, "derive.lua": `yolo.mcp_sign_in("fixture",{native_name=function(name,key) return "plugin:"..name..":"..key end})`, "skills/original/LICENSE": "MIT License", "skills/original/.claude-plugin/plugin.json": `{"name":"original"}`, "skills/original/.mcp.json": `{"server":{"url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"public-fixture"}}}`} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	if _, ps := packload.LoadDirForUse(root, "fixture"); len(ps) != 0 {
		t.Fatal(ps)
	}
	writeFile(t, filepath.Join(agentRoot, "pack.toml"), `name="agent"
mcp_header_form="json"
mcp_sign_in_paste=true
[native_sign_in]
formats=["claude-plugin"]
[[skills]]
agent="fixture"
into=".native/skills"
[[mcp_auth]]
store=".native/.credentials.json"
format="json"
empty_store="{}"
entries_at=["mcpOAuth"]
lock={protocol="proper-lockfile",path=".native/.storage-write.lock",stale_seconds=15}
fields={access_token="accessToken",refresh_token="refreshToken",expires_at={path="expiresAt",unit="ms"},client_id="clientId"}
login_hint="authenticate fixture"
`)
	writeFile(t, filepath.Join(agentRoot, "derive.lua"), `yolo.mcp_sign_in("fixture",{native_name=function(name,key) return "plugin:"..name..":"..key end})`)
	home := hostComputedHome(t, fmt.Sprintf(`{"packs":[%q,%q],"host_management":"own","mcp_servers":{"server":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once"}}}`, agentRoot, root))
	readFacts := func() mcpsignin.Launch {
		t.Helper()
		packs := selectConfiguredHostPacks().packs
		composition, err := composeHostInputs(config.UserScopeConfigOrEmpty(), packs, home)
		if err != nil {
			t.Fatal(err)
		}
		var facts mcpsignin.Launch
		if err := json.Unmarshal([]byte(composition.inputs.Vars[mcpsignin.WireEnv]), &facts); err != nil {
			t.Fatal(err)
		}
		return facts
	}
	before := readFacts()
	if len(before.Decisions) != 1 || before.Decisions[0].Point != nil {
		t.Fatalf("declaration became placement: %+v", before)
	}
	var out, errs bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errs, false, nil); rc != 0 {
		t.Fatalf("actual host apply: rc=%d %s %s", rc, out.String(), errs.String())
	}
	after := readFacts()
	if len(after.Decisions) != 1 || after.Decisions[0].Point == nil || after.Decisions[0].Point.NativeName != "plugin:original:server" || after.Bundles[0].Placement != "placed" {
		t.Fatalf("actual host delivery not resolved: %+v", after)
	}
	if info, err := os.Stat(filepath.Join(home, ".native/skills/original/.mcp.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("no actual native tree: %v", err)
	}
	t.Setenv(claudeview.SecureStorageEnv, t.TempDir())
	alternate := readFacts()
	if alternate.Decisions[0].Capturable || alternate.Decisions[0].Point == nil {
		t.Fatalf("alternate storage changed placement or acquired a source: %+v", alternate)
	}
}
