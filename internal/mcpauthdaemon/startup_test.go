package mcpauthdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestMCPAuthSeedRespectsOneAbsoluteStartupBudgetAcrossNativeLock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hold      time.Duration
		wantError bool
	}{
		{"lock released within allowance", 50 * time.Millisecond, false},
		{"lock outlives total startup budget", 170 * time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, state := t.TempDir(), t.TempDir()
			native := filepath.Join(workspace, ".yolo", "home", "codex")
			if err := os.MkdirAll(filepath.Join(native, "mcp-oauth-locks"), 0o700); err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(filepath.Join(native, "mcp-oauth-locks", "file-store.lock"), os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
				t.Fatal(err)
			}
			released := make(chan struct{})
			time.AfterFunc(tc.hold, func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); close(released) })
			derive, err := filepath.Abs(filepath.Join("..", "..", "packs", "codex", "derive.lua"))
			if err != nil {
				t.Fatal(err)
			}
			decl := packdecl.MCPAuth{Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "mcp-oauth-locks/file-store.lock"}, Fields: packdecl.MCPAuthFields{AccessToken: "access_token", RefreshToken: "refresh_token"}}
			id := strings.Repeat("a", 64)
			share := mcpauth.Share{Workspace: workspace, Replicas: []mcpauth.Replica{{Pack: "codex", DeriveFile: derive, Root: []string{"home", "codex"}, Store: ".credentials.json", Lock: *decl.Lock, Decl: decl, Shared: []mcpauth.SharedEntry{{Name: "linear", Identity: id, URL: "https://mcp.example/linear", Key: "linear|1111111111111111"}}}}}
			canonical, err := mcpauth.NewCanonicalStore(state)
			if err != nil {
				t.Fatal(err)
			}
			entry := json.RawMessage(`{"server_url":"https://mcp.example/linear","access_token":"fake-access","refresh_token":"fake-refresh"}`)
			if err := canonical.WithLock(func(f *mcpauth.CanonicalFile) error {
				f.Records = []mcpauth.Record{{Server: "linear", Identity: id, URL: "https://mcp.example/linear", Generation: 1, State: "live", Entry: entry}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			b := broker{share: share, canonical: canonical, startupDeadline: time.Now().Add(115 * time.Millisecond)}
			if err := b.loadScripts(); err != nil {
				t.Fatal(err)
			}
			err = b.seed()
			<-released
			if tc.wantError && err == nil {
				t.Fatal("seed ignored its absolute startup budget")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("lock released within allowance: %v", err)
			}
			if tc.wantError {
				if _, err := os.Stat(filepath.Join(native, ".credentials.json")); !os.IsNotExist(err) {
					t.Fatal("late seed wrote an access token")
				}
			}
		})
	}
}
