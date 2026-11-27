package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// managedMCPConsumerAdapter preserves this candidate's real Lua, surfaces and
// source metadata. Only the program installation recipe becomes an inert local
// fixture. It is a custom shadow, never embedded provenance or advice authority.
func managedMCPConsumerAdapter(t *testing.T, name, parent string) string {
	t.Helper()
	originals := packload.EmbeddedFS()
	manifestBytes, err := fs.ReadFile(originals, name+"/pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	decl, problems := packdecl.DecodeTOML(manifestBytes)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	root := filepath.Join(parent, name)
	// The programs below lose their update verbs, so their update probes go too (packdecl refuses a
	// probe no verb is asked before).
	decl.UpdateProbeByBin = nil
	for i := range decl.Contributes {
		c := &decl.Contributes[i]
		if c.Kind == packdecl.KindProgram {
			// A local installer declaration keeps effective program ownership;
			// neither readiness nor auto-capture may invoke it in this fixture.
			*c = packdecl.Contribution{Kind: packdecl.KindProgram, Bin: c.Bin, Via: "installer", URL: "file:///ctx/packs/" + name + "/never-install.sh", ProbeArgs: []string{"--version"}}
		}
	}
	encoded, err := packdecl.EncodeTOML(decl)
	if err != nil {
		t.Fatal(err)
	}
	decoded, problems := packdecl.DecodeTOML(encoded)
	if len(problems) != 0 || !reflect.DeepEqual(decoded.MCPAuth, decl.MCPAuth) || decoded.MCPHeaderForm != decl.MCPHeaderForm || !reflect.DeepEqual(decoded.MCPSignInFormats, decl.MCPSignInFormats) {
		t.Fatal("consumer adapter lost source/format declaration", problems)
	}
	for _, program := range decoded.Contributions() {
		if program.Kind == packdecl.KindProgram && (program.Via != "installer" || !strings.HasPrefix(program.URL, "file:///ctx/packs/")) {
			t.Fatal("fixture preparation graph can reach a vendor")
		}
	}
	write := func(rel string, body []byte, mode fs.FileMode) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("pack.toml", encoded, 0644)
	lua, err := fs.ReadFile(originals, name+"/derive.lua")
	if err != nil {
		t.Fatal(err)
	}
	write("derive.lua", lua, 0644)
	for _, c := range decoded.Contributions() {
		if c.Kind == packdecl.KindBriefing && c.From != "" {
			body, err := fs.ReadFile(originals, name+"/"+c.From)
			if err != nil {
				t.Fatal(err)
			}
			write(c.From, body, 0644)
		}
	}
	write("never-install.sh", []byte("#!/bin/sh\necho EXTERNAL_INSTALL_EXECUTED >&2\nexit 91\n"), 0644)
	actual, err := os.ReadFile(filepath.Join(root, "derive.lua"))
	if err != nil || !bytes.Equal(actual, lua) {
		t.Fatal("adapter Lua differs from shipped consumer")
	}
	return root
}

func managedMCPHome(t *testing.T, source, integration bool) string {
	t.Helper()
	packs := []string{}
	root := resolvedTempDir(t)
	for _, name := range []string{"claude", "pi"} {
		if name == "claude" && !source {
			continue
		}
		managedMCPConsumerAdapter(t, name, root)
		packs = append(packs, fmt.Sprintf(`{"source":%q,"name":%q}`, filepath.Join(root, name), name))
	}
	if integration {
		fixture := managedMCPIntegrationFixture(t, root)
		packs = append(packs, fmt.Sprintf(`{"source":%q,"name":"fixture-mcp"}`, fixture))
	}
	packs = append(packs, `"mcp-auth"`)
	allow := `"cgroup-delegate":{"off":true}`
	if source {
		allow += `,"claude-auth":{"off":true}`
	}
	// Legacy selection keeps existing leaf source IDs. Readable custom handles
	// containing '/' are rejected by Source.validate; no schema migration here.
	text := `{"packs":[` + strings.Join(packs, ",") + `],"allow":{` + allow + `},"mcp_servers":{"configured-alias":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once"}}}`
	home := mcpAuthHome(t, "")
	configDir := filepath.Join(home, ".config/yolo-jail")
	if err := os.Remove(filepath.Join(configDir, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.jsonc"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	// The harness's private HOME contains no owner source or provider credential.
	for _, name := range []string{".claude/.credentials.json", ".pi/agent/auth.json"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatal("fixture inherited a credential source", name, err)
		}
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatal("private fixture selection is invalid", err, problems)
	}
	adapters := 0
	for _, entry := range entries {
		if entry.Name == "claude" || entry.Name == "pi" {
			adapters++
			if entry.Embedded() {
				t.Fatal("custom consumer gained embedded provenance")
			}
			resolved, err := config.ResolvePack(entry, config.ResolvePackSpec{})
			if err != nil {
				t.Fatal(err)
			}
			for _, program := range resolved.Pack.Decl.Contributions() {
				if program.Kind == packdecl.KindProgram && (program.Via != "installer" || !strings.HasPrefix(program.URL, "file:///ctx/packs/")) {
					t.Fatal("effective fixture preparation can resolve a vendor")
				}
			}
		}
	}
	wantAdapters := 1
	if source {
		wantAdapters = 2
	}
	if adapters != wantAdapters {
		t.Fatal("fixture consumer shadow was not selected", adapters, wantAdapters)
	}
	stubHostBins(t, "claude", "pi")
	return home
}

func managedMCPRun(t *testing.T, workspace, script string) result {
	t.Helper()
	before := captureEntryNames(t, paths.CapturesDir())
	options := []runOption{withoutFixtureProgramInstall(), withEnv("YOLO_NO_UPDATE_CHECK=1", "YOLO_NO_AUTO_IMAGE_REAP=1")}
	if expected := os.Getenv("YOLO_TEST_MANAGED_MCP_EXPECTED_LAUNCHER"); expected != "" {
		options = append(options, func(cfg *runConfig) { cfg.expectedLauncher = expected })
	}
	r := runYolo(t, workspace, script, options...)
	after := captureEntryNames(t, paths.CapturesDir())
	if !reflect.DeepEqual(before, after) || strings.Contains(r.combined(), "EXTERNAL_INSTALL_EXECUTED") {
		t.Fatal("fixture executed external preparation or capture")
	}
	if r.rc != 0 {
		log, _ := os.ReadFile(filepath.Join(paths.GlobalStorage(), "logs/host-service-mcp-auth-broker.log"))
		t.Fatalf("managed fixture launch rc=%d: %s; daemon: %s", r.rc, r.stderr, log)
	}
	return r
}

const managedMCPRenderProbe = `python3 - <<'PY'
import json,os,subprocess
c=json.load(open(os.path.expanduser('~/.claude.json')))['mcpServers']['configured-alias']
p=json.load(open(os.path.join(os.environ['PI_CODING_AGENT_DIR'],'mcp.json')))['mcpServers']['configured-alias']
assert c['headersHelper']=='yolo mcp-auth header configured-alias --format json'
assert 'oauth' not in c
assert p['headers']['Authorization']=='!yolo mcp-auth header configured-alias'
assert 'oauth' not in p
manifest=json.load(open(os.path.expanduser('~/.claude/skills/fixture/.claude-plugin/plugin.json')))
assert manifest['name']=='fixture'
s=json.loads(subprocess.check_output(['yolo','mcp-auth','status','configured-alias','--json']))
assert 'plugin:fixture:fixture' in json.dumps(s)
row=[o for o in s['once'] if o['name']=='configured-alias'][0]
callback=row.get('callback','')
assert ('full callback URL' in json.dumps(row))==(callback not in ('owner','not needed')),(callback,row.get('next_step'))
print('MANAGED_CALLBACK='+callback)
print('MANAGED_RENDER=yes')
PY`

func TestManagedMCPSignInLaunchPlacesAndDerivesWithoutExecutingAgents(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || runtime.GOOS != "linux" {
		t.Skip("synthetic MCP endpoint slice is Linux podman")
	}
	home := managedMCPHome(t, true, true)
	workspace := writeProject(t, `{}`)
	for _, rel := range []string{"LICENSE", ".mcp.json"} {
		want := []byte(managedMCPFixtureFiles[rel])
		if err := os.WriteFile(filepath.Join(workspace, "expected-"+rel), want, 0600); err != nil {
			t.Fatal(err)
		}
	}
	compare := `python3 - <<'PY'
import os
for rel in ['LICENSE','.mcp.json']:
 assert open(os.path.expanduser('~/.claude/skills/fixture/'+rel),'rb').read()==open('/workspace/expected-'+rel,'rb').read()
print('ORIGINAL_BYTES=yes')
PY`
	r := managedMCPRun(t, workspace, managedMCPRenderProbe+"\n"+compare)
	if kvLine(r.stdout, "MANAGED_RENDER") != "yes" || kvLine(r.stdout, "ORIGINAL_BYTES") != "yes" {
		t.Fatalf("real boot consumer did not verify rendered routes and originals:\n%s", r.combined())
	}
	// The paste hint follows the callback's disposition (mcpsignin.Diagnose omits it for
	// a published "owner" callback). A nested jail publishes nothing, so there it must be
	// the unpublished branch; a bridged rootless host publishes the port and gets "owner".
	if got := kvLine(r.stdout, "MANAGED_CALLBACK"); inContainer() && got != "not published on this backend" {
		t.Fatalf("nested launch callback disposition = %q, want %q:\n%s", got, "not published on this backend", r.combined())
	}
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{"mcp_servers":{"configured-alias":{"url":"https://workspace.fixture.invalid/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	noBorrow := `python3 - <<'PY'
import json,os
c=json.load(open(os.path.expanduser('~/.claude.json')))['mcpServers']['configured-alias']
p=json.load(open(os.path.join(os.environ['PI_CODING_AGENT_DIR'],'mcp.json')))['mcpServers']['configured-alias']
assert c['url']=='https://workspace.fixture.invalid/mcp' and 'headersHelper' not in c
assert p['url']=='https://workspace.fixture.invalid/mcp'
assert not any('yolo mcp-auth header' in str(v) for v in p.get('headers',{}).values())
print('WORKSPACE_NO_BORROW=yes')
PY`
	if r := managedMCPRun(t, workspace, noBorrow); kvLine(r.stdout, "WORKSPACE_NO_BORROW") != "yes" {
		t.Fatalf("workspace override borrowed user sign-in route:\n%s", r.combined())
	}
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config/yolo-jail/config.jsonc")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var selected []any
	for _, entry := range cfg["packs"].([]any) {
		integration, ok := entry.(map[string]any)
		if !ok || integration["name"] != "fixture-mcp" {
			selected = append(selected, entry)
		}
	}
	cfg["packs"] = selected
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	withdrawn := `python3 - <<'PY'
import json,os
assert not os.path.exists(os.path.expanduser('~/.claude/skills/fixture'))
c=json.load(open(os.path.expanduser('~/.claude.json')))['mcpServers']['configured-alias']
assert 'headersHelper' not in c
print('WITHDRAWAL=yes')
PY`
	if r := managedMCPRun(t, workspace, withdrawn); kvLine(r.stdout, "WITHDRAWAL") != "yes" {
		t.Fatalf("withdrawn bundle remained a managed sign-in route:\n%s", r.combined())
	}
}

func TestManagedMCPSignInHeaderOnlyStatusReadsSyntheticRecord(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || runtime.GOOS != "linux" {
		t.Skip("synthetic MCP endpoint slice is Linux podman")
	}
	managedMCPHome(t, false, false)
	store, err := mcpauth.NewOnceStore(filepath.Join(loopholes.StateDirFor("mcp-auth-broker"), "once"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"client_id":"","headers":{},"transport":"http","url":"https://mcp.fixture.invalid/mcp"}`))
	fixtureToken := "task9-inert-access-only"
	record := mcpauth.Record{Server: "configured-alias", Identity: hex.EncodeToString(sum[:]), URL: "https://mcp.fixture.invalid/mcp", Generation: 1, State: "live", Entry: json.RawMessage(`{}`), AccessToken: fixtureToken, ExpiresAtMS: time.Now().Add(24 * time.Hour).UnixMilli()}
	if err := store.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records = []mcpauth.Record{record}; return nil }); err != nil {
		t.Fatal(err)
	}
	workspace := writeProject(t, `{}`)
	if err := os.WriteFile(filepath.Join(workspace, "private-expectation"), []byte(fixtureToken), 0600); err != nil {
		t.Fatal(err)
	}
	script := `python3 - <<'PY'
import json,subprocess
token=open('/workspace/private-expectation','rb').read()
s=subprocess.check_output(['yolo','mcp-auth','status','configured-alias','--json'])
assert token not in s
v=json.loads(s)
assert 'live' in str(v) and 'configured-alias' in str(v)
assert subprocess.check_output(['yolo','mcp-auth','header','configured-alias']).strip()==b'Bearer '+token
assert json.loads(subprocess.check_output(['yolo','mcp-auth','header','configured-alias','--format','json']))=={'Authorization':'Bearer '+token.decode()}
print('HEADER_ONLY=yes')
PY`
	r := managedMCPRun(t, workspace, script)
	if kvLine(r.stdout, "HEADER_ONLY") != "yes" || strings.Contains(r.combined(), fixtureToken) {
		t.Fatal("header-only reader leaked fixture token or omitted real row")
	}
	current, err := store.Read()
	if err != nil || len(current.Records) != 1 || current.Records[0].Generation != 1 || current.Records[0].RefreshToken != "" {
		t.Fatal("access-only header fixture refreshed or changed state", err)
	}
}

func TestManagedMCPCallbackLaunchPlanRetiresLease(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || runtime.GOOS != "linux" {
		t.Skip("callback disposition fixture is Linux podman")
	}
	home := managedMCPHome(t, true, true)
	workspace := writeProject(t, `{}`)
	script := `python3 - <<'PY'
import json,os
facts=json.loads(os.environ['YOLO_MCP_SIGN_IN'])
assert facts['callbacks']
for callback in facts['callbacks']:
 if callback.get('port'):
  assert callback['disposition']=='not published on this backend'
 else:
  assert callback['disposition']=='not requested'
assert not os.environ.get('YOLO_PUBLISHED_PORTS')
print('NESTED_CALLBACK=yes')
PY`
	if !inContainer() {
		t.Skip("this test pins nested no-publication, not real bridge reachability")
	}
	r := managedMCPRun(t, workspace, script)
	if kvLine(r.stdout, "NESTED_CALLBACK") != "yes" {
		t.Fatal("nested callback branch not observed")
	}
	state := filepath.Join(paths.GlobalStorageUnder(home), "state", "mcp-callback")
	if files, err := os.ReadDir(state); err == nil {
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "port-") {
				t.Fatal("nested launch acquired a callback lease")
			}
		}
	}
	assertManagedMCPInputsRetired(t, loopholes.StateDirFor("mcp-auth-broker"))
}

func assertManagedMCPInputsRetired(t *testing.T, state string) {
	t.Helper()
	files, err := os.ReadDir(filepath.Join(state, "replicas"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			t.Fatal("private managed launch input survived confirmed exit")
		}
	}
}
