package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

func TestApplyHostManagedMCPRouteUsesDeliveredBundleAndRecordFreshness(t *testing.T) {
	for _, tc := range []struct {
		name, record                                    string
		bundle, view, ownClient, wantHeader, firstApply bool
	}{
		{name: "namespaced delivered", bundle: true, wantHeader: true},
		{name: "plain bootstrap"},
		{name: "live record without bundle", record: "live", wantHeader: true},
		{name: "expired record without bundle", record: "expired"},
		{name: "dead record without bundle", record: "dead"},
		{name: "noncapturable view", bundle: true, view: true},
		{name: "noncapturable with live record", bundle: true, view: true, record: "live", wantHeader: true},
		{name: "own client refusal", bundle: true, ownClient: true},
		{name: "live route respects first apply", record: "live", firstApply: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bundle := filepath.Join(root, "bundle")
			writeFile(t, filepath.Join(bundle, "pack.toml"), `name="bundle"
skills_tier="namespaced"
[[skills]]
from="skills"
[[mcp_sign_in_bundle]]
bundle="skills/native"
format="claude-plugin"
origin="https://example.invalid/original"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/native/LICENSE"
`)
			original := `{"mcpServers":{"fixture-key":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"public-native-client","callbackPort":31111}}}}`
			writeFile(t, filepath.Join(bundle, "skills/native/.mcp.json"), original)
			writeFile(t, filepath.Join(bundle, "skills/native/.claude-plugin/plugin.json"), `{"name":"native"}`)
			writeFile(t, filepath.Join(bundle, "skills/native/LICENSE"), "MIT License")
			before := managedMCPTreeSnapshot(t, bundle)
			home := hostComputedHome(t, `{"packs":["claude"],"host_management":"own"}`)
			if err := os.Remove(filepath.Join(home, ".config/yolo-jail/config.jsonc")); err != nil {
				t.Fatal(err)
			}
			packs := `["claude"]`
			if tc.bundle {
				packs = `["claude","team/bundle"]`
			}
			client := ""
			if tc.ownClient {
				client = "\n[mcp_servers.configured-alias.oauth]\nclient_id=\"own-client\"\n"
			}
			writeFile(t, filepath.Join(home, ".config/yolo-jail/config.toml"), fmt.Sprintf(`packs=%s
host_management="own"
pack_sources={team={path=%q,bare=false}}
[mcp_servers.configured-alias]
url="https://mcp.fixture.invalid/mcp"
sign_in="once"
[mcp_servers.configured-alias.headers]
aUtHoRiZaTiOn="stale"
AUTHORIZATION="also-stale"
X-Team="t1"
%s`, packs, root, client))
			t.Setenv(claudeview.SecureStorageEnv, "")
			if tc.view {
				t.Setenv(claudeview.SecureStorageEnv, t.TempDir())
			}
			if tc.record != "" {
				selection := selectConfiguredHostPacks()
				share, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), selection.packs, home, home)
				if err != nil || len(share.Once) != 1 {
					t.Fatalf("synthetic share: %+v %v", share, err)
				}
				store, err := mcpauth.NewOnceStore(filepath.Join(loopholes.StateDirFor("mcp-auth-broker"), "once"))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
					record := mcpauth.Record{Server: share.Once[0].Name, Identity: share.Once[0].Identity, URL: share.Once[0].URL, State: "live", Generation: 1, AccessToken: "synthetic-secret-access", RefreshToken: "synthetic-secret-refresh", ClientID: "adopted-private-client", Entry: json.RawMessage(`{}`), ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli()}
					if tc.record == "expired" {
						record.ExpiresAtMS = time.Now().Add(-time.Hour).UnixMilli()
					}
					if tc.record == "dead" {
						record.State = "dead"
					}
					file.Records = []mcpauth.Record{record}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			prior := `{"mcpServers":{"configured-alias":{"type":"http","url":"https://mcp.fixture.invalid/mcp","headers":{"AUTHORIZATION":"also-stale","X-Team":"t1","aUtHoRiZaTiOn":"stale"}}}}`
			if tc.firstApply {
				writeFile(t, filepath.Join(home, ".claude.json"), prior)
			}
			var out, errs bytes.Buffer
			rc := applyHost(&out, &errs, false, true, nil)
			if tc.firstApply {
				body, err := os.ReadFile(filepath.Join(home, ".claude.json"))
				if rc == 0 || err != nil || string(body) != prior || !strings.Contains(out.String()+errs.String(), "not confirmed") {
					t.Fatalf("managed header route bypassed first-apply refusal: rc=%d %v %s %s", rc, err, out.String(), errs.String())
				}
				return
			}
			if rc != 0 {
				t.Fatalf("actual applyHost: rc=%d %s %s", rc, out.String(), errs.String())
			}
			remote := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
			_, header := remote["headersHelper"]
			if header != tc.wantHeader || remote["url"] != "https://mcp.fixture.invalid/mcp" {
				selection := selectConfiguredHostPacks()
				share, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), selection.packs, home, home)
				facts, _ := json.Marshal(share.SignIn)
				t.Fatalf("wrong actual apply route: %#v; frozen facts %s (%v); %s %s", remote, facts, err, out.String(), errs.String())
			}
			if header {
				if remote["headersHelper"] != "yolo mcp-auth header configured-alias --format json" || remote["oauth"] != nil {
					t.Fatalf("wrong helper or borrowed OAuth: %#v", remote)
				}
				headers := remote["headers"].(map[string]any)
				if headers["X-Team"] != "t1" || len(headers) != 1 {
					t.Fatal("static Authorization survived or nonauth header was lost")
				}
			} else if remote["headers"].(map[string]any)["aUtHoRiZaTiOn"] != "stale" {
				t.Fatal("baseline plain configuration changed")
			}
			body, err := os.ReadFile(filepath.Join(home, ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"synthetic-secret", "adopted-private-client", "public-native-client"} {
				if strings.Contains(string(body)+out.String()+errs.String(), forbidden) {
					t.Fatalf("render/disclosure borrowed credential metadata: %s", forbidden)
				}
			}
			if !reflect.DeepEqual(before, managedMCPTreeSnapshot(t, bundle)) {
				t.Fatal("retained original mutated")
			}
			if tc.bundle {
				delivered, err := os.ReadFile(filepath.Join(home, ".claude/skills/team-native/.mcp.json"))
				if err != nil || string(delivered) != original {
					t.Fatalf("native original not preserved at namespaced point: %v", err)
				}
			}
		})
	}
}
