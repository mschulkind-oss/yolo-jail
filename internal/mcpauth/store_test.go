package mcpauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestCanonicalStatusReadDoesNotCreateOrReplaceLock(t *testing.T) {
	store, err := NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(store.Dir, store.LockName)
	if _, err := store.ReadStatus(); err == nil {
		t.Fatal("status returned an unlocked snapshot")
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("status created lock: %v", err)
	}
	target := filepath.Join(t.TempDir(), "other.lock")
	if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadStatus(); err == nil {
		t.Fatal("status accepted linked lock")
	}
	info, err := os.Lstat(lock)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("status changed linked lock")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "sentinel" {
		t.Fatal("status changed linked target")
	}
}

func TestCanonicalStatusReadDoesNotRepairUnsafeMode(t *testing.T) {
	store, err := NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithLock(func(*CanonicalFile) error { return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, store.Filename)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadStatus(); err == nil {
		t.Fatal("status accepted unsafe canonical mode")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("status repaired file mode: %04o", info.Mode().Perm())
	}
}

func TestCanonicalStoreModeAndWriterSerialization(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCanonicalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Wait = time.Second
	held := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.WithLock(func(file *CanonicalFile) error {
			close(held)
			<-release
			file.Records = []Record{canonicalRecord(1, "live", `{"access_token":"token"}`)}
			return nil
		})
	}()
	<-held
	secondDone := make(chan error, 1)
	go func() { secondDone <- store.WithLock(func(*CanonicalFile) error { return nil }) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second writer did not block: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("canonical file mode = %04o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.lock")); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalStoreWaitTimeoutWritesNothing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCanonicalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Wait = 30 * time.Millisecond
	root, err := paths.OpenStateDirRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := paths.OpenLockFileBeneath(root, "codex.lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := syscallFlock(f); err != nil {
		t.Fatal(err)
	}
	defer func() { unlockSyscallFlock(f); f.Close(); root.Close() }()
	err = store.WithLock(func(file *CanonicalFile) error {
		file.Records = []Record{canonicalRecord(1, "live", `{"access_token":"token"}`)}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("lock timeout error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.json")); !os.IsNotExist(err) {
		t.Fatalf("timeout wrote canonical file: %v", err)
	}
}

func TestReplicaWriteReplacesSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{EmptyStore: "{}", Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "agent.lock"}}
	store := Store{Entries: map[string]json.RawMessage{"x": json.RawMessage(`{"access_token":"a"}`)}}
	if err := os.Symlink(target, filepath.Join(dir, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SeedFile(root, ".credentials.json", time.Second, false, func(current Store) (Store, error) {
		return store, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("symlink target changed: %q err=%v", got, err)
	}
	link, err := os.Lstat(filepath.Join(dir, ".credentials.json"))
	if err != nil || !link.Mode().IsRegular() {
		t.Fatalf("replica path not replaced by regular file: %v %v", link, err)
	}
}

func TestReplicaSeedReplacesInRootLinkWithoutReadingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	const sentinel = `{"access_token":"target sentinel"}`
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.json", filepath.Join(dir, "replica.json")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{EmptyStore: "{}", Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "agent.lock"}}
	if err := adapter.SeedFile(root, "replica.json", time.Second, false, func(current Store) (Store, error) {
		if len(current.Root) != 0 || len(current.Entries) != 0 {
			t.Fatalf("seed read the in-root link target: %+v", current)
		}
		return Store{Entries: map[string]json.RawMessage{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != sentinel {
		t.Fatalf("seed changed target: %q, %v", got, err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "replica.json")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("seed did not replace linked leaf: %v, %v", fi, err)
	}
}

func TestReplicaAdoptRefusesPreexistingLinkedLeafWithoutSignout(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "store.json")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "agent.lock"}}
	called := false
	err = adapter.UpdateFile(root, "store.json", time.Second, false, func(current Store) (Store, error) {
		called = true
		_, _, events := PlanAdopt([]Record{canonicalRecord(1, "live", `{"access_token":"canonical"}`)}, current, true, []SharedServer{shared("linear")}, dir)
		if len(events) != 0 {
			t.Fatal("linked leaf was treated as clean sign-out")
		}
		return current, nil
	})
	if err == nil || called {
		t.Fatalf("linked leaf adoption err=%v plannerCalled=%v", err, called)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "untouched" {
		t.Fatalf("linked target changed: %q %v", data, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "store.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("linked leaf changed: %v %v", info, err)
	}
}

func TestReplicaSeedRefusesLinkedParentAndLockDirectoryWithoutWrites(t *testing.T) {
	for _, linkedPart := range []string{"replica-parent", "lock-directory"} {
		t.Run(linkedPart, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			parent, lockDir := filepath.Join(dir, "replica"), filepath.Join(dir, "locks")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			link := parent
			if linkedPart == "lock-directory" {
				link = lockDir
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			adapter := Adapter{Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "locks/agent.lock"}}
			called := false
			err = adapter.SeedFile(root, "replica/store.json", time.Second, false, func(store Store) (Store, error) {
				called = true
				return store, nil
			})
			if err == nil || called {
				t.Fatalf("linked %s seed err=%v called=%v", linkedPart, err, called)
			}
			if files, err := os.ReadDir(outside); err != nil || len(files) != 0 {
				t.Fatalf("outside modified: %v %v", files, err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "replica", "store.json")); !os.IsNotExist(err) {
				t.Fatalf("replica written: %v", err)
			}
		})
	}
}

func TestReplicaLockRefusesLinkedLockDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "locks")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "locks/file.lock"}}
	if _, err := adapter.lockReplica(root, 50*time.Millisecond); err == nil {
		t.Fatal("linked lock directory was accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("refusal wrote outside lock directory: %#v err=%v", entries, err)
	}
}

func syscallFlock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func unlockSyscallFlock(file *os.File) error { return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }

func TestMissingCanonicalFileAndPerRecordRefreshLocks(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCanonicalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Wait = 25 * time.Millisecond
	file, err := store.Read()
	if err != nil || file.Version != 1 || len(file.Records) != 0 {
		t.Fatalf("missing canonical read = %+v err=%v", file, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.json")); !os.IsNotExist(err) {
		t.Fatalf("read-only missing-store lookup wrote data: %v", err)
	}
	unlock, err := store.LockRefresh("linear", testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(dir, "codex."+testIdentity+".refresh.lock")); err != nil {
		t.Fatalf("documented per-record lock missing: %v", err)
	}
	if _, err := store.LockRefresh("linear", testIdentity); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("same-record refresh lock did not block: %v", err)
	}
	otherUnlock, err := store.LockRefresh("sentry", strings.Repeat("b", 64))
	if err != nil {
		t.Fatalf("different record refresh lock was not independent: %v", err)
	}
	if err := otherUnlock(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalStoreRejectsInvalidWholeFileWithoutCorruptingPrevious(t *testing.T) {
	store, err := NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := canonicalRecord(1, "live", `{"access_token":"safe"}`)
	if err := store.WithLock(func(file *CanonicalFile) error { file.Records = []Record{original}; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []func(*CanonicalFile){
		func(file *CanonicalFile) { file.Version = 2 },
		func(file *CanonicalFile) { file.Records = append(file.Records, original) },
	} {
		if err := store.WithLock(func(file *CanonicalFile) error { invalid(file); return nil }); err == nil {
			t.Fatal("invalid canonical file persisted")
		}
		file, err := store.Read()
		if err != nil || file.Version != 1 || len(file.Records) != 1 || !sameJSON(file.Records[0].Entry, original.Entry) {
			t.Fatalf("previous file unreadable: %+v %v", file, err)
		}
	}
}

func TestReplicaUpdateLocksReadPlanWriteAndTimeoutWritesNothing(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{EmptyStore: "{}", Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "agent.lock"}}
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"unshared":{"z":1,"a":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := adapter.lockReplica(root, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	plan := func(store Store) (Store, error) {
		called = true
		store.Entries["shared"] = json.RawMessage(`{"access_token":"new"}`)
		return store, nil
	}
	if err := adapter.UpdateFile(root, "store.json", 25*time.Millisecond, false, plan); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout = %v", err)
	}
	if err := adapter.SeedFile(root, "store.json", 25*time.Millisecond, false, plan); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("seed timeout = %v", err)
	}
	if called {
		t.Fatal("plan ran without lock")
	}
	data, err := os.ReadFile(filepath.Join(dir, "store.json"))
	if err != nil || string(data) != `{"unshared":{"z":1,"a":2}}` {
		t.Fatalf("timeout wrote: %s %v", data, err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UpdateFile(root, "store.json", time.Second, false, plan); err != nil || !called {
		t.Fatalf("transaction failed: %v called=%v", err, called)
	}
	data, err = os.ReadFile(filepath.Join(dir, "store.json"))
	if err != nil || !strings.Contains(string(data), `"unshared":{"z":1,"a":2}`) || !strings.Contains(string(data), `"shared":{"access_token":"new"}`) {
		t.Fatalf("transaction lost entries: %s %v", data, err)
	}
	none := Adapter{Lock: packdecl.MCPAuthLock{Protocol: "none"}}
	if err := none.UpdateFile(root, "store.json", time.Second, false, plan); err == nil {
		t.Fatal("none accepted without launch boundary")
	}
	if err := none.SeedFile(root, "store.json", time.Second, false, plan); err == nil {
		t.Fatal("seed accepted none without launch boundary")
	}
}

func TestReplicaUpdateReadsAfterFlockAndPreservesInterveningAgentEdit(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	adapter := Adapter{Lock: packdecl.MCPAuthLock{Protocol: "flock", Path: "agent.lock"}}
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"unshared":{"version":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := adapter.lockReplica(root, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		finished <- adapter.UpdateFile(root, "store.json", time.Second, false, func(store Store) (Store, error) {
			store.Entries["shared"] = json.RawMessage(`{"access_token":"copy"}`)
			return store, nil
		})
	}()
	<-started
	select {
	case err := <-finished:
		t.Fatalf("transaction did not wait on agent lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	// An agent writing under this lock must be seen by the broker's later read.
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"unshared":{"version":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "store.json"))
	if err != nil || !strings.Contains(string(data), `"unshared":{"version":2}`) || !strings.Contains(string(data), `"shared":{"access_token":"copy"}`) {
		t.Fatalf("stale read overwrote agent edit: %s %v", data, err)
	}
}
