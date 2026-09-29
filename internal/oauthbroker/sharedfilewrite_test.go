package oauthbroker

// sharedfilewrite_test.go pins the broker's write of the legacy shared credentials file once
// CL-D22's bridge makes it Claude's own store file (store.go, writeLegacy): a refresh replaces
// claudeAiOauth and keeps every other key Claude keeps there, it waits for Claude's
// `.storage-write.lock` beside the file, and a sign-out keeps the other keys too. Each drives the
// production entry point (the background tick, the sign-out verb), so reverting writeLegacy to a
// whole-file write fails them.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func mcpLogins() *jsonx.OrderedMap {
	mcp := jsonx.NewOrderedMap()
	mcp.Set("server|abc", map[string]any{"accessToken": "mcp-secret"})
	return mcp
}

func TestARefreshKeepsClaudesOtherKeysInTheSharedFile(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_old", "RT_old", nowMS()+20*60_000, nil)
	// Under the bridge a jail's Claude keeps its MCP servers' OAuth in this file.
	writeLogin(t, f.legacy, "AT_old", "RT_old", nowMS()+20*60_000, map[string]any{"mcpOAuth": mcpLogins()})

	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got := f.redemptions(); len(got) != 1 || got[0] != "RT_old" {
		t.Fatalf("upstream was sent %v, want the canonical refresh token once", got)
	}
	if got := str(oauthOf(t, f.legacy), "refreshToken"); got != "RT_1" {
		t.Errorf("the shared file holds refresh token %q, want RT_1", got)
	}
	mcp, ok := readRoot(t, f.legacy).Get("mcpOAuth")
	if !ok {
		t.Fatal("the refresh deleted the shared file's mcpOAuth: every jail's MCP logins, " +
			"which Claude keeps in this file once its store points here (CL-D22)")
	}
	if m, _ := mcp.(*jsonx.OrderedMap); m == nil || m.Len() != 1 {
		t.Errorf("mcpOAuth = %v, want the one server login kept as it was", mcp)
	}
}

func TestTheSharedFileIsWrittenUnderClaudesStorageLock(t *testing.T) {
	f := newViewFixture(t)
	savedWait := claudeview.StorageLockWait
	claudeview.StorageLockWait = 5 * time.Second
	t.Cleanup(func() { claudeview.StorageLockWait = savedWait })
	writeLogin(t, CanonicalPath, "AT_old", "RT_old", nowMS()+20*60_000, nil)
	writeLogin(t, f.legacy, "AT_old", "RT_old", nowMS()+20*60_000, nil)

	// A Claude mid read-modify-write holds its lock beside the file.
	lock := filepath.Join(filepath.Dir(f.legacy), claudeview.StorageLockDir)
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	// The views are written just before the shared file (CL-D12), so from that moment the
	// broker is at the shared file's write, which must wait for Claude to let go.
	reached := make(chan struct{})
	onViewsPublished = func() { close(reached) }
	t.Cleanup(func() { onViewsPublished = nil })
	whileHeld := make(chan string, 1)
	go func() {
		<-reached
		time.Sleep(200 * time.Millisecond)
		data, _ := os.ReadFile(f.legacy)
		whileHeld <- string(data)
		_ = os.Remove(lock)
	}()

	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	held := <-whileHeld
	var heldRoot *jsonx.OrderedMap
	if v, err := jsonx.Decode([]byte(held)); err == nil {
		heldRoot, _ = v.(*jsonx.OrderedMap)
	}
	if heldRoot != nil {
		if oa, _ := heldRoot.Get("claudeAiOauth"); oa != nil {
			if m, _ := oa.(*jsonx.OrderedMap); str(m, "refreshToken") != "RT_old" {
				t.Errorf("the broker rewrote the shared file (refresh token %q) while Claude held "+
					"its storage lock", str(m, "refreshToken"))
			}
		}
	}
	if got := str(oauthOf(t, f.legacy), "refreshToken"); got != "RT_1" {
		t.Errorf("after Claude let go the shared file holds %q, want RT_1", got)
	}
	if _, err := os.Lstat(lock); err == nil {
		t.Error("the broker left Claude's storage lock behind")
	}
}

func TestASignOutKeepsClaudesOtherKeysInTheSharedFile(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	writeLogin(t, f.legacy, "AT_machine", "RT_machine", nowMS()+7*3600_000,
		map[string]any{"mcpOAuth": mcpLogins()})

	if _, err := SignOut(); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	root := readRoot(t, f.legacy)
	if _, ok := root.Get("claudeAiOauth"); ok {
		t.Error("the sign-out left a Claude login in the shared file")
	}
	if _, ok := root.Get("mcpOAuth"); !ok {
		t.Error("the sign-out deleted the shared file's mcpOAuth, which is not the machine's login")
	}
}

// Any process in any claude jail can make Claude's storage lock unremovable: a stale lock with
// something inside it, which rmdir refuses. The broker writes the shared file holding
// refresh.lock, so a lock wait with no bound there is every jail's refresh on the machine
// blocked. The tick must come back within the lock wait and write the file without the lock.
func TestAnUnremovableStaleLockDoesNotHoldTheRefresh(t *testing.T) {
	f := newViewFixture(t)
	savedWait := claudeview.StorageLockWait
	claudeview.StorageLockWait = 200 * time.Millisecond
	t.Cleanup(func() { claudeview.StorageLockWait = savedWait })
	writeLogin(t, CanonicalPath, "AT_old", "RT_old", nowMS()+20*60_000, nil)
	writeLogin(t, f.legacy, "AT_old", "RT_old", nowMS()+20*60_000, nil)

	lock := filepath.Join(filepath.Dir(f.legacy), claudeview.StorageLockDir)
	if err := os.MkdirAll(filepath.Join(lock, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(claudeview.StorageLockWait + 5*time.Second):
		t.Fatal("the background tick was still inside the shared file's write, holding " +
			"refresh.lock, long after the storage lock wait")
	}
	if got := str(oauthOf(t, f.legacy), "refreshToken"); got != "RT_1" {
		t.Errorf("the shared file holds refresh token %q, want RT_1 written without the lock", got)
	}
}
