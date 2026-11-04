package brokerstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectContextDoesNotCreateLockOrNormalizeModes(t *testing.T) {
	s, _, input := fixture(t)
	if _, err := s.File(input); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.Dir, ".lock")
	if err := os.Chmod(s.Dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0640); err != nil {
		t.Fatal(err)
	}
	dirBefore, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := s.InspectContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	dirAfter, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	lockAfter, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	if dirAfter.Mode().Perm() != dirBefore.Mode().Perm() || lockAfter.Mode().Perm() != lockBefore.Mode().Perm() {
		t.Fatalf("inspection normalized directory/lock modes: dir %o->%o lock %o->%o",
			dirBefore.Mode().Perm(), dirAfter.Mode().Perm(), lockBefore.Mode().Perm(), lockAfter.Mode().Perm())
	}

	noLock := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(noLock, 0750); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := (&Store{Dir: noLock}).InspectContext(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection without an initialized lock = %v, want unavailable", err)
	}
	if _, err := os.Lstat(filepath.Join(noLock, ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created a missing lock: %v", err)
	}
	fi, err := os.Stat(noLock)
	if err != nil || fi.Mode().Perm() != 0750 {
		t.Fatalf("inspection changed missing-lock directory: mode=%v err=%v", fi.Mode().Perm(), err)
	}
}

func TestInspectContextDoesNotExpireOrRewriteRecords(t *testing.T) {
	s, now, input := fixture(t)
	q, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.Decide(q.ID, "pending", Decision{})
	if err != nil {
		t.Fatal(err)
	}
	*now = grant.Expires.Add(time.Second)
	path := filepath.Join(s.Dir, "grants", grant.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, grants, _, inspectedAt, err := s.InspectContext(context.Background())
	if err != nil || len(grants) != 1 || !grants[0].Ended.IsZero() || !inspectedAt.Equal(*now) {
		t.Fatalf("inspect returned grants=%+v at=%s err=%v", grants, inspectedAt, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only inspect rewrote expired grant: %v", err)
	}
}
