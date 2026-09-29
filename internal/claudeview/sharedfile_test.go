package claudeview

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A link the jail planted at the shared file's name is replaced, never read or written through,
// and a linked directory is refused: the shared dir is jail-writable (UpdateSharedFile).
func TestUpdateSharedFileNeverFollowsALink(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(t.TempDir(), "host-credentials.json")
	const hostBody = `{"claudeAiOauth":{"accessToken":"host"}}`
	if err := os.WriteFile(host, []byte(hostBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(dir, ViewFile)); err != nil {
		t.Fatal(err)
	}
	var seen []byte
	wrote, _, err := UpdateSharedFile(dir, ViewFile, func(cur []byte) ([]byte, error) {
		seen = cur
		return []byte(`{"claudeAiOauth":{"accessToken":"new"}}`), nil
	})
	if err != nil || !wrote {
		t.Fatalf("UpdateSharedFile = %v, %v; want a write", wrote, err)
	}
	if seen != nil {
		t.Errorf("the update read through the jail's link: %q", seen)
	}
	if got, _ := os.ReadFile(host); string(got) != hostBody {
		t.Errorf("the update wrote through the jail's link into the host file: %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(dir, ViewFile)); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the link was not replaced by a regular file: %v", err)
	}

	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateSharedFile(linked, ViewFile, func([]byte) ([]byte, error) {
		return []byte(`{}`), nil
	}); err == nil {
		t.Error("UpdateSharedFile wrote into a directory reached through a link")
	}
	if _, _, err := UpdateSharedFile(dir, "../escape", func([]byte) ([]byte, error) {
		return []byte(`{}`), nil
	}); err == nil {
		t.Error("UpdateSharedFile accepted a name that is not a leaf")
	}
}

// It takes Claude's storage lock beside the file and reports whether it held it, like a view.
func TestUpdateSharedFileTakesClaudesStorageLock(t *testing.T) {
	dir := t.TempDir()
	saved := StorageLockWait
	StorageLockWait = 100 * time.Millisecond
	t.Cleanup(func() { StorageLockWait = saved })
	write := func() bool {
		_, locked, err := UpdateSharedFile(dir, ViewFile, func(cur []byte) ([]byte, error) {
			if _, err := os.Stat(filepath.Join(dir, StorageLockDir)); err != nil {
				t.Errorf("the mutate ran without Claude's lock in place: %v", err)
			}
			return append(cur, 'x'), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return locked
	}
	if !write() {
		t.Error("an uncontended write did not hold Claude's storage lock")
	}
	if _, err := os.Lstat(filepath.Join(dir, StorageLockDir)); err == nil {
		t.Error("the lock was not released")
	}
}
