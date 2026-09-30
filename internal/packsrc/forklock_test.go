package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fork lock round-trips, sorted and whole, beside the user config.
func TestForkLockRoundTrip(t *testing.T) {
	path := ForkLockPath(filepath.Join(t.TempDir(), "config.jsonc"))
	if filepath.Base(path) != "forks.lock.json" {
		t.Fatalf("fork lock path = %s", path)
	}
	l, err := LoadForkLock(path)
	if err != nil || len(l.Forks) != 0 {
		t.Fatalf("a missing lock must load empty: %v %+v", err, l)
	}
	e := ForkLockEntry{Key: "pi-matt/pi", Source: "git+https://h/pi-fork?ref=main", Ref: "main",
		Commit: strings.Repeat("a", 40)}
	l.Set(e)
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadForkLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := back.Get("pi-matt/pi"); !ok || got != e {
		t.Errorf("round trip: got %+v (%v), want %+v", got, ok, e)
	}
	if back.Schema != ForkLockSchema {
		t.Errorf("schema = %d", back.Schema)
	}
	if removed := back.Prune(nil); len(removed) != 1 || removed[0] != "pi-matt/pi" {
		t.Errorf("Prune removed %v", removed)
	}
}

// A lock written by a newer yolo is refused rather than misread, as the pack lock is.
func TestForkLockRefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), ForkLockName)
	if err := os.WriteFile(path, []byte(`{"schema": 99, "forks": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadForkLock(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v, want a newer-schema refusal", err)
	}
}

// WithForkLock writes only when fn changed something, so a pin that moved nothing leaves the
// file (and its mtime) alone.
func TestWithForkLockWritesOnlyAChange(t *testing.T) {
	store := t.TempDir()
	path := filepath.Join(t.TempDir(), ForkLockName)
	if err := WithForkLock(store, path, nil, func(*ForkLock) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("an unchanged fork lock was written: %v", err)
	}
	err := WithForkLock(store, path, nil, func(l *ForkLock) (bool, error) {
		l.Set(ForkLockEntry{Key: "a/b", Source: "git+https://h/r?ref=v1", Commit: "c"})
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := LoadForkLock(path)
	if err != nil || len(l.Forks) != 1 {
		t.Errorf("the changed lock was not written: %v %+v", err, l)
	}
}
