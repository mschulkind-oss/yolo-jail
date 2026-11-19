package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func qualifiedMCPConsumer(t *testing.T, parent string) string {
	t.Helper()
	originals := packload.EmbeddedFS()
	body, err := fs.ReadFile(originals, "claude/pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	decl, problems := packdecl.DecodeTOML(body)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	decl.Needs = nil
	for i := range decl.Contributes {
		c := &decl.Contributes[i]
		if c.Kind == packdecl.KindProgram {
			*c = packdecl.Contribution{Kind: packdecl.KindProgram, Bin: c.Bin, Via: "installer", URL: "file:///never-execute-qualified-fixture", ProbeArgs: []string{"--version"}}
		}
	}
	encoded, err := packdecl.EncodeTOML(decl)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "claude")
	writeFile(t, filepath.Join(root, "pack.toml"), string(encoded))
	lua, err := fs.ReadFile(originals, "claude/derive.lua")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "derive.lua"), string(lua))
	for _, c := range decl.Contributions() {
		if c.Kind == packdecl.KindBriefing && c.From != "" {
			content, err := fs.ReadFile(originals, "claude/"+c.From)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, c.From), string(content))
		}
	}
	return root
}

func TestManagedMCPQualifiedSourceThroughTOMLShareAdoptionAndHostHeader(t *testing.T) {
	home := hostComputedHome(t, `{"packs":[],"host_management":"own"}`)
	t.Setenv(claudeview.SecureStorageEnv, "")
	t.Setenv("YOLO_NO_PROGRAM_READINESS", "1")
	t.Setenv("YOLO_NO_AUTO_CAPTURE", "1")
	parent := t.TempDir()
	qualifiedMCPConsumer(t, parent)
	var base string
	var redemptions atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "token_endpoint": base + "/token"})
		case "/token":
			redemptions.Add(1)
			_ = r.ParseForm()
			if r.PostForm.Get("client_id") != "public-fixture" || r.PostForm.Get("refresh_token") != "synthetic-qualified-refresh" {
				t.Error("synthetic issuer received wrong client or grant")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-qualified-adopted", "token_type": "Bearer", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base = srv.URL
	oldTransport := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	bundle := filepath.Join(parent, "bundle")
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
	writeFile(t, filepath.Join(bundle, "skills/native/LICENSE"), "MIT License")
	writeFile(t, filepath.Join(bundle, "skills/native/.claude-plugin/plugin.json"), `{"name":"native"}`)
	writeFile(t, filepath.Join(bundle, "skills/native/.mcp.json"), fmt.Sprintf(`{"mcpServers":{"native-key":{"type":"http","url":%q,"oauth":{"clientId":"public-fixture"}}}}`, base+"/mcp"))
	if err := os.Remove(filepath.Join(home, ".config/yolo-jail/config.jsonc")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".config/yolo-jail/config.toml"), fmt.Sprintf(`packs=["team/claude","team/bundle","mcp-auth"]
host_management="own"
pack_sources={team={path=%q,bare=true}}
[mcp_servers.configured-alias]
url=%q
sign_in="once"
[mcp_servers.configured-alias.oauth]
client_id="public-fixture"
`, parent, base+"/mcp"))
	selected := selectConfiguredHostPacks()
	if len(selected.problems()) != 0 {
		t.Fatal(selected.problems())
	}
	var out, errs bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errs, false, nil); rc != 0 {
		t.Fatalf("qualified inert apply failed: rc=%d %s %s", rc, out.String(), errs.String())
	}
	share, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), selected.packs, home, home)
	if err != nil || len(share.Sources) != 1 || share.Sources[0].Pack != "team/claude" {
		t.Fatalf("actual qualified producer lost logical source: %v", err)
	}
	if err := share.Validate(); err != nil {
		t.Fatalf("rendered qualified helper has an unusable actual share: %v", err)
	}
	if share.Sources[0].AdapterName() != "claude" {
		t.Fatal("logical handle was confused with declared adapter")
	}
	remote := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
	if remote["headersHelper"] != "yolo mcp-auth header configured-alias --format json" {
		t.Fatal("qualified capturable point did not enable actual consumer helper")
	}
	// Unsupported registrations cannot borrow the built-in adapter merely because
	// the logical handle's leaf is claude. The actual source consumer stays plain.
	originalLua, err := os.ReadFile(share.Sources[0].DeriveFile)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := strings.Replace(string(originalLua), `yolo.mcp_auth("claude",`, `yolo.mcp_auth("other",`, 1)
	writeFile(t, share.Sources[0].DeriveFile, unsupported)
	noAdapter, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), selected.packs, home, home)
	if err != nil || len(noAdapter.Sources) != 0 {
		t.Fatal("unsupported qualified adapter was an executable source", err)
	}
	qualifiedMCPPlainConsumer(t, home, base+"/mcp", noAdapter.SignIn)
	writeFile(t, share.Sources[0].DeriveFile, string(originalLua))
	// Selection, not a same-leaf directory or the existence of built-in bytes,
	// determines the producer's sources and managed eligibility.
	configPath := filepath.Join(home, ".config/yolo-jail/config.toml")
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, configPath, strings.Replace(string(originalConfig), `"team/claude",`, "", 1))
	unselected := selectConfiguredHostPacks()
	withoutSource, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), unselected.packs, home, home)
	if err != nil || len(withoutSource.Sources) != 0 {
		t.Fatal("unselected qualified source was borrowed", err)
	}
	qualifiedMCPPlainConsumer(t, home, base+"/mcp", withoutSource.SignIn)
	writeFile(t, configPath, string(originalConfig))
	entry := map[string]any{"accessToken": "synthetic-qualified-initial", "refreshToken": "synthetic-qualified-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "clientId": "public-fixture", "issuer": base, "serverName": "plugin:native:native-key", "serverUrl": base + "/mcp"}
	body, _ := json.Marshal(map[string]any{"mcpOAuth": map[string]any{"plugin:native:native-key|0000000000000000": entry}})
	source := share.Sources[0]
	writeFile(t, filepath.Join(source.Dir, source.Store), string(body))
	if err := os.Chmod(filepath.Join(source.Dir, source.Store), 0600); err != nil {
		t.Fatal(err)
	}
	state := loopholes.StateDirFor("mcp-auth-broker")
	for _, wrong := range []string{"adapter", "script"} {
		bad := share
		bad.Sources = append([]mcpauth.Source(nil), share.Sources...)
		if wrong == "adapter" {
			bad.Sources[0].Adapter = "pi"
		} else {
			foreign := filepath.Join(t.TempDir(), "derive.lua")
			writeFile(t, foreign, `yolo.mcp_auth("pi", {key=function(s) return "x" end,match=function(k,e) return nil end})`)
			bad.Sources[0].DeriveFile = foreign
		}
		if err := mcpauthdaemon.BoundaryPass(context.Background(), state, bad, nil); err == nil || redemptions.Load() != 0 {
			t.Fatal("wrong adapter/script borrowed a source", wrong, err, redemptions.Load())
		}
	}
	badClient := share
	badClient.Once = append([]mcpauth.OnceServer(nil), share.Once...)
	badClient.Once[0].ExpectedClientID = "wrong-client"
	if err := mcpauthdaemon.BoundaryPass(context.Background(), state, badClient, nil); err != nil || redemptions.Load() != 0 {
		t.Fatal("wrong native client was redeemed", err, redemptions.Load())
	}
	wrongEntry := make(map[string]any, len(entry))
	for key, value := range entry {
		wrongEntry[key] = value
	}
	wrongEntry["serverUrl"] = "https://wrong-source.invalid/mcp"
	wrongBody, _ := json.Marshal(map[string]any{"mcpOAuth": map[string]any{"plugin:native:native-key|0000000000000000": wrongEntry}})
	if err := os.WriteFile(filepath.Join(source.Dir, source.Store), wrongBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mcpauthdaemon.BoundaryPass(context.Background(), state, share, nil); err != nil || redemptions.Load() != 0 {
		t.Fatal("unrelated source entry was redeemed", err, redemptions.Load())
	}
	if err := os.WriteFile(filepath.Join(source.Dir, source.Store), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mcpauthdaemon.BoundaryPass(context.Background(), state, share, nil); err != nil {
		t.Fatalf("actual qualified source adapter could not adopt synthetic sign-in: %v", err)
	}
	if redemptions.Load() != 1 {
		t.Fatal("actual qualified match did not redeem exactly once", redemptions.Load())
	}
	rc, reply, diagnostic := captureStd(t, func() int {
		return Main([]string{"yolo", "mcp-auth", "header", "configured-alias", "--format", "json"})
	})
	if rc != 0 {
		t.Fatalf("actual qualified host helper refused adopted record: rc=%d %s", rc, diagnostic)
	}
	var header map[string]string
	if json.Unmarshal([]byte(reply), &header) != nil || header["Authorization"] != "Bearer synthetic-qualified-adopted" {
		t.Fatal("qualified host helper did not issue privately compared synthetic header")
	}
	if redemptions.Load() != 1 || strings.Contains(diagnostic, "synthetic-qualified") {
		t.Fatal("host helper redeemed or disclosed synthetic credentials")
	}
	writeFile(t, configPath, strings.Replace(string(originalConfig), base+"/mcp", "https://override.invalid/mcp", 1))
	redirected, err := run.HostHeaderShare(config.UserScopeConfigOrEmpty(), selectConfiguredHostPacks().packs, home, home)
	if err != nil {
		t.Fatal(err)
	}
	qualifiedMCPPlainConsumer(t, home, "https://override.invalid/mcp", redirected.SignIn)
	rc, reply, _ = captureStd(t, func() int { return Main([]string{"yolo", "mcp-auth", "header", "configured-alias"}) })
	if rc == 0 || reply != "" || redemptions.Load() != 1 {
		t.Fatal("identity override borrowed the qualified source's canonical header")
	}
}

func qualifiedMCPPlainConsumer(t *testing.T, workspace, url string, facts *mcpsignin.Launch) {
	t.Helper()
	wire, _ := json.Marshal(facts)
	servers, _ := json.Marshal(map[string]any{"configured-alias": map[string]any{"url": url, "sign_in": "once", "oauth": map[string]any{"client_id": "public-fixture"}}})
	e := &entrypoint.Env{Home: t.TempDir(), Workspace: workspace, Vars: map[string]string{mcpsignin.WireEnv: string(wire), entrypoint.MCPServersWireEnv: string(servers), "YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT": "/private-fixture.endpoint"}, Stderr: &bytes.Buffer{}}
	if err := entrypoint.ConfigurePackByName(e, "claude"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.Home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(body, &config) != nil {
		t.Fatal("invalid rendered config")
	}
	remote := config["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
	if remote["headersHelper"] != nil || remote["url"] != url {
		t.Fatal("unsupported/unselected/overridden facts did not keep native plaintext")
	}
}
