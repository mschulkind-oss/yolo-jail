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

// finishesWithin runs update and fails the test when it has not returned within limit. The
// update is left running on failure: a spinning lock loop cannot be stopped from outside.
func finishesWithin(t *testing.T, limit time.Duration, update func() (bool, bool, error)) (wrote, locked bool) {
	t.Helper()
	type result struct {
		wrote, locked bool
		err           error
	}
	done := make(chan result, 1)
	go func() {
		w, l, err := update()
		done <- result{w, l, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.wrote, r.locked
	case <-time.After(limit):
		t.Fatalf("the write had not returned after %s; the lock wait is %s", limit, StorageLockWait)
		return false, false
	}
}

// A stale lock the broker cannot remove (the jail put something inside it, so rmdir fails) is
// waited on like a held one, and the write then goes ahead without it. It must never be retried
// without a pause or past the wait: the broker writes this file holding refresh.lock, so a write
// that never returns stops every jail's refresh on the machine.
func TestUpdateSharedFileGivesUpOnAStaleLockItCannotRemove(t *testing.T) {
	dir := t.TempDir()
	saved := StorageLockWait
	StorageLockWait = 200 * time.Millisecond
	t.Cleanup(func() { StorageLockWait = saved })
	lock := filepath.Join(dir, StorageLockDir)
	if err := os.MkdirAll(filepath.Join(lock, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	wrote, locked := finishesWithin(t, StorageLockWait+2*time.Second, func() (bool, bool, error) {
		return UpdateSharedFile(dir, ViewFile, func([]byte) ([]byte, error) { return []byte("{}"), nil })
	})
	if !wrote || locked {
		t.Errorf("UpdateSharedFile over an unremovable stale lock = wrote %v, locked %v; want written without the lock", wrote, locked)
	}
	if time.Since(start) < StorageLockWait {
		t.Error("the write did not wait for the lock before going ahead without it")
	}
	if _, err := os.Stat(filepath.Join(lock, "x")); err != nil {
		t.Errorf("the write removed what the jail put inside the lock: %v", err)
	}
}

// A stale lock the jail plants again each time the broker breaks it is bounded by the same wait:
// breaking a stale lock retries at once, but never past the deadline.
func TestUpdateSharedFileGivesUpOnAStaleLockPlantedAgain(t *testing.T) {
	dir := t.TempDir()
	saved := StorageLockWait
	StorageLockWait = 200 * time.Millisecond
	t.Cleanup(func() { StorageLockWait = saved })
	lock := filepath.Join(dir, StorageLockDir)
	old := time.Now().Add(-time.Hour)
	plant := func() {
		if err := os.Mkdir(lock, 0o755); err != nil && !os.IsExist(err) {
			t.Error(err)
		}
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Error(err)
		}
	}
	plant()
	onStaleLockBroken = plant
	t.Cleanup(func() { onStaleLockBroken = nil })
	wrote, locked := finishesWithin(t, StorageLockWait+2*time.Second, func() (bool, bool, error) {
		return UpdateSharedFile(dir, ViewFile, func([]byte) ([]byte, error) { return []byte("{}"), nil })
	})
	if !wrote || locked {
		t.Errorf("UpdateSharedFile over a replanted stale lock = wrote %v, locked %v; want written without the lock", wrote, locked)
	}
}
