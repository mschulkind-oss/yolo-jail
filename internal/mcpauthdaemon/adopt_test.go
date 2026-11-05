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

func TestMCPAuthBrokerPreservesUnprovedNativeSignInBeforeSeed(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	dir := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(dir, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := "linear|1111111111111111"
	path := filepath.Join(dir, ".credentials.json")
	original := []byte(`{"linear|1111111111111111":{"server_url":"https://mcp.example/linear","access_token":"fake-new-access","refresh_token":"fake-new-refresh","expires_at":9999999999999,"client_id":"fake-client"},"private":{"refresh_token":"private-only"}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	script = filepath.Join(t.TempDir(), "derive.lua")
	if err := os.WriteFile(script, scriptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token", ExpiresAt: packdecl.MCPAuthExpiryField{Path: "expires_at", Unit: "ms"}, ClientID: "client_id"}}
	share := mcpauth.Share{Version: 1, Jail: "jail-synthetic", Workspace: workspace, WrittenAt: time.Now(), Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: script, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", URL: "https://mcp.example/linear", Identity: strings.Repeat("a", 64), Key: key}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := b.adoptAtStart(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	file, err := canonical.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Records) != 0 {
		t.Fatal("native sign-in without header provenance promoted to user canonical")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]map[string]any
	if err := json.Unmarshal(bytes, &entries); err != nil {
		t.Fatal(err)
	}
	if entries[key]["refresh_token"] != "fake-new-refresh" {
		t.Fatal("unproved native sign-in had its refresh token stripped")
	}
	if entries["private"]["refresh_token"] != "private-only" {
		t.Fatal("startup adoption changed a workspace-only entry")
	}
}
