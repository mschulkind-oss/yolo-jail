package mcpauthdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestMCPAuthBrokerReplacesOnlyAnExactManagedReplica(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(root, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	derive, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token", ClientID: "client_id"}}
	id := strings.Repeat("a", 64)
	share := mcpauth.Share{Workspace: workspace, Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: derive, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", Identity: id, URL: "https://mcp.example/linear", Key: "linear|1111111111111111", ClientID: "same-client"}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	makeRecord := func(generation uint64, access string) mcpauth.Record {
		entry, _ := json.Marshal(map[string]any{"server_url": "https://mcp.example/linear", "access_token": access, "refresh_token": "fake-refresh", "client_id": "same-client"})
		return mcpauth.Record{Server: "linear", Identity: id, URL: "https://mcp.example/linear", Generation: generation, State: "live", Entry: entry}
	}
	if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
		f.Records = []mcpauth.Record{makeRecord(1, "first-access")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
		f.Records = []mcpauth.Record{makeRecord(2, "second-access")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	key := "linear|1111111111111111"
	if entries[key]["access_token"] != "second-access" {
		t.Fatal("trusted exact broker copy did not receive newer canonical access")
	}
	provenance := filepath.Join(state, b.managedName(share.Replicas[0], share.Replicas[0].Servers()[0]))
	originalProvenance, err := os.ReadFile(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provenance, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err == nil {
		t.Fatal("unreadable managed provenance was treated as absent ownership")
	}
	if err := os.WriteFile(provenance, originalProvenance, 0o600); err != nil {
		t.Fatal(err)
	}
	entries[key]["access_token"] = "unknown-login-access"
	entries[key]["refresh_token"] = "unknown-login-refresh"
	changed, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if entries[key]["access_token"] != "unknown-login-access" || entries[key]["refresh_token"] != "unknown-login-refresh" {
		t.Fatal("unknown native sign-in overwritten")
	}
	if err := b.harvest(); err != nil {
		t.Fatal(err)
	}
	current, err := canonical.Read()
	if err != nil || len(current.Records) != 1 || current.Records[0].Generation != 2 {
		t.Fatalf("unknown login promoted: %v %+v", err, current)
	}
}

func TestMCPAuthBrokerDoesNotReplaceChangedLargeNativeExpiry(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(root, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	derive, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token", ExpiresAt: packdecl.MCPAuthExpiryField{Path: "expires_at", Unit: "ms"}}}
	key := "linear|1111111111111111"
	share := mcpauth.Share{Workspace: workspace, Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: derive, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", Identity: strings.Repeat("a", 64), URL: "https://mcp.example/linear", Key: key}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	generation := uint64(0)
	put := func(access string) {
		generation++
		t.Helper()
		entry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"` + access + `","refresh_token":"fake-refresh","expires_at":9007199254740992}`)
		if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
			f.Records = []mcpauth.Record{{Server: "linear", Identity: share.Replicas[0].Shared[0].Identity, URL: "https://mcp.example/linear", State: "live", Generation: generation, Entry: entry}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("first")
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "9007199254740992", "9007199254740993", 1)
	if changed == string(data) {
		t.Fatal("seed did not contain expiry")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	put("second")
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "9007199254740993") || strings.Contains(string(data), "second") {
		t.Fatal("changed native expiry was overwritten as exact managed copy")
	}
}

func TestMCPAuthBrokerRetiresProvedOldHeaderIdentityBeforeSeedingNewCanonical(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(root, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	derive, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token"}}
	script, err := os.ReadFile(derive)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := luahook.MCPAuthKeys(string(script), "codex", []luahook.MCPServerRef{{Name: "linear", URL: "https://mcp.example/linear"}})
	if err != nil {
		t.Fatal(err)
	}
	key := keys["linear"]
	old, fresh := strings.Repeat("a", 64), strings.Repeat("b", 64)
	share := mcpauth.Share{Workspace: workspace, Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: derive, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", Identity: old, URL: "https://mcp.example/linear", Key: key}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	put := func(id, access string) {
		t.Helper()
		entry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"` + access + `","refresh_token":"fake-refresh"}`)
		if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
			f.Records = []mcpauth.Record{{Server: "linear", Identity: id, URL: "https://mcp.example/linear", State: "live", Generation: 1, Entry: entry}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(old, "old")
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	foreign := b
	foreign.share.Workspace = t.TempDir()
	foreignEntry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"other-workspace"}`)
	if err := foreign.writeManaged(share.Replicas[0], share.Replicas[0].Servers()[0], foreignEntry); err != nil {
		t.Fatal(err)
	}
	foreignName := foreign.managedName(share.Replicas[0], share.Replicas[0].Servers()[0])
	path := filepath.Join(root, ".credentials.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	put(fresh, "new")
	share.Replicas[0].Shared[0].Identity = fresh
	b.share = share
	changed := strings.Replace(string(original), `"access_token":"old"`, `"access_token":"native-login"`, 1)
	if changed == string(original) {
		t.Fatal("no old native copy to change")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err == nil {
		t.Fatal("changed old-identity native entry was allowed into new identity")
	}
	unmodified, err := os.ReadFile(path)
	if err != nil || string(unmodified) != changed {
		t.Fatal("unknown changed native entry was not preserved")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"access_token":"new"`) {
		t.Fatal("old broker credential remains under new header identity")
	}
	managed, err := b.readManaged(share.Replicas[0], share.Replicas[0].Servers()[0])
	if err != nil || managed == nil || managed.Identity != fresh {
		t.Fatalf("scoped provenance did not advance to new identity: %v", err)
	}
	other, err := os.ReadFile(filepath.Join(state, foreignName))
	if err != nil || !strings.Contains(string(other), "other-workspace") {
		t.Fatalf("unrelated workspace ledger was changed: %v", err)
	}
}

func TestMCPAuthBrokerRetiresProvedOldHeaderIdentityWithoutNewCanonical(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	root := filepath.Join(workspace, ".yolo", "home", "codex")
	if err := os.MkdirAll(filepath.Join(root, "mcp-oauth-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	derive, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token"}}
	script, err := os.ReadFile(derive)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := luahook.MCPAuthKeys(string(script), "codex", []luahook.MCPServerRef{{Name: "linear", URL: "https://mcp.example/linear"}})
	if err != nil {
		t.Fatal(err)
	}
	key := keys["linear"]
	old, fresh := strings.Repeat("a", 64), strings.Repeat("b", 64)
	share := mcpauth.Share{Workspace: workspace, Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: derive, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", Identity: old, URL: "https://mcp.example/linear", Key: key}}}}}
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	put := func(id, access string) {
		t.Helper()
		entry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"` + access + `","refresh_token":"fake-refresh"}`)
		if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
			f.Records = []mcpauth.Record{{Server: "linear", Identity: id, URL: "https://mcp.example/linear", State: "live", Generation: 1, Entry: entry}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(old, "old")
	b := broker{share: share, canonical: canonical}
	if err := b.loadScripts(); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	foreign := b
	foreign.share.Workspace = t.TempDir()
	foreignEntry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"other-workspace"}`)
	if err := foreign.writeManaged(share.Replicas[0], share.Replicas[0].Servers()[0], foreignEntry); err != nil {
		t.Fatal(err)
	}
	foreignName := foreign.managedName(share.Replicas[0], share.Replicas[0].Servers()[0])
	path := filepath.Join(root, ".credentials.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
		f.Records = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	share.Replicas[0].Shared[0].Identity = fresh
	b.share = share
	changed := strings.Replace(string(original), `"access_token":"old"`, `"access_token":"native-login"`, 1)
	if changed == string(original) {
		t.Fatal("no old native copy to change")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err == nil {
		t.Fatal("changed old-identity native entry was allowed into new identity")
	}
	unmodified, err := os.ReadFile(path)
	if err != nil || string(unmodified) != changed {
		t.Fatal("unknown changed native entry was not preserved")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.seed(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old broker credential remains without new canonical record: %v", err)
	}
	managed, err := b.readManaged(share.Replicas[0], share.Replicas[0].Servers()[0])
	if err != nil || managed != nil {
		t.Fatalf("retired scoped provenance remains: %v", err)
	}
	other, err := os.ReadFile(filepath.Join(state, foreignName))
	if err != nil || !strings.Contains(string(other), "other-workspace") {
		t.Fatalf("unrelated workspace ledger was changed: %v", err)
	}
}
