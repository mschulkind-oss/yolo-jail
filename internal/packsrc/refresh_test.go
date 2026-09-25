package packsrc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// refresh_test.go pins the launch-time refresh (refresh.go) against REAL git repositories:
// the ref rule (a–d), failure handling (e, f), the disclosure lines (g), the lockfile record
// (h) and the concurrency guarantee (i). A mocked git would pass while the real invocations
// were wrong, which is the only failure mode that matters for a fetch.

// gitIn runs git in dir with a clean environment and a fixed identity, returning stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(CleanGitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFile writes one file into repo and commits it, returning the new HEAD.
func commitFile(t *testing.T, repo, rel, body string) string {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "c "+rel)
	return gitIn(t, repo, "rev-parse", "HEAD")
}

// refreshFixture is a remote repo with one commit on main, a store, a lockfile path, and a
// controllable clock.
type refreshFixture struct {
	repo  string
	store *Store
	lock  string
	now   time.Time
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	repo := gitRepo(t, map[string]string{"pack.json": `{"name":"p"}`})
	return &refreshFixture{
		repo:  repo,
		store: &Store{Dir: t.TempDir(), Getenv: noStagedTree},
		lock:  filepath.Join(t.TempDir(), "packs.lock.json"),
		now:   time.Unix(1_800_000_000, 0),
	}
}

func (f *refreshFixture) source(ref string) string {
	return "git+file://" + f.repo + "?ref=" + ref
}

// refresh runs one Refresh of the named packs (name -> ref) at the fixture's clock.
func (f *refreshFixture) refresh(t *testing.T, force bool, packs ...RefreshPack) []Outcome {
	t.Helper()
	outs, err := f.store.Refresh(packs, RefreshOptions{
		Force: force, LockPath: f.lock, Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("Refresh: lockfile error: %v", err)
	}
	return outs
}

func (f *refreshFixture) head(t *testing.T) string { return gitIn(t, f.repo, "rev-parse", "HEAD") }

// breakRemote makes the remote unreachable, so any fetch attempt FAILS — which is how the
// tests below prove a refresh did not try: it would have reported the failure.
func (f *refreshFixture) breakRemote(t *testing.T) {
	t.Helper()
	if err := os.Rename(f.repo, f.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(f.repo+".gone", f.repo) })
}

// (a) NEVER FETCHED: fetched and checked out, recorded in the lockfile, and disclosed.
func TestRefreshFetchesANeverFetchedPack(t *testing.T) {
	f := newRefreshFixture(t)
	want := f.head(t)
	outs := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
	o := outs[0]
	if o.Err != nil || o.FetchErr != nil {
		t.Fatalf("outcome: %+v", o)
	}
	if !o.First || !o.Fetched || o.Commit != want {
		t.Errorf("outcome = %+v, want a first fetch resolving to %s", o, want)
	}
	if got, wantLine := o.Disclosure(), "Fetched pack p: main → "+want[:8]; got != wantLine {
		t.Errorf("Disclosure() = %q, want %q", got, wantLine)
	}
	if _, err := os.Stat(filepath.Join(f.store.Dir, "trees", want, treeCompleteMarker)); err != nil {
		t.Errorf("the commit was not checked out: %v", err)
	}
	// Resolve — the launch's offline half — now answers from what the refresh left.
	a, _ := Parse(f.source("main"))
	if res, err := f.store.Resolve(a, "p"); err != nil || res.Commit != want {
		t.Errorf("Resolve after refresh: %+v, %v", res, err)
	}
	l, err := LoadLock(f.lock)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := l.Get("p"); !ok || e != (LockEntry{Name: "p", Source: f.source("main"), Commit: want, Ref: "main"}) {
		t.Errorf("lock entry = %+v (present %v)", e, ok)
	}
}

// (a) again, for a mirror that EXISTS but lacks the ref: a newly-configured tag is fetched even
// though the mirror's other ref was fetched a moment ago.
func TestRefreshFetchesARefTheMirrorLacks(t *testing.T) {
	f := newRefreshFixture(t)
	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
	c2 := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "v2")
	outs := f.refresh(t, false, RefreshPack{Name: "q", Source: f.source("v2")})
	if o := outs[0]; o.Err != nil || !o.Fetched || !o.First || o.Commit != c2 {
		t.Errorf("outcome = %+v, want v2 fetched at %s", o, c2)
	}
}

// (b) A FULL COMMIT SHA THAT RESOLVES IS NEVER FETCHED, however stale: the remote is broken,
// so a fetch attempt would have come back as FetchErr.
func TestRefreshNeverFetchesAPinnedCommit(t *testing.T) {
	f := newRefreshFixture(t)
	sha := f.head(t)
	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source(sha)})
	f.breakRemote(t)
	f.now = f.now.Add(30 * 24 * time.Hour)
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source(sha)})[0]
	if o.Err != nil || o.FetchErr != nil || o.Fetched || o.Commit != sha {
		t.Errorf("a pinned commit was fetched (or failed to resolve): %+v", o)
	}
	if o.Disclosure() != "" || o.Warning() != "" {
		t.Errorf("nothing moved, so nothing is said: %q %q", o.Disclosure(), o.Warning())
	}
}

// (c) A TAG THAT RESOLVES IS NEVER FETCHED: re-pointing it upstream moves nothing at launch,
// and `yolo pack update` (Force) is what moves it — disclosed as an update.
func TestRefreshNeverFetchesATagButForceDoes(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	gitIn(t, f.repo, "tag", "v1")
	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("v1")})

	c2 := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "-f", "v1")
	f.now = f.now.Add(48 * time.Hour)
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("v1")})[0]
	if o.Fetched || o.Commit != c1 {
		t.Fatalf("a launch moved a tag pin: %+v (want %s, unfetched)", o, c1)
	}
	o = f.refresh(t, true, RefreshPack{Name: "p", Source: f.source("v1")})[0]
	if !o.Fetched || o.Commit != c2 {
		t.Fatalf("Force did not move to the re-pointed tag: %+v (want %s)", o, c2)
	}
	if got, want := o.Disclosure(), fmt.Sprintf("Updated pack p: v1 %s → %s", c1[:8], c2[:8]); got != want {
		t.Errorf("Disclosure() = %q, want %q", got, want)
	}
}

// (d) A BRANCH IS FETCHED ONLY WHEN ITS LAST GOOD FETCH IS OLDER THAN THE INTERVAL, and the
// stamp is written only by a successful fetch.
func TestRefreshFetchesABranchHourly(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	c2 := commitFile(t, f.repo, "two", "2")

	f.now = f.now.Add(BranchRefreshInterval - time.Minute)
	if o := f.refresh(t, false, pack)[0]; o.Fetched || o.Commit != c1 || o.Disclosure() != "" {
		t.Fatalf("inside the interval: %+v, want no fetch at %s and nothing said", o, c1)
	}
	f.now = f.now.Add(2 * time.Minute) // now past the interval since the first fetch
	o := f.refresh(t, false, pack)[0]
	if !o.Fetched || o.Commit != c2 {
		t.Fatalf("past the interval: %+v, want a fetch reaching %s", o, c2)
	}
	if got, want := o.Disclosure(), fmt.Sprintf("Updated pack p: main %s → %s", c1[:8], c2[:8]); got != want {
		t.Errorf("Disclosure() = %q, want %q", got, want)
	}

	a, _ := Parse(pack.Source)
	stamp := f.store.stampPath(a)
	before, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("no stamp after a successful fetch: %v", err)
	}
	// A FAILED fetch writes no stamp, so the next launch tries again.
	f.breakRemote(t)
	f.now = f.now.Add(2 * BranchRefreshInterval)
	if o := f.refresh(t, false, pack)[0]; o.FetchErr == nil {
		t.Fatalf("a fetch against a broken remote did not fail: %+v", o)
	}
	if after, _ := os.ReadFile(stamp); string(after) != string(before) {
		t.Errorf("a failed fetch rewrote the stamp: %q -> %q", before, after)
	}
}

// (e) A FAILED FETCH WITH A CACHED COPY IS A WARNING, not a failure: the cached commit is used,
// and the one line names the pack and the error.
func TestRefreshFetchFailureUsesTheCachedCommit(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.breakRemote(t)
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if o.Err != nil || o.FetchErr == nil || o.Commit != c1 {
		t.Fatalf("outcome = %+v, want the cached %s with the fetch error kept", o, c1)
	}
	w := o.Warning()
	if !strings.Contains(w, "pack p") || !strings.Contains(w, "using the cached "+c1[:8]) ||
		strings.Contains(w, "\n") {
		t.Errorf("Warning() = %q, want one line naming the pack, the error and the cached commit", w)
	}
	a, _ := Parse(pack.Source)
	if res, err := f.store.Resolve(a, "p"); err != nil || res.Commit != c1 {
		t.Errorf("resolution after a failed refresh: %+v, %v", res, err)
	}
}

// (e) WITH NO USABLE COPY the pack is unusable, and resolution's error — the one a launch
// dies on, by name — carries the FETCH error rather than advice to go fetch.
func TestRefreshFetchFailureWithNoCopyNamesTheError(t *testing.T) {
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	src := "git+file://" + filepath.Join(t.TempDir(), "no-such-repo") + "?ref=main"
	outs, err := store.Refresh([]RefreshPack{{Name: "p", Source: src}}, RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if o := outs[0]; o.Err == nil || o.Commit != "" || o.Warning() != "" || o.Disclosure() != "" {
		t.Fatalf("outcome = %+v, want unusable with no warning or disclosure", o)
	}
	a, _ := Parse(src)
	_, rerr := store.Resolve(a, "p")
	if rerr == nil || !strings.Contains(rerr.Error(), "never been fetched") ||
		!strings.Contains(rerr.Error(), "fetching it failed") {
		t.Errorf("Resolve error = %v, want it to name the failed fetch", rerr)
	}
	if strings.Contains(rerr.Error(), "\n") {
		t.Errorf("the resolution error spans lines: %q", rerr)
	}
}

// (f) A HUNG REMOTE CANNOT HANG A LAUNCH: the store's per-invocation timeout bounds the fetch,
// and a timeout is a failure like any other — here, the cached copy is used.
func TestRefreshFetchTimeoutIsAFailure(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)

	hang := filepath.Join(t.TempDir(), "git-hangs-on-fetch")
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = fetch ] && exec sleep 30; done\nexec git \"$@\"\n"
	if err := os.WriteFile(hang, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.store.Git, f.store.Timeout = hang, 500*time.Millisecond
	f.now = f.now.Add(2 * BranchRefreshInterval)
	start := time.Now()
	o := f.refresh(t, false, pack)[0]
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the refresh took %s against a hung remote", took)
	}
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "timed out") || o.Commit != c1 {
		t.Errorf("outcome = %+v, want a timed-out fetch and the cached %s", o, c1)
	}
}

// (g) NOTHING MOVED, NOTHING SAID: a fetch that brought nothing new discloses nothing.
func TestRefreshSaysNothingWhenNothingMoved(t *testing.T) {
	f := newRefreshFixture(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if !o.Fetched {
		t.Fatalf("fixture: expected a fetch past the interval: %+v", o)
	}
	if o.Disclosure() != "" || o.Warning() != "" {
		t.Errorf("a fetch that moved nothing said %q / %q", o.Disclosure(), o.Warning())
	}
}

// (h) THE LOCKFILE: entries the refresh did not process are untouched and nothing is pruned,
// and a refresh that changed nothing does not rewrite the file.
func TestRefreshRecordsWithoutPruning(t *testing.T) {
	f := newRefreshFixture(t)
	seed := &Lock{Packs: map[string]LockEntry{
		"departed": {Name: "departed", Source: "git+https://example.invalid/x?ref=main", Commit: strings.Repeat("a", 40), Ref: "main"},
		"local":    {Name: "local", Source: "file:///somewhere"},
	}}
	if err := seed.Save(f.lock); err != nil {
		t.Fatal(err)
	}
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	l, err := LoadLock(f.lock)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range seed.Packs {
		if got, ok := l.Get(name); !ok || got != want {
			t.Errorf("entry %s = %+v (present %v), want it untouched: %+v", name, got, ok, want)
		}
	}
	if _, ok := l.Get("p"); !ok {
		t.Error("the refreshed pack was not recorded")
	}

	old := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(f.lock, old, old); err != nil {
		t.Fatal(err)
	}
	f.refresh(t, false, pack) // inside the interval: nothing to record
	if fi, err := os.Stat(f.lock); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("a refresh that changed nothing rewrote the lockfile (mtime %v)", fi.ModTime())
	}
}

// A FROZEN TAG STAYS FROZEN WHEN ITS MIRROR IS FETCHED FOR ANOTHER PACK. Two packs on one repo:
// the branch pack's stale stamp forces a fetch, which force-updates tags — so without the
// restore, the tag pack would move, and Resolve (reading the mirror's refs) would stage it.
func TestRefreshKeepsATagFrozenWhenAPackBesideItIsFetched(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	gitIn(t, f.repo, "tag", "v1")
	tagged := RefreshPack{Name: "tagged", Source: f.source("v1")}
	branch := RefreshPack{Name: "branch", Source: f.source("main")}
	f.refresh(t, false, tagged, branch)

	c2 := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "-f", "v1")
	f.now = f.now.Add(2 * BranchRefreshInterval)
	outs := f.refresh(t, false, tagged, branch)
	if outs[1].Commit != c2 {
		t.Fatalf("the branch pack did not move: %+v", outs[1])
	}
	if outs[0].Commit != c1 {
		t.Errorf("the tag pack moved with its neighbour's fetch: %+v (want %s)", outs[0], c1)
	}
	a, _ := Parse(tagged.Source)
	if res, err := f.store.Resolve(a, "tagged"); err != nil || res.Commit != c1 {
		t.Errorf("Resolve of the frozen tag after the fetch = %+v, %v; want %s", res, err, c1)
	}
}

// Packs sharing a mirror are disclosed individually on a first fetch: the second pack's ref
// resolving after the first pack's clone must not hide that this launch fetched it too.
func TestRefreshDisclosesEveryPackOfAFreshMirror(t *testing.T) {
	f := newRefreshFixture(t)
	outs := f.refresh(t, false,
		RefreshPack{Name: "one", Source: f.source("main")},
		RefreshPack{Name: "two", Source: f.source("main")})
	for _, o := range outs {
		if !strings.HasPrefix(o.Disclosure(), "Fetched pack "+o.Name+":") {
			t.Errorf("%s: Disclosure() = %q", o.Name, o.Disclosure())
		}
	}
}

// (i) CONCURRENCY. Eight refreshes of one never-fetched mirror at once, each recording a
// different pack name: exactly one clone and one fetch reach the network (every waiter
// re-checks the stamp the first one wrote), and the lockfile keeps all eight entries.
//
// flock is per open file description, so goroutines opening the lock file separately contend
// exactly as separate processes do.
func TestRefreshConcurrentLaunchesFetchOnceAndLoseNothing(t *testing.T) {
	f := newRefreshFixture(t)
	calls := filepath.Join(t.TempDir(), "calls")
	wrapper := filepath.Join(t.TempDir(), "git-counting")
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in clone|fetch) echo \"$a\" >> '" +
		calls + "';; esac; done\nexec git \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := &Store{Dir: f.store.Dir, Git: wrapper, Getenv: noStagedTree}
			outs, err := store.Refresh([]RefreshPack{{Name: fmt.Sprintf("p%d", i), Source: f.source("main")}},
				RefreshOptions{LockPath: f.lock, Now: func() time.Time { return f.now }})
			if err != nil {
				errs <- err.Error()
				return
			}
			if outs[0].Err != nil || outs[0].Commit == "" {
				errs <- fmt.Sprintf("p%d: %+v", i, outs[0])
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	data, _ := os.ReadFile(calls)
	if got := strings.Fields(string(data)); len(got) != 2 || got[0] != "clone" || got[1] != "fetch" {
		t.Errorf("network calls = %v, want exactly [clone fetch]", got)
	}
	l, err := LoadLock(f.lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Packs) != n {
		t.Errorf("lockfile has %d entries, want %d: %+v", len(l.Packs), n, l.Packs)
	}
}

// A local address handed to Refresh touches nothing: no mirror, no lock, no network.
func TestRefreshIgnoresALocalPack(t *testing.T) {
	store := &Store{Dir: t.TempDir(), Git: "/nonexistent/git", Getenv: noStagedTree}
	outs, err := store.Refresh([]RefreshPack{{Name: "l", Source: "file://" + t.TempDir()}}, RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].Err == nil || outs[0].Commit != "" {
		t.Errorf("outcome = %+v", outs[0])
	}
	if entries, _ := os.ReadDir(store.Dir); len(entries) != 0 {
		t.Errorf("a local pack wrote into the store: %v", entries)
	}
}

// (i), the lockfile half on its own: concurrent load-modify-save cycles lose no entry. Each
// writer holds its loaded copy for a moment before saving, which is the window a lost update
// needs — so without WithLock's flock this test drops entries.
func TestWithLockSerialisesLockfileWriters(t *testing.T) {
	storeDir := t.TempDir()
	lockPath := filepath.Join(t.TempDir(), "packs.lock.json")
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := WithLock(storeDir, lockPath, nil, func(l *Lock) (bool, error) {
				time.Sleep(5 * time.Millisecond)
				l.Set(LockEntry{Name: fmt.Sprintf("p%d", i), Source: "s"})
				return true, nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	l, err := LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Packs) != n {
		t.Errorf("lockfile has %d entries after %d concurrent writers, want all of them", len(l.Packs), n)
	}
}
