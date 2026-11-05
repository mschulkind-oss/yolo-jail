package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestMCPAuthBrokerSeedsButDoesNotAdoptUnprovedCodexSignIn crosses the selected pack, core launch
// producer, real host daemon and jailed Codex-shaped file with no agent or API call.
func TestMCPAuthBrokerSeedsButDoesNotAdoptUnprovedCodexSignIn(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || goruntime.GOOS != "linux" {
		t.Skip("Codex MCP sharing slice is Linux podman only")
	}
	// requireJail isolates config, but links host state. Replace HOME altogether before
	// invoking the launcher: no link to any real config, cache, or credential store.
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configText := "packs = [\"codex\"]\n[active_provider]\ncodex = \"codex\"\n[mcp_servers.linear]\nurl = \"https://mcp.example.test/mcp\"\n[mcp_servers.env]\nurl = \"https://mcp.example.test/env\"\nrequires_env = [\"YOLO_UNSET_SYNTHETIC_MCP\"]\n[mcp_servers.capability]\nurl = \"https://mcp.example.test/capability\"\nprovides = \"web_search\"\n"
	t.Setenv("YOLO_UNSET_SYNTHETIC_MCP", "")
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := writeProject(t, `{"mcp_servers":{"private":{"url":"https://workspace.example/mcp"}}}`)
	state := loopholes.StateDirFor("mcp-auth-broker")
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://mcp.example.test/mcp"
	identitySum := sha256.Sum256([]byte(`{"client_id":"","headers":{},"transport":"http","url":"https://mcp.example.test/mcp"}`))
	identity := hex.EncodeToString(identitySum[:])
	keySum := sha256.Sum256([]byte(`{"headers":{},"type":"http","url":"https://mcp.example.test/mcp"}`))
	key := "linear|" + hex.EncodeToString(keySum[:8])
	entry, _ := json.Marshal(map[string]any{"server_url": url, "access_token": "fake-access-seed", "refresh_token": "fake-refresh-seed", "expires_at": time.Now().Add(24 * time.Hour).UnixMilli(), "client_id": "fake-client"})
	record := mcpauth.Record{Server: "linear", Identity: identity, URL: url, Generation: 1, State: "live", Entry: entry, AccessToken: "fake-access-seed", RefreshToken: "fake-refresh-seed"}
	if err := canonical.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records = []mcpauth.Record{record}; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, withheld := range []string{"env", "capability"} {
		withheldURL := "https://mcp.example.test/" + withheld
		identityBytes := sha256.Sum256([]byte(fmt.Sprintf(`{"client_id":"","headers":{},"transport":"http","url":%q}`, withheldURL)))
		withheldEntry, _ := json.Marshal(map[string]any{"server_url": withheldURL, "access_token": "fake-withheld-access", "refresh_token": "fake-withheld-refresh", "expires_at": time.Now().Add(24 * time.Hour).UnixMilli(), "client_id": "fake-client"})
		other := mcpauth.Record{Server: withheld, Identity: hex.EncodeToString(identityBytes[:]), URL: withheldURL, Generation: 1, State: "live", Entry: withheldEntry}
		if err := canonical.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records = append(file.Records, other); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// The script never runs Codex, prints token bytes, or reaches a remote MCP endpoint.
	script := `python3 - <<'PY'
import hashlib,json,os,fcntl,tomllib
with open(os.path.expanduser('~/.codex/config.toml'),'rb') as config:
 settings=tomllib.load(config)
 print('FILE_STORE='+str(settings.get('mcp_oauth_credentials_store')))
 print('WITHHELD_RENDERED='+str(any(n in settings.get('mcp_servers',{}) for n in ('env','capability'))))
p=os.path.expanduser('~/.codex/.credentials.json')
lock=os.path.expanduser('~/.codex/mcp-oauth-locks/file-store.lock')
key='` + key + `'
with open(lock,'a') as f:
 fcntl.flock(f,fcntl.LOCK_EX)
 with open(p) as src: data=json.load(src)
 print('SEED_ACCESS_HASH='+hashlib.sha256(data[key]['access_token'].encode()).hexdigest())
 print('SEED_HAS_REFRESH='+str('refresh_token' in data[key]))
 print('PRIVATE_SEEDED='+str('private|synthetic' in data))
 print('WITHHELD_STORED='+str(any(n+'|'+hashlib.sha256(json.dumps({'headers':{},'type':'http','url':'https://mcp.example.test/'+n},separators=(',',':'),sort_keys=True).encode()).hexdigest()[:16] in data for n in ('env','capability'))))
 data['private|synthetic']={'refresh_token':'workspace-private-refresh'}
 data[key]['access_token']='fake-access-harvest'
 data[key]['refresh_token']='fake-refresh-harvest'
 with open(p,'w') as dst: json.dump(data,dst)
PY`
	result := runYolo(t, workspace, script, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if result.rc != 0 {
		logPath := filepath.Join(paths.GlobalStorageUnder(home), "logs", "host-service-mcp-auth-broker.log")
		daemonLog, _ := os.ReadFile(logPath)
		t.Fatalf("synthetic MCP jail exited %d; daemon: %s; launcher: %s", result.rc, daemonLog, result.stderr)
	}
	// This ordinary selected launch actually traversed keeper publication and
	// the ending reader: neither the token-free result nor its per-generation
	// claim/lease may survive a confirmed last session.
	cname := yoloruntime.FromWorkspace(workspace)
	owners := filepath.Join(paths.GlobalStorageUnder(home), "owners")
	files, err := os.ReadDir(owners)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), cname+".mcp-teardown-") {
			t.Fatalf("selected outcome was not retired: %s", file.Name())
		}
	}
	generations, err := os.ReadDir(filepath.Join(owners, cname+".mcp-outcomes"))
	if err != nil || len(generations) != 0 {
		t.Fatalf("selected generation was not retired: %v %v", generations, err)
	}
	accessSum := sha256.Sum256([]byte("fake-access-seed"))
	if kvLine(result.stdout, "FILE_STORE") != "file" || kvLine(result.stdout, "PRIVATE_SEEDED") != "False" || kvLine(result.stdout, "WITHHELD_RENDERED") != "False" || kvLine(result.stdout, "WITHHELD_STORED") != "False" || kvLine(result.stdout, "SEED_ACCESS_HASH") != hex.EncodeToString(accessSum[:]) || kvLine(result.stdout, "SEED_HAS_REFRESH") != "False" {
		t.Fatalf("seed was absent or carried refresh; probe: %s", result.stdout)
	}
	current, err := canonical.Read()
	if err != nil || len(current.Records) != 3 {
		t.Fatalf("unproved native login was promoted to canonical: %v, %+v", err, current)
	}
	for _, record := range current.Records {
		if record.Generation != 1 || record.Server == "linear" && record.RefreshToken != "fake-refresh-seed" {
			t.Fatal("unproved native login changed canonical record")
		}
	}
	data, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(workspace), "home", "codex", ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	if values[key]["refresh_token"] != "fake-refresh-harvest" || values["private|synthetic"]["refresh_token"] != "workspace-private-refresh" {
		t.Fatal("unknown native login or unshared entry was modified")
	}
	audit, err := os.ReadFile(filepath.Join(paths.BrokerDir(), "audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), `"event":"seed"`) {
		t.Fatalf("seed audit missing: %v", err)
	}
	if strings.Contains(string(audit), `"event":"adopt"`) || strings.Contains(string(audit), "fake-refresh-") || strings.Contains(string(audit), "fake-access-") {
		t.Fatal("unproved adoption or credential data in audit")
	}
	files, err = os.ReadDir(filepath.Join(state, "replicas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			t.Fatal("private launch input outlived observed exit")
		}
	}
	if !strings.Contains(result.stderr, "mcp-auth-broker") {
		t.Fatal("selected broker was not disclosed")
	}
}
