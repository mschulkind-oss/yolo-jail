package config

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
)

// A fetch can take minutes, and every approval on the host waits on a broker store lock, so no
// fetch may run while one is held (brokerlock.Held). The fetch below blocks on a channel; the
// store lock is free the whole time it blocks.
func TestAFetchInProgressNeverHoldsTheBrokerStoreLock(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	started, release := make(chan struct{}), make(chan struct{})
	enableFetchForTest(t, func(PackEntry) error { close(started); <-release; return os.ErrPermission })

	read := make(chan struct{})
	go func() {
		defer close(read)
		BrokeredGrantKindsWithNotes("git")
	}()
	<-started
	locked := make(chan error, 1)
	dir := t.TempDir()
	go func() { locked <- brokerlock.WithTransaction(dir, func() error { return nil }) }()
	if err := <-locked; err != nil {
		t.Fatalf("taking the store lock while a fetch blocks: %v", err)
	}
	if brokerlock.Held() {
		t.Fatal("a lock is still reported held after its transaction ended")
	}
	close(release)
	<-read
}

// The evaluation a store transaction runs (a push's late-coverage recheck, a revalidation) never
// starts a fetch and never waits on one under way, even when an earlier fetch failed or is still
// blocked; it skips the pack, naming why. The same evaluation outside the lock fetches.
func TestNoFetchRunsOrIsAwaitedUnderABrokerStoreLock(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	var mu sync.Mutex
	calls := 0
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	enableFetchForTest(t, func(PackEntry) error {
		mu.Lock()
		calls++
		mu.Unlock()
		once.Do(func() { close(started) })
		<-release
		return os.ErrPermission
	})
	joined := make(chan struct{}, 1)
	missingFetchMu.Lock()
	missingFetchJoined = func(PackEntry) { joined <- struct{}{} }
	missingFetchMu.Unlock()

	// A fetch is under way, blocked, outside any lock.
	outside := make(chan struct{})
	go func() { defer close(outside); BrokeredGrantKindsWithNotes("git") }()
	<-started

	err := brokerlock.WithTransaction(t.TempDir(), func() error {
		kinds, _, notes, err := BrokeredGrantKindsWithNotes("git")
		if err != nil || !slices.Contains(kinds, "branch") || len(notes) != 1 || !strings.Contains(notes[0], "a fetch must never run under it") {
			t.Errorf("under the lock: kinds %v notes %q err %v; want the pack skipped naming the lock", kinds, notes, err)
		}
		if _, notes, _, complete := UserScopeSelectedPacksWithNotes(); !complete || len(notes) != 1 || !strings.Contains(notes[0], "a fetch must never run under it") {
			t.Errorf("approval selection under the lock: complete %t notes %q", complete, notes)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
		t.Fatal("an evaluation under the lock joined the fetch under way and would have waited on it")
	default:
	}
	close(release)
	<-outside
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("the fetch ran %d times; want only the one outside the lock", calls)
	}
}
