package packsrc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// refresh_fix_test.go pins the review round on the launch-time refresh: the timeout really
// ends a hung transport, ssh cannot prompt, fsck covers every network run, resolution stages
// the commit the refresh decided, a launch fetch cannot move any tag, and the typed
// resolution errors a read-only caller classifies by.

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git-wrapper")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// THE COMMAND EVERY STORE RUN EXECUTES carries the prompt hygiene, and a Detached store runs
// it in its own session (no controlling terminal, so ssh cannot prompt; a process group, so
// a timeout can kill the transport helper). Deleting any of these from gitCmd fails here.
func TestGitCmdHygiene(t *testing.T) {
	for _, detached := range []bool{false, true} {
		s := &Store{Dir: t.TempDir(), Env: []string{"GIT_DIR=/leak", "PATH=/bin"}, Detached: detached}
		cmd := s.gitCmd(t.Context(), "", "fetch")
		env := strings.Join(cmd.Env, "\n")
		for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS="} {
			if !strings.Contains(env, want) {
				t.Errorf("detached=%v: env lacks %s: %v", detached, want, cmd.Env)
			}
		}
		if strings.Contains(env, "GIT_DIR=") {
			t.Errorf("detached=%v: inherited git state leaked: %v", detached, cmd.Env)
		}
		if cmd.WaitDelay != gitWaitDelay {
			t.Errorf("detached=%v: WaitDelay = %v, want the %v backstop", detached, cmd.WaitDelay, gitWaitDelay)
		}
		gotSetsid := cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
		if gotSetsid != detached {
			t.Errorf("detached=%v: Setsid = %v", detached, gotSetsid)
		}
		if detached && cmd.Cancel == nil {
			t.Error("a Detached run has no process-group Cancel")
		}
	}
}

// (f) A TIMEOUT ENDS A GIT WHOSE HELPER HOLDS THE OUTPUT PIPE. The fake git leaves a
// background child holding stderr, the shape git-remote-http takes; killing git alone would
// leave the wait blocked on that pipe. Detached, the whole group dies at the deadline.
//
// THE TIMEOUT IS NOT ONLY THE FETCH'S. Every local git run before it (the rev-parses that
// classify the ref) gets the same timeout as its own budget. At 700ms one of them overran under
// a full parallel `go test ./...` on macOS; that read as "no such ref", so nothing was fetched
// and FetchErr was nil. It is now the refresh's own failure, naming the rev-parse
// (TestRefreshReportsARefItCouldNotRead), which this test would report instead of the fetch's
// timeout. 3s leaves the rev-parses room; the fetch still hangs for 20s.
func TestRefreshTimeoutKillsTheTransportHelper(t *testing.T) {
	f := newRefreshFixture(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.store.Git = writeScript(t, "for a in \"$@\"; do [ \"$a\" = fetch ] && { sleep 20 & exec sleep 20; }; done\nexec git \"$@\"\n")
	f.store.Timeout, f.store.Detached = 3*time.Second, true
	f.now = f.now.Add(2 * BranchRefreshInterval)
	start := time.Now()
	o := f.refresh(t, false, pack)[0]
	if took := time.Since(start); took > f.store.Timeout+gitWaitDelay/2 {
		t.Errorf("the refresh took %s: the helper outlived the timeout", took)
	}
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "git fetch timed out") {
		t.Errorf("outcome = %+v, want a labelled fetch timeout", o)
	}
}

// The same shape on a store that is NOT Detached (`yolo pack install`, at a terminal): git
// alone is killed, and WaitDelay bounds the wait on the orphan's pipe. 3s for the reason
// TestRefreshTimeoutKillsTheTransportHelper states.
func TestRefreshTimeoutIsBoundedWithoutDetach(t *testing.T) {
	f := newRefreshFixture(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.store.Git = writeScript(t, "for a in \"$@\"; do [ \"$a\" = fetch ] && { sleep 20 & exec sleep 20; }; done\nexec git \"$@\"\n")
	f.store.Timeout = 3 * time.Second
	f.now = f.now.Add(2 * BranchRefreshInterval)
	start := time.Now()
	o := f.refresh(t, false, pack)[0]
	if took := time.Since(start); took > f.store.Timeout+gitWaitDelay+3*time.Second {
		t.Errorf("the refresh took %s: WaitDelay did not bound the orphan's pipe", took)
	}
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "timed out") {
		t.Errorf("outcome = %+v, want a fetch timeout", o)
	}
}

// A REF THE REFRESH COULD NOT READ IS A FAILURE, NOT A MISSING REF. The fake git fails only
// the rev-parse asking whether `main` is a branch (refs/heads/main), the way a git error (exit
// 128) or an overrun of the store's timeout does; every other run is real git. The pack
// follows a branch, is past its interval and its remote has moved on, so this launch's refresh
// was due: it must say it could not refresh, naming the lookup and git's error, and keep the
// cached commit. Read as "no such ref", the failure made `main` classify as neither tag nor
// branch, so nothing was fetched, FetchErr was nil, and a launch said nothing.
func TestRefreshReportsARefItCouldNotRead(t *testing.T) {
	for _, tc := range []struct {
		name, fail, cause string
		timeout           time.Duration
	}{
		{"git error", `echo "fatal: simulated rev-parse failure" >&2; exit 128`, "simulated rev-parse failure", 0},
		// 3s for the reason TestRefreshTimeoutKillsTheTransportHelper states: every other local
		// git run gets the same timeout as its own budget.
		{"timeout", "exec sleep 30", "git rev-parse timed out", 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			c1 := f.head(t)
			pack := RefreshPack{Name: "p", Source: f.source("main")}
			f.refresh(t, false, pack)
			commitFile(t, f.repo, "two", "2")
			f.store.Git = writeScript(t, `for a in "$@"; do [ "$a" = 'refs/heads/main^{commit}' ] && { `+
				tc.fail+`; }; done
exec git "$@"
`)
			f.store.Timeout, f.store.Detached = tc.timeout, true
			f.now = f.now.Add(2 * BranchRefreshInterval)
			o := f.refresh(t, false, pack)[0]
			if o.FetchErr == nil {
				t.Fatalf("outcome = %+v: the due refresh was skipped and no failure was reported", o)
			}
			for _, want := range []string{"refs/heads/main", tc.cause} {
				if !strings.Contains(o.FetchErr.Error(), want) {
					t.Errorf("FetchErr = %q, want it to name %q", o.FetchErr, want)
				}
			}
			if o.Err != nil || o.Fetched || o.Commit != c1 {
				t.Errorf("outcome = %+v, want the cached %s in use and nothing fetched", o, c1[:8])
			}
			w := o.Warning()
			t.Logf("Warning() = %s", w)
			for _, want := range []string{"pack p:", "could not refresh main", "refs/heads/main", tc.cause,
				"using the cached " + c1[:8]} {
				if !strings.Contains(w, want) {
					t.Errorf("Warning() = %q, want it to contain %q", w, want)
				}
			}
		})
	}
}

// A MIRROR GIT CANNOT READ AT ALL is named by git's own error, in the refresh's outcome and in
// the resolution a launch stops on: never as a ref the remote lacks, nor as one the next launch
// fetches (ErrNotFetched). Real corruption rather than a fake git: a packed-refs line git
// rejects makes every rev-parse in the mirror exit 128, "fatal: unexpected line in
// ./packed-refs" (measured with git 2.55).
func TestRefreshNamesGitsErrorForAMirrorItCannotRead(t *testing.T) {
	f := newRefreshFixture(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	a := mustParse(t, pack.Source)
	f.refresh(t, false, pack)
	mirror := f.store.mirrorPath(a.Repo)
	gitIn(t, mirror, "pack-refs", "--all")
	pr, err := os.OpenFile(filepath.Join(mirror, "packed-refs"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pr.WriteString("not a packed ref line\n"); err != nil {
		t.Fatal(err)
	}
	pr.Close()

	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	const cause = "unexpected line"
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), cause) {
		t.Errorf("FetchErr = %v, want git's %q", o.FetchErr, cause)
	}
	if o.Err == nil || o.Commit != "" {
		t.Fatalf("outcome = %+v, want the pack unusable", o)
	}
	if !strings.Contains(o.Err.Error(), `"main"`) || !strings.Contains(o.Err.Error(), cause) ||
		strings.Contains(o.Err.Error(), "not found") || errors.Is(o.Err, ErrNotFetched) {
		t.Errorf("Err = %v, want the ref and git's %q, not a missing ref", o.Err, cause)
	}
	// The launch's resolution, in the same process or a later one (neither the commit a refresh
	// decided nor its fetch record survives a process), says the same.
	decidedCommits.Delete(f.store.decidedKey(a))
	_, rerr := f.store.Resolve(a, "p")
	if rerr == nil || !strings.Contains(rerr.Error(), cause) || strings.Contains(rerr.Error(), "not found") ||
		errors.Is(rerr, ErrNotFetched) || strings.Contains(rerr.Error(), "\n") {
		t.Errorf("Resolve error = %q, want one line naming git's %q, not a missing ref", rerr, cause)
	}
}

// (f) against a REAL transport: an http remote that accepts the connection and never
// answers. This is the measured hang — git-remote-http held the pipe past the timeout.
func TestRefreshTimeoutEndsAStalledHTTPRemote(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c) // held open, never answered
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
		for _, c := range conns {
			c.Close()
		}
	})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree, Timeout: 2 * time.Second, Detached: true}
	src := "git+http://" + ln.Addr().String() + "/acme/repo?ref=main"
	start := time.Now()
	outs, err := store.Refresh([]RefreshPack{{Name: "p", Source: src}}, RefreshOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > store.Timeout+gitWaitDelay/2 {
		t.Errorf("Refresh took %s against a stalled http remote (timeout %s)", took, store.Timeout)
	}
	if o := outs[0]; o.Err == nil || !strings.Contains(o.Err.Error(), "timed out") {
		t.Errorf("outcome = %+v, want the pack unusable with the timeout named", o)
	}
}

// (f) ONE BUDGET PER FETCH, not per git run: a clone and a fetch that each fit the timeout
// but together exceed it are a timeout.
//
// THE REAL CLONE RUNS INSIDE THE BUDGET TOO, after the injected sleep, so the margin left
// for it is the budget minus one sleep. It was 0.4s (1s budget, 0.6s sleeps), and under a
// full parallel `go test ./...` on macOS the clone alone overran it (and overran 1.2s, at
// 3s and 1.8s): the clone timed out first and the fetch this test is about never ran. 6s
// and 3.6s keep both properties (one sleep fits, two do not) and give the clone 2.4s.
func TestRefreshSharesOneBudgetAcrossCloneAndFetch(t *testing.T) {
	f := newRefreshFixture(t)
	f.store.Git = writeScript(t, "for a in \"$@\"; do case \"$a\" in clone|fetch) sleep 3.6; break;; esac; done\nexec git \"$@\"\n")
	f.store.Timeout = 6 * time.Second
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})[0]
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "git fetch timed out") {
		t.Errorf("outcome = %+v, want the fetch to time out on the budget the clone spent", o)
	}
	if o.Err == nil || !strings.Contains(o.Err.Error(), "timed out") {
		t.Errorf("outcome = %+v, want the checkout, in the same spent budget, unusable too", o)
	}
}

// (k) FSCK ON EVERY RUN THAT RECEIVES OBJECTS: the clone, the fetch, the one-request prefetch
// of the files a checkout needs (prefetchBlobs), and the checkout (whose lazy blob fetch
// inherits `-c`), and the prompt hygiene reaches the child's environment.
//
// The remote allows the blobless filter (uploadpack.allowFilter), as a real host does. Without
// it git ignores --filter over file:// and sends every file with the clone, so the prefetch
// finds nothing missing and never runs, and this test would not see the one run that now
// receives every file a checkout reads.
func TestRefreshChecksObjectsOnEveryNetworkRun(t *testing.T) {
	f := newRefreshFixture(t)
	gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
	log := filepath.Join(t.TempDir(), "argv")
	f.store.Git = writeScript(t, "echo \"$GIT_TERMINAL_PROMPT|$*\" >> '"+log+"'\nexec git \"$@\"\n")
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})[0]
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	data, _ := os.ReadFile(log)
	const prefetch = "prefetch (fetch --stdin)"
	runs := []string{"clone", "fetch", prefetch, "checkout"}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		prompt, argv, _ := strings.Cut(line, "|")
		if prompt != "0" {
			t.Errorf("git ran with GIT_TERMINAL_PROMPT=%q: %s", prompt, argv)
		}
		for _, sub := range runs {
			match := strings.Contains(" "+argv+" ", " "+sub+" ")
			if sub == prefetch {
				match = strings.Contains(" "+argv+" ", " fetch ") && strings.HasSuffix(argv, " --stdin")
			}
			if match {
				seen[sub] = true
				if !strings.Contains(argv, "transfer.fsckObjects=true") {
					t.Errorf("%s ran without fsck: %s", sub, argv)
				}
			}
		}
	}
	for _, sub := range runs {
		if !seen[sub] {
			t.Errorf("no %s ran: %s", sub, data)
		}
	}
}

// A failed fetch's error names the subcommand, not `-c`.
func TestFetchErrorNamesTheSubcommand(t *testing.T) {
	f := newRefreshFixture(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.breakRemote(t)
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "git fetch:") ||
		strings.Contains(o.FetchErr.Error(), "git -c") {
		t.Errorf("FetchErr = %v, want it labelled `git fetch`", o.FetchErr)
	}
	if got := gitLabel([]string{"-c", "a=b", "--work-tree=x", "checkout", "c"}); got != "checkout" {
		t.Errorf("gitLabel = %q", got)
	}
}

// RESOLUTION STAGES THE COMMIT THE REFRESH DECIDED. The launch's refresh fails to fetch and
// warns it is using the cached commit; another process then fetches the shared mirror (done
// here with raw git, as another process would); the launch's Resolve must still stage the
// cached commit it disclosed, not the one the mirror moved to.
func TestResolveStagesTheCommitTheRefreshDecided(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	c2 := commitFile(t, f.repo, "two", "2")

	f.store.Git = writeScript(t, "for a in \"$@\"; do [ \"$a\" = fetch ] && { echo offline >&2; exit 1; }; done\nexec git \"$@\"\n")
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if o.Commit != c1 || o.FetchErr == nil {
		t.Fatalf("fixture: want a failed fetch on the cached %s: %+v", c1, o)
	}
	mirror := f.store.mirrorPath(mustParse(t, pack.Source).Repo)
	gitIn(t, mirror, "fetch", "origin", "+refs/heads/*:refs/heads/*")
	if got := gitIn(t, mirror, "rev-parse", "refs/heads/main"); got != c2 {
		t.Fatalf("fixture: the other process's fetch did not move main: %s", got)
	}
	f.store.Git = ""
	res, err := f.store.Resolve(mustParse(t, pack.Source), "p")
	if err != nil || res.Commit != c1 {
		t.Errorf("Resolve = %+v, %v; want the %s this launch disclosed, not %s", res, err, c1[:8], c2[:8])
	}
}

func mustParse(t *testing.T, src string) Addr {
	t.Helper()
	a, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// (c) A LAUNCH CANNOT MOVE ANY TAG, including one no pack in THIS call is pinned to: a tag
// pack refreshed in a different call (another workspace, a config that dropped it for a
// while) must not find its tag re-pointed by a branch pack's fetch.
func TestRefreshNeverMovesATagOfAPackOutsideTheCall(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	gitIn(t, f.repo, "tag", "v1")
	gitIn(t, f.repo, "tag", "gone")
	tagged := RefreshPack{Name: "b", Source: f.source("v1")}
	branch := RefreshPack{Name: "a", Source: f.source("main")}
	f.refresh(t, false, tagged)
	f.refresh(t, false, branch)

	commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "-f", "v1")
	gitIn(t, f.repo, "tag", "-d", "gone")
	f.now = f.now.Add(2 * BranchRefreshInterval)
	if o := f.refresh(t, false, branch)[0]; !o.Fetched {
		t.Fatalf("fixture: the branch pack did not fetch: %+v", o)
	}
	o := f.refresh(t, false, tagged)[0]
	if o.Commit != c1 || o.Disclosure() != "" {
		t.Errorf("the tag pin moved without an explicit install/update: %+v, %q", o, o.Disclosure())
	}
	mirror := f.store.mirrorPath(mustParse(t, tagged.Source).Repo)
	if got := gitIn(t, mirror, "rev-parse", "refs/tags/gone"); got != c1 {
		t.Errorf("a tag pruned upstream was not restored: %s", got)
	}
	// An explicit act still moves it.
	if o := f.refresh(t, true, tagged)[0]; o.Commit == c1 {
		t.Errorf("Force did not follow the re-pointed tag: %+v", o)
	}
}

// (g) THE UPDATED LINE IS RELATIVE TO THE LOCKFILE. Both tags are already in the mirror, so
// the ref edit v1 → v2 fetches nothing; the move is still said, against the locked commit.
func TestRefreshDisclosesAMoveAgainstTheLockfile(t *testing.T) {
	f := newRefreshFixture(t)
	c1 := f.head(t)
	gitIn(t, f.repo, "tag", "v1")
	c2 := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "v2")
	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("v1")})
	f.breakRemote(t) // proves no fetch: one would fail
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("v2")})[0]
	if o.Fetched || o.FetchErr != nil || o.Commit != c2 {
		t.Fatalf("outcome = %+v, want v2 resolved from the mirror at %s", o, c2)
	}
	if got, want := o.Disclosure(), fmt.Sprintf("Updated pack p: v2 %s → %s", c1[:8], c2[:8]); got != want {
		t.Errorf("Disclosure() = %q, want %q", got, want)
	}
}

// (g) A PACK DELIVERED FOR THE FIRST TIME IS SAID even when nothing was fetched for it: a
// monorepo's second subpath resolves from a mirror the first one already filled.
func TestRefreshDisclosesASecondSubpathOfAMirroredRepo(t *testing.T) {
	repo := gitRepo(t, map[string]string{"sub1/pack.json": `{}`, "sub2/pack.json": `{}`})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	lock := filepath.Join(t.TempDir(), "packs.lock.json")
	now := time.Unix(1_800_000_000, 0)
	src := func(sub string) string { return "git+file://" + repo + "//" + sub + "?ref=main" }
	refresh := func(packs ...RefreshPack) []Outcome {
		outs, err := store.Refresh(packs, RefreshOptions{LockPath: lock, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return outs
	}
	refresh(RefreshPack{Name: "a", Source: src("sub1")})
	outs := refresh(RefreshPack{Name: "a", Source: src("sub1")}, RefreshPack{Name: "b", Source: src("sub2")})
	if outs[1].Fetched || outs[1].Err != nil {
		t.Fatalf("fixture: pack b = %+v, want it resolved from the existing mirror", outs[1])
	}
	if !strings.HasPrefix(outs[1].Disclosure(), "Fetched pack b: main → ") {
		t.Errorf("pack b was delivered for the first time with Disclosure() = %q", outs[1].Disclosure())
	}
	if outs[0].Disclosure() != "" {
		t.Errorf("pack a moved nothing but said %q", outs[0].Disclosure())
	}
}

// THE TYPED RESOLUTION ERRORS. "Not fetched yet" (ErrNotFetched) is what a read-only caller
// reports as the launch's to repair; a ref a SUCCESSFUL fetch did not find is not, in this
// process or — through the stamp — in a later one; an unchecked-out commit is
// ErrNotCheckedOut from ResolveExisting.
func TestResolutionErrorsSayWhatRepairsThem(t *testing.T) {
	f := newRefreshFixture(t)
	a := mustParse(t, f.source("main"))
	if _, err := f.store.Resolve(a, "p"); !errors.Is(err, ErrNotFetched) ||
		!strings.Contains(err.Error(), "the next host launch fetches it") {
		t.Errorf("never fetched: %v, want ErrNotFetched naming the host launch", err)
	}

	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
	// A ref the mirror lacks and no fetch was ever run for — a ref edited into the config
	// since the last launch: the next launch would fetch it.
	if _, err := f.store.Resolve(mustParse(t, f.source("v9")), "q"); !errors.Is(err, ErrNotFetched) {
		t.Errorf("unfetched ref: %v, want ErrNotFetched", err)
	}
	// A typo'd ref after a SUCCESSFUL fetch: not the launch's to repair.
	o := f.refresh(t, false, RefreshPack{Name: "q", Source: f.source("mian")})[0]
	if o.Err == nil || errors.Is(o.Err, ErrNotFetched) || !strings.Contains(o.Err.Error(), "not found on the remote") {
		t.Errorf("typo ref after a good fetch: %v, want a plain 'not found on the remote'", o.Err)
	}
	// A later process reads the same, from the stamp the fetch wrote for that ref.
	fetchFailures.Delete(f.store.failureKey(a.Repo))
	if _, err := f.store.Resolve(mustParse(t, f.source("mian")), "q"); err == nil ||
		errors.Is(err, ErrNotFetched) || !strings.Contains(err.Error(), "not found on the remote") {
		t.Errorf("typo ref in a later process: %v", err)
	}

	// ResolveExisting on a commit the mirror holds but no tree exists for.
	c2 := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "tag", "v2")
	if _, err := f.store.Sync(mustParse(t, f.source("v2"))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResolveExisting(mustParse(t, f.source("v2")), "r"); !errors.Is(err, ErrNotCheckedOut) {
		t.Errorf("unchecked-out %s: %v, want ErrNotCheckedOut", c2[:8], err)
	}
}

// A CLONE THAT SUCCEEDS AHEAD OF A FETCH THAT FAILS keeps the pack usable from what the clone
// brought: that is the cached copy, used with the fetch error warned about like any other.
// The git wrapper passes every run through except `fetch`, so the very first refresh clones
// the mirror and then fails its fetch. Fails if resolveCommit lets a recorded fetch failure
// outrank a ref that resolves locally (the verifier's surviving mutation d2).
func TestRefreshKeepsWhatACloneBroughtWhenItsFetchFails(t *testing.T) {
	f := newRefreshFixture(t)
	want := f.head(t)
	f.store.Git = writeScript(t, `for a in "$@"; do
  if [ "$a" = fetch ]; then echo "fatal: simulated fetch failure" >&2; exit 128; fi
done
exec git "$@"
`)
	o := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})[0]
	if o.Err != nil {
		t.Fatalf("the pack is unusable although its clone succeeded: %+v", o)
	}
	if o.FetchErr == nil || !strings.Contains(o.FetchErr.Error(), "simulated fetch failure") {
		t.Errorf("FetchErr = %v, want the failed fetch reported", o.FetchErr)
	}
	if o.Commit != want {
		t.Errorf("Commit = %q, want the cloned %s", o.Commit, want)
	}
	if w := o.Warning(); !strings.Contains(w, "using the cached "+want[:8]) {
		t.Errorf("Warning() = %q, want it to say the cloned commit is used", w)
	}
	a, _ := Parse(f.source("main"))
	if res, err := f.store.Resolve(a, "p"); err != nil || res.Commit != want {
		t.Errorf("resolution after the failed fetch: %+v, %v", res, err)
	}
}
