package packsrc

// nowait_test.go pins a NoWait store (store.go, docs/design/pi-extension-store-builds.md §6.2 rule
// 5, XB-D19): the background advance's mirror and record locks are a try, never a wait, and a check
// that meets a held mirror lock leaves its record as it was.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A HELD LOCK FAILS AT ONCE WITH ErrLockHeld under NoWait, for the mirror's lock and the record's,
// and the same store with NoWait unset takes each once it is free. Red if either lock stops reading
// NoWait.
func TestANoWaitStoreReturnsErrLockHeld(t *testing.T) {
	s := &Store{Dir: t.TempDir(), NoWait: true}
	const repo, owner = "https://example.invalid/r.git", "pack/ext"
	for _, c := range []struct {
		name string
		path string
		take func() (func(), error)
	}{
		{"mirror", filepath.Join(s.Dir, "locks", mirrorSlug(repo)+".lock"), func() (func(), error) { return s.lockMirror(repo, nil) }},
		{"record", filepath.Join(s.Dir, "locks", "check-"+mirrorSlug(owner)+".lock"), func() (func(), error) {
			return s.lockCheckRecord(owner, nil)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hold, err := flockPath(c.path, "the lock", nil)
			if err != nil {
				t.Fatal(err)
			}
			type taken struct {
				unlock func()
				err    error
			}
			done := make(chan taken, 1)
			go func() { u, err := c.take(); done <- taken{u, err} }()
			select {
			case got := <-done:
				if !errors.Is(got.err, ErrLockHeld) || got.unlock != nil {
					t.Fatalf("a held %s lock under NoWait = %v, want ErrLockHeld", c.name, got.err)
				}
			case <-time.After(5 * time.Second):
				hold() // let the waiter through, so the test ends
				t.Fatalf("a held %s lock under NoWait was waited for", c.name)
			}
			hold()
			unlock, err := c.take()
			if err != nil {
				t.Fatalf("a free %s lock under NoWait: %v", c.name, err)
			}
			unlock()
		})
	}
}

// A NoWait CHECK THAT MEETS A HELD MIRROR LOCK fetches nothing and leaves the record byte for byte
// as it was — the attempt's stamp put back — and says the lock was held. Red if the check records
// the held lock as its problem, or keeps the attempt's stamp.
func TestANoWaitCheckThatMeetsAHeldMirrorLeavesTheRecord(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	recPath := u.store.CheckRecordPath(w.Owner)
	before, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	_, addr, _, err := w.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	hold, err := flockPath(filepath.Join(u.store.Dir, "locks", mirrorSlug(addr.Repo)+".lock"), "the mirror", nil)
	if err != nil {
		t.Fatal(err)
	}
	bg := *u.store
	bg.NoWait = true
	u.now = u.now.Add(2 * time.Hour)
	done := make(chan CheckResult, 1)
	go func() { done <- bg.CheckPatched(w, CheckOptions{Now: func() time.Time { return u.now }}) }()
	var res CheckResult
	select {
	case res = <-done:
		hold()
	case <-time.After(5 * time.Second):
		hold()
		<-done
		t.Fatal("the NoWait check waited for the held mirror lock")
	}
	if !errors.Is(res.Err, ErrLockHeld) || res.Ran {
		t.Fatalf("the check = %+v, want ErrLockHeld and nothing run", res)
	}
	after, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the record changed:\nbefore %s\nafter  %s", before, after)
	}
}

// A SIBLING IN THE SAME PROCESS is still a held lock: NoWait must not introduce a process-mutex
// wait, which would also ignore cancellation. The pending sibling retries at the next launch.
func TestANoWaitTrySkipsItsOwnProcessesSibling(t *testing.T) {
	s := &Store{Dir: t.TempDir(), NoWait: true}
	const repo = "https://example.invalid/r.git"
	first, err := s.lockMirror(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	done := make(chan error, 1)
	go func() {
		unlock, err := s.lockMirror(repo, nil)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLockHeld) {
			t.Errorf("the sibling's try = %v, want ErrLockHeld", err)
		}
	case <-time.After(time.Second):
		t.Fatal("NoWait waited for its own process's sibling")
	}
}
