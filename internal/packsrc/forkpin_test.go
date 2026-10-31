package packsrc

// forkpin_test.go pins THE LAUNCH'S FORK PIN (forkpin.go; docs/design/forked-programs-as-packs.md
// FP-D18) against real git repositories: an unpinned fork is pinned once, a standing pin never moves
// and costs no git run, a pin that cannot be made writes nothing, and concurrent pinners of one fork
// agree on one entry — including the one that waited for the other's lock.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// forkPinFixture is a remote repository, a store and a fork lock path.
func forkPinFixture(t *testing.T) (repo string, store *Store, lockPath string) {
	t.Helper()
	repo = gitRepo(t, map[string]string{"build.sh": "# first\n"})
	return repo, &Store{Dir: t.TempDir(), Getenv: noStagedTree}, filepath.Join(t.TempDir(), ForkLockName)
}

func TestPinForksPinsAnUnpinnedForkOnceAndNeverMovesIt(t *testing.T) {
	repo, store, lockPath := forkPinFixture(t)
	head1 := gitIn(t, repo, "rev-parse", "HEAD")
	want := []ForkWant{{Key: "forkpack/tool", Source: "git+file://" + repo + "?ref=main"}}

	got := store.PinForks(lockPath, want, ForkPinOptions{})
	if got[0].Err != nil || !got[0].Pinned || got[0].Entry.Commit != head1 || got[0].Entry.Ref != "main" {
		t.Fatalf("first pin = %+v, want forkpack/tool pinned at %s", got[0], head1)
	}
	l, err := LoadForkLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := l.Get("forkpack/tool"); !ok || e != got[0].Entry {
		t.Errorf("the fork lock holds %+v (%v), want %+v", e, ok, got[0].Entry)
	}
	// The commit is checked out, so the build act's checkout reads only the pack store.
	a, _ := Parse(want[0].Source)
	if _, err := os.Stat(filepath.Join(store.treeDir(a, head1), treeCompleteMarker)); err != nil {
		t.Errorf("the pinned commit is not checked out: %v", err)
	}

	// THE BRANCH MOVES, AND THE PIN STANDS — with no git run at all, so a broken git proves it.
	commitFile(t, repo, "build.sh", "# second\n")
	before, _ := os.ReadFile(lockPath)
	broken := &Store{Dir: store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	begun := false
	got = broken.PinForks(lockPath, want, ForkPinOptions{Begin: func() (func(string), func()) {
		begun = true
		return nil, func() {}
	}})
	if got[0].Err != nil || got[0].Pinned || got[0].Entry.Commit != head1 || begun {
		t.Errorf("a standing pin = %+v (begun %v), want it left at %s with no git run", got[0], begun, head1)
	}
	if after, _ := os.ReadFile(lockPath); string(after) != string(before) {
		t.Errorf("a standing pin rewrote the fork lock:\n%s", after)
	}
}

// TestPinForksExplicitRepinMovesOnlyAtTheManualUpdateBoundary is the standing-pin counterpart:
// normal launches retain the pin, while a manual request re-resolves the declared branch and records
// the new commit under the fork lock.
func TestPinForksExplicitRepinMovesAtTheManualUpdateBoundary(t *testing.T) {
	repo, store, lockPath := forkPinFixture(t)
	source := "git+file://" + repo + "?ref=main"
	want := []ForkWant{{Key: "forkpack/tool", Source: source}}
	first := store.PinForks(lockPath, want, ForkPinOptions{})[0]
	if first.Err != nil || first.Entry.Commit == "" {
		t.Fatalf("initial pin = %+v", first)
	}
	commitFile(t, repo, "build.sh", "# fresh branch head\n")
	newHead := gitIn(t, repo, "rev-parse", "HEAD")
	ordinary := store.PinForks(lockPath, want, ForkPinOptions{})[0]
	if ordinary.Err != nil || ordinary.Entry.Commit != first.Entry.Commit {
		t.Fatalf("ordinary launch moved the standing pin: %+v; want %s", ordinary, first.Entry.Commit)
	}
	fresh := store.PinForks(lockPath, want, ForkPinOptions{Repin: true})[0]
	if fresh.Err != nil || !fresh.Pinned || fresh.Entry.Commit != newHead {
		t.Fatalf("explicit repin = %+v; want the fresh branch head %s", fresh, newHead)
	}
	lock, err := LoadForkLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := lock.Get(want[0].Key); !ok || entry != fresh.Entry {
		t.Fatalf("fresh pin was not committed to the lock: %+v (present=%v), want %+v", entry, ok, fresh.Entry)
	}
}

// A pin made for another source is no pin: the fork is pinned again, for the source it declares now.
func TestRepinForkPinWaitIsCancellableAndKeepsTheOldEntry(t *testing.T) {
	repo, store, lockPath := forkPinFixture(t)
	source := "git+file://" + repo + "?ref=main"
	want := []ForkWant{{Key: "forkpack/tool", Source: source}}
	first := store.PinForks(lockPath, want, ForkPinOptions{})[0]
	if first.Err != nil || first.Entry.Commit == "" {
		t.Fatalf("first pin = %+v", first)
	}
	commitFile(t, repo, "build.sh", "# fresh\n")

	locked, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- WithForkLock(store.Dir, lockPath, nil, func(*ForkLock) (bool, error) {
			close(locked)
			<-release
			return false, nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("test pinner did not acquire the fork lock")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting, done := make(chan struct{}), make(chan []ForkPinOutcome, 1)
	cancellable := &Store{Dir: store.Dir, Ctx: ctx}
	go func() {
		done <- cancellable.PinForks(lockPath, want, ForkPinOptions{Repin: true,
			Begin: func() (func(string), func()) { return func(string) { close(waiting) }, func() {} },
		})
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("fresh pinner did not begin waiting")
	}
	cancel()
	var outcome []ForkPinOutcome
	select {
	case outcome = <-done:
	case <-time.After(time.Second):
		t.Error("fresh pinner did not stop after cancellation")
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if len(outcome) != 1 || !errors.Is(outcome[0].Err, context.Canceled) {
		t.Fatalf("fresh pin result = %+v, want context.Canceled without a stale pin", outcome)
	}
	lock, err := LoadForkLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Get("forkpack/tool")
	if !ok || entry.Commit != first.Entry.Commit {
		t.Fatalf("cancelled repin changed the admitted pin: %+v (present=%v), original %s", entry, ok, first.Entry.Commit)
	}
}

func TestPinForksRepinsAForkWhoseSourceChanged(t *testing.T) {
	repo, store, lockPath := forkPinFixture(t)
	l := &ForkLock{}
	l.Set(ForkLockEntry{Key: "forkpack/tool", Source: "git+file:///gone?ref=main", Commit: strings.Repeat("a", 40)})
	if err := l.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	source := "git+file://" + repo + "?ref=main"
	got := store.PinForks(lockPath, []ForkWant{{Key: "forkpack/tool", Source: source}}, ForkPinOptions{})
	if got[0].Err != nil || !got[0].Pinned || got[0].Entry.Source != source ||
		got[0].Entry.Commit != gitIn(t, repo, "rev-parse", "HEAD") {
		t.Errorf("a drifted fork = %+v, want it pinned again for %s", got[0], source)
	}
}

// A PIN THAT CANNOT BE MADE WRITES NOTHING, and says why in one line.
func TestPinForksThatCannotResolveWritesNothing(t *testing.T) {
	_, store, lockPath := forkPinFixture(t)
	source := "git+file://" + filepath.Join(t.TempDir(), "absent") + "?ref=main"
	got := store.PinForks(lockPath, []ForkWant{{Key: "forkpack/tool", Source: source}}, ForkPinOptions{})
	if got[0].Err == nil || got[0].Pinned || got[0].Entry.Commit != "" {
		t.Fatalf("an unresolvable fork = %+v, want no pin and an error", got[0])
	}
	if strings.Contains(got[0].Err.Error(), "\n") {
		t.Errorf("the error is not one line: %q", got[0].Err)
	}
	if strings.Contains(got[0].Err.Error(), "yolo pack install") {
		t.Errorf("the error carries a pack's next step, which is not a fork's: %q", got[0].Err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("a failed pin wrote the fork lock (err %v)", err)
	}
}

// A FORK LOCK THAT CANNOT BE READ is pinned over by nothing: every fork carries the read error, and
// no git runs.
func TestPinForksOverAnUnreadableLockRunsNoGit(t *testing.T) {
	_, store, lockPath := forkPinFixture(t)
	if err := os.WriteFile(lockPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := &Store{Dir: store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	got := broken.PinForks(lockPath, []ForkWant{{Key: "forkpack/tool", Source: "git+file:///x?ref=main"}}, ForkPinOptions{})
	if got[0].Err == nil || !strings.Contains(got[0].Err.Error(), lockPath) {
		t.Errorf("over an unreadable lock = %+v, want the read error", got[0])
	}
	if data, _ := os.ReadFile(lockPath); string(data) != "{not json" {
		t.Errorf("an unreadable lock was rewritten: %q", data)
	}
}

// THE LOSER OF A PIN RACE USES THE WINNER'S PIN. A pinner that found the fork unpinned and then
// waited for the fork lock re-reads it under the lock, finds the pin another process made while it
// waited, and takes it — no git run, no second resolution, so one fork never gets two commits. The
// "other process" here holds the lock's flock and writes the pin, so the waiter's re-read is the only
// way it can answer with that commit.
func TestPinForksWaiterUsesThePinMadeWhileItWaited(t *testing.T) {
	_, store, lockPath := forkPinFixture(t)
	source := "git+file:///nonexistent/fork?ref=main" // a git run would fail: the answer must be the lock's
	held, err := flockPath(filepath.Join(store.Dir, "locks", "forklock-"+mirrorSlug(lockPath)+".lock"), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	done := make(chan []ForkPinOutcome)
	go func() {
		done <- store.PinForks(lockPath, []ForkWant{{Key: "forkpack/tool", Source: source}}, ForkPinOptions{
			Begin: func() (func(string), func()) {
				var once sync.Once
				return func(string) { once.Do(func() { close(waiting) }) }, func() {}
			},
		})
	}()
	select {
	case <-waiting:
	case <-time.After(10 * time.Second):
		held()
		t.Fatal("the pinner never waited for the held fork lock")
	}
	commit := strings.Repeat("f", 40)
	l := &ForkLock{}
	l.Set(ForkLockEntry{Key: "forkpack/tool", Source: source, Ref: "main", Commit: commit})
	if err := l.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	held()
	got := <-done
	if got[0].Err != nil || got[0].Pinned || got[0].Entry.Commit != commit {
		t.Errorf("the waiter = %+v, want the pin made while it waited (%s), not one of its own", got[0], commit)
	}
	if mirrors, _ := filepath.Glob(filepath.Join(store.Dir, "mirrors", "*")); len(mirrors) != 0 {
		t.Errorf("the waiter fetched the fork's source (%v)", mirrors)
	}
}

// TWO PINNERS AT ONCE AGREE: every concurrent pin of one fork answers with one commit, exactly one of
// them made it, the fork lock holds one entry, and the repository was cloned once, with no fetch after it.
func TestPinForksConcurrentPinnersAgreeOnOneEntry(t *testing.T) {
	repo, store, lockPath := forkPinFixture(t)
	calls := filepath.Join(t.TempDir(), "calls")
	wrapper := filepath.Join(t.TempDir(), "git-counting")
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in clone|fetch) echo \"$a\" >> '" +
		calls + "';; esac; done\nexec git \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []ForkWant{{Key: "forkpack/tool", Source: "git+file://" + repo + "?ref=main"}}
	const n = 6
	results := make([]ForkPinOutcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := &Store{Dir: store.Dir, Git: wrapper, Getenv: noStagedTree}
			results[i] = s.PinForks(lockPath, want, ForkPinOptions{})[0]
		}(i)
	}
	wg.Wait()
	head := gitIn(t, repo, "rev-parse", "HEAD")
	pinned := 0
	for i, r := range results {
		if r.Err != nil || r.Entry.Commit != head {
			t.Errorf("pinner %d = %+v, want %s", i, r, head)
		}
		if r.Pinned {
			pinned++
		}
	}
	if pinned != 1 {
		t.Errorf("%d pinners say they made the pin, want exactly one", pinned)
	}
	l, err := LoadForkLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Forks) != 1 {
		t.Errorf("the fork lock has %d entries, want one: %+v", len(l.Forks), l.Forks)
	}
	data, _ := os.ReadFile(calls)
	if got := strings.Fields(string(data)); len(got) != 1 || got[0] != "clone" {
		t.Errorf("network calls = %v, want exactly [clone]: a fresh clone needs no fetch", got)
	}
}

func TestFetchForkCommitPreservesLockAndCancellation(t *testing.T) {
	repo, store, _ := forkPinFixture(t)
	a, err := Parse("git+file://" + repo + "?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	commit := gitIn(t, repo, "rev-parse", "HEAD")
	unlock, err := store.lockMirror(a.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.NoWait = true
	if err := store.FetchForkCommit(a, commit, nil); !errors.Is(err, ErrLockHeld) {
		t.Errorf("busy FetchForkCommit error = %v, want errors.Is(ErrLockHeld)", err)
	}
	unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.Ctx, store.NoWait = ctx, false
	if err := store.FetchForkCommit(a, commit, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled FetchForkCommit error = %v, want errors.Is(context.Canceled)", err)
	}
}

// FETCHING A PINNED COMMIT MOVES NOTHING: a commit the store has never held is fetched by its id and
// checked out, and the branch a pin was resolved from may have moved on without moving the pin's
// tree. A commit the store already holds runs no fetch.
func TestFetchForkCommitMakesAPinnedCommitBuildable(t *testing.T) {
	repo, store, _ := forkPinFixture(t)
	head1 := gitIn(t, repo, "rev-parse", "HEAD")
	commitFile(t, repo, "build.sh", "# second\n")
	a, err := Parse("git+file://" + repo + "?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FetchForkCommit(a, head1, nil); err != nil {
		t.Fatalf("FetchForkCommit: %v", err)
	}
	res, err := store.Materialize(a, head1)
	if err != nil {
		t.Fatalf("the fetched commit cannot be materialized: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(res.Root, "build.sh")); string(body) != "# first\n" {
		t.Errorf("the checkout holds %q, want the pinned commit's", body)
	}
	broken := &Store{Dir: store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	if err := broken.FetchForkCommit(a, head1, nil); err != nil {
		t.Errorf("a commit already checked out ran git: %v", err)
	}
	if err := store.FetchForkCommit(a, strings.Repeat("e", 40), nil); err == nil {
		t.Error("a commit the remote does not have was reported fetched")
	} else if strings.Contains(err.Error(), "\n") {
		t.Errorf("the error is not one line: %q", fmt.Sprint(err))
	}
}
