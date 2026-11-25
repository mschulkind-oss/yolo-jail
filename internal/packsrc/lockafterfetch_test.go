package packsrc

import (
	"os"
	"path/filepath"
	"testing"
)

// The lockfile records a commit only after the fetch and the checkout produced it, so a launch's
// update check can never leave the lock naming a commit the store lacks: when the fetch fails the
// lock keeps the pin it had (and a first-time pack gets none), and every other broker's reads
// keep resolving what they resolved.
func TestARefreshThatCannotFetchLeavesTheLockAsItWas(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.git")
	source := "git+file://" + missing + "//tools?ref=main"
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	lockPath := filepath.Join(t.TempDir(), "packs.lock.json")

	outcomes, err := store.Refresh([]RefreshPack{{Name: "acme/tools", Source: source}}, RefreshOptions{LockPath: lockPath})
	if err != nil || len(outcomes) != 1 || outcomes[0].Err == nil || outcomes[0].Commit != "" {
		t.Fatalf("Refresh = %+v, %v; want the pack's own failure and no commit", outcomes, err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("a failed first fetch wrote the lock file (%v)", err)
	}

	prior := &Lock{}
	prior.Set(LockEntry{Name: "acme/tools", Source: source, Ref: "main", Commit: "1111111111111111111111111111111111111111"})
	if err := prior.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(lockPath)
	if _, err := store.Refresh([]RefreshPack{{Name: "acme/tools", Source: source}}, RefreshOptions{LockPath: lockPath}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(lockPath); string(after) != string(before) {
		t.Fatalf("a failed refresh rewrote the lock:\nbefore %s\nafter  %s", before, after)
	}
}
