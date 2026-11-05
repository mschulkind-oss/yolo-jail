package mcpauthdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestMCPAuthBrokerSeedsAndRetiresOnlyProvedCodexCopies(t *testing.T) {
	workspace := t.TempDir()
	state := t.TempDir()
	root := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(root, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	key := "linear|1111111111111111"
	deriveFile, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", Store: ".codex/.credentials.json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token", ExpiresAt: packdecl.MCPAuthExpiryField{Path: "expires_at", Unit: "ms"}, ClientID: "client_id"}}
	share := mcpauth.Share{Version: 1, Jail: "jail-synthetic", Workspace: workspace, WrittenAt: time.Now(), Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: deriveFile, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", URL: "https://mcp.example/linear", Identity: identity, Key: key}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	original := mcpauth.Record{Server: "linear", Identity: identity, URL: "https://mcp.example/linear", Generation: 1, State: "live", Entry: json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"fake-access-1","refresh_token":"fake-refresh-1","expires_at":9999999999999,"client_id":"fake-client"}`)}
	if err := canonical.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records = []mcpauth.Record{original}; return nil }); err != nil {
		t.Fatal(err)
	}
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	wrong := []byte(`{"linear|1111111111111111":{"server_url":"https://wrong.example/mcp","access_token":"fake-wrong","refresh_token":"fake-wrong-refresh","expires_at":9999999999999,"client_id":"fake-client"}}`)
	path := filepath.Join(root, ".credentials.json")
	if err := os.WriteFile(path, wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(path)
	if err != nil || string(preserved) != string(wrong) {
		t.Fatal("refused seed changed a mismatched native entry")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The old workspace used a different header at this same name, URL and
	// client ID. Codex's native file does not record it, so key/URL cannot
	// promote this unknown refresh credential into the user identity.
	oldWorkspace := []byte(`{"linear|1111111111111111":{"server_url":"https://mcp.example/linear","access_token":"workspace-override-access","refresh_token":"workspace-override-refresh","expires_at":9999999999999,"client_id":"fake-client"}}`)
	if err := os.WriteFile(path, oldWorkspace, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.adoptAtStart(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(oldWorkspace) {
		t.Fatal("old workspace header identity was overwritten")
	}
	current, err := canonical.Read()
	if err != nil || len(current.Records) != 1 || current.Records[0].Generation != 1 {
		t.Fatal("old workspace sign-in promoted into user canonical")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	b.share.Replicas[0].Shared[0].ClientID = "different-client"
	if err := b.seed(); err == nil {
		t.Fatal("canonical native client ID mismatch was accepted")
	}
	b.share.Replicas[0].Shared[0].ClientID = "fake-client"
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if entries[key]["access_token"] != "fake-access-1" {
		t.Fatal("seed missing synthetic access token")
	}
	if _, ok := entries[key]["refresh_token"]; ok {
		t.Fatal("seed leaked a refresh token")
	}
	entries[key]["access_token"] = "fake-access-2"
	entries[key]["refresh_token"] = "fake-refresh-2"
	entries["private|synthetic"] = map[string]any{"refresh_token": "private-refresh"}
	changed, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.harvest(); err != nil {
		t.Fatal(err)
	}
	records, err := canonical.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Records) != 1 || records.Records[0].Generation != 1 || records.Records[0].RefreshToken != "" {
		t.Fatal("unproved fresh sign-in promoted to canonical")
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if entries[key]["refresh_token"] != "fake-refresh-2" {
		t.Fatal("unproved native refresh was stripped")
	}
	if entries["private|synthetic"]["refresh_token"] != "private-refresh" {
		t.Fatal("unshared entry was changed")
	}
	// A second workspace's older broker-written, refresh-free replica must not
	// survive a clean sign-out in A merely because canonical is now absent.
	other := t.TempDir()
	otherRoot := filepath.Join(other, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(otherRoot, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	otherShare := share
	otherShare.Workspace = other
	second := broker{share: otherShare, canonical: canonical}
	if err := second.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := second.seed(); err != nil {
		t.Fatal(err)
	}
	delete(entries, key)
	changed, err = json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.harvest(); err != nil {
		t.Fatal(err)
	}
	records, err = canonical.Read()
	if err != nil || len(records.Records) != 0 {
		t.Fatalf("sign-out did not retire canonical: %v, %+v", err, records)
	}
	relaunch := broker{share: otherShare, canonical: canonical}
	if err := relaunch.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := relaunch.adoptAtStart(); err != nil {
		t.Fatal(err)
	}
	if err := relaunch.seed(); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(otherRoot, ".credentials.json")
	otherData, err := os.ReadFile(otherPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var otherEntries map[string]json.RawMessage
	if len(otherData) > 0 && json.Unmarshal(otherData, &otherEntries) != nil {
		t.Fatal("malformed other store")
	}
	if _, remains := otherEntries[key]; remains {
		t.Fatal("stale managed access survived sign-out on relaunch")
	}
	// This freshly logged-in native copy is not broker managed and must survive.
	if err := os.WriteFile(otherPath, wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := relaunch.seed(); err != nil {
		t.Fatal(err)
	}
	unknown, err := os.ReadFile(otherPath)
	if err != nil || string(unknown) != string(wrong) {
		t.Fatal("unknown native sign-in was deleted")
	}
}
