package packsrc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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

// ONLY A RUN THAT RECEIVES OBJECTS MAY FETCH ONE ON DEMAND, and those are exactly the runs given
// fsckArgs: every other run's environment ends GIT_NO_LAZY_FETCH=1, and a receiving run's ends
// GIT_NO_LAZY_FETCH=0, so the checkout's lazy fetch (the prefetch's fallback) survives a caller
// environment that turned it off. os/exec keeps the last of duplicate keys, so the last one is
// the one git sees.
func TestGitCmdAllowsTheLazyFetchOnlyToARunThatReceivesObjects(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Env: []string{"GIT_NO_LAZY_FETCH=1", "PATH=/bin"}}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--verify", "--quiet", "main^{commit}"}, "1"},
		{[]string{"ls-tree", "-z", "c", "--", "."}, "1"},
		{[]string{"update-ref", "--stdin"}, "1"},
		{withFsck("--work-tree=/t", "checkout", "--force", "c", "--", "."), "0"},
		{withFsck("clone", "--bare", "--filter=blob:none", "u", "m"), "0"},
	} {
		got := ""
		for _, kv := range s.gitCmd(t.Context(), "", tc.args...).Env {
			if v, ok := strings.CutPrefix(kv, "GIT_NO_LAZY_FETCH="); ok {
				got = v
			}
		}
		if got != tc.want {
			t.Errorf("git %v: GIT_NO_LAZY_FETCH=%q, want %q", tc.args, got, tc.want)
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

// requireNoLazyFetchSwitch skips a test whose assertion is that a lookup in the partial mirror
// fetches nothing, when the git running it is older than 2.45, the release that added
// GIT_NO_LAZY_FETCH (with `git --no-lazy-fetch`): a git without the variable ignores it, and
// still fetches a commit it lacks on demand.
func requireNoLazyFetchSwitch(t *testing.T) {
	t.Helper()
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(out), "git version %d.%d", &major, &minor); err != nil {
		t.Fatalf("unparseable `git version`: %q", out)
	}
	if major < 2 || major == 2 && minor < 45 {
		t.Skipf("%s predates GIT_NO_LAZY_FETCH (git 2.45)", strings.TrimSpace(string(out)))
	}
}

// mirrorHolds reports whether the mirror holds object oid itself. It asks without the lazy
// fetch, since a plain `git cat-file -e` in a partial mirror fetches a missing object from the
// remote in order to answer (measured with git 2.55).
func mirrorHolds(t *testing.T, mirror, oid string) bool {
	t.Helper()
	cmd := exec.Command("git", "cat-file", "-e", oid)
	cmd.Dir = mirror
	cmd.Env = append(CleanGitEnv(os.Environ()), "GIT_NO_LAZY_FETCH=1")
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return err == nil
}

// malformedCommit writes into repo, reachable from no ref, a commit fsck rejects (an author line
// with no date: badDate) whose parent is HEAD and whose tree is HEAD's, and returns its id.
func malformedCommit(t *testing.T, repo string) string {
	t.Helper()
	body := fmt.Sprintf("tree %s\nparent %s\nauthor t <t@e> notadate +0000\ncommitter t <t@e> 1 +0000\n\nbad\n",
		gitIn(t, repo, "rev-parse", "HEAD^{tree}"), gitIn(t, repo, "rev-parse", "HEAD"))
	p := filepath.Join(t.TempDir(), "commit")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return gitIn(t, repo, "hash-object", "-t", "commit", "-w", "--literally", p)
}

// A LOOKUP IN THE MIRROR FETCHES NOTHING. The mirror lacks a commit its remote has (main moved
// on after the mirror was fetched). Asked about that commit by its full id, `git rev-parse` in
// the partial mirror used to fetch it from the remote by itself and exit 0 (measured with git
// 2.55): network traffic inside a lookup documented as offline, objects received without the
// fsck the store's own fetches run with, and, from a remote that ignores the blobless filter as
// this file:// one does, git's "warning: filtering not recognized by server, ignoring" on
// stderr, which revParse returned in front of the commit id. The commit must read as absent,
// from the refresh's lookup and from the reader that must write nothing (ResolveExisting), and
// the mirror must still lack it afterwards.
func TestALookupOfACommitTheMirrorLacksFetchesNothing(t *testing.T) {
	requireNoLazyFetchSwitch(t)
	f := newRefreshFixture(t)
	f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
	c2 := commitFile(t, f.repo, "two", "2")
	a := mustParse(t, f.source(c2))
	mirror := f.store.mirrorPath(a.Repo)

	if sha, err := f.store.revParse(mirror, c2); sha != "" || err != nil {
		t.Errorf("revParse(%s) = %q, %v; want absent", c2[:8], sha, err)
	}
	if kind, name, local, err := f.store.classifyRef(mirror, c2); kind != refUnresolved || name != "" ||
		local != "" || err != nil {
		t.Errorf("classifyRef(%s) = %v, %q, %q, %v; want unresolved", c2[:8], kind, name, local, err)
	}
	if res, err := f.store.ResolveExisting(a, "q"); !errors.Is(err, ErrNotFetched) {
		t.Errorf("ResolveExisting = %+v, %v; want ErrNotFetched", res, err)
	}
	if mirrorHolds(t, mirror, c2) {
		t.Errorf("a lookup fetched %s into the mirror", c2[:8])
	}
}

// THE COMMIT ID COMES FROM GIT'S STDOUT ALONE. The fake git prints a warning on stderr before
// every real git run, the way git does when a lookup fetches from a remote that ignores the
// filter (as a git without GIT_NO_LAZY_FETCH still does). Read with stderr, the
// warning led the id revParse returned, so the repository-root pack's checkout failed on
// "invalid reference: warning: …", and the subdirectory pack's ls-tree on "Not a valid object
// name warning: …".
func TestRevParseReadsTheCommitFromStdoutAlone(t *testing.T) {
	repo := gitRepo(t, map[string]string{"pack.json": `{}`, "sub/pack.json": `{}`})
	head := gitIn(t, repo, "rev-parse", "HEAD")
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree,
		Git: writeScript(t, "echo 'warning: a message git printed' >&2\nexec git \"$@\"\n")}
	for _, src := range []string{"git+file://" + repo + "?ref=main", "git+file://" + repo + "//sub?ref=main"} {
		o := refreshOne(t, store, src, time.Unix(1_800_000_000, 0))
		if o.Err != nil || o.Commit != head {
			t.Errorf("%s: outcome = %+v, want %s", src, o, head[:8])
		}
	}
	mirror := store.mirrorPath(mustParse(t, "git+file://"+repo+"?ref=main").Repo)
	if sha, err := store.revParse(mirror, "main"); sha != head || err != nil {
		t.Errorf("revParse(main) = %q, %v; want exactly %s", sha, err, head)
	}
}

// A PINNED COMMIT THE MIRROR LACKS IS FETCHED BY THE REFRESH'S OWN FETCH. The mirror exists and
// its remote's main has moved on; a pack pinned to the new commit must read as not in the store,
// so the refresh fetches (Fetched), and resolve to that commit. When the lookup fetched the
// commit itself, the refresh saw a pinned commit it held and fetched nothing: from a remote that
// honors the filter the pack resolved with Fetched false, and from one that ignores it the
// warning git printed became the commit and the checkout failed.
func TestRefreshFetchesAPinnedCommitTheMirrorLacks(t *testing.T) {
	requireNoLazyFetchSwitch(t)
	for _, tc := range []struct {
		name        string
		allowFilter bool
	}{{"a remote that ignores the filter", false}, {"a remote that honors it", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			if tc.allowFilter {
				gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
			}
			f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
			c2 := commitFile(t, f.repo, "two", "2")
			o := f.refresh(t, false, RefreshPack{Name: "q", Source: f.source(c2)})[0]
			if o.Err != nil || o.FetchErr != nil || !o.Fetched || o.Commit != c2 {
				t.Errorf("outcome = %+v, want %s fetched by the refresh", o, c2[:8])
			}
			if got, want := o.Disclosure(), "Fetched pack q: "+c2+" → "+c2[:8]; got != want {
				t.Errorf("Disclosure() = %q, want %q", got, want)
			}
		})
	}
}

// A PACK MAY PIN A COMMIT ON NO BRANCH OR TAG of its remote (a deleted branch's, a pull
// request's). The fetch of the branches and tags does not bring it, so the refresh asks the
// remote for the commit by its id, in a checked fetch of its own. That is what the lookup's own
// fetch used to deliver, unchecked.
func TestRefreshFetchesAPinnedCommitOnNoBranchOrTag(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("mirror exists %v", existing), func(t *testing.T) {
			f := newRefreshFixture(t)
			gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
			gitIn(t, f.repo, "checkout", "-q", "-b", "feature")
			c2 := commitFile(t, f.repo, "two", "2")
			gitIn(t, f.repo, "checkout", "-q", "main")
			gitIn(t, f.repo, "branch", "-q", "-D", "feature")
			if existing {
				f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
			}
			o := f.refresh(t, false, RefreshPack{Name: "q", Source: f.source(c2)})[0]
			if o.Err != nil || o.FetchErr != nil || o.Commit != c2 {
				t.Fatalf("outcome = %+v, want the pinned %s delivered", o, c2[:8])
			}
			if _, err := os.Stat(filepath.Join(f.store.Dir, "trees", c2, "two")); err != nil {
				t.Errorf("the pinned commit's file was not checked out: %v", err)
			}
		})
	}
}

// A PINNED COMMIT THE REMOTE LACKS (a typo) is named by the fetch that asked for it, and fails
// no other pack: the branch pack and the pack pinned to a real commit on no branch, refreshed
// with it, resolve with nothing to report. A later process, which has no record of that fetch,
// reads the stamp the good fetch of the branches wrote and says the remote has no such commit.
func TestRefreshNamesTheFetchOfAPinnedCommitTheRemoteLacks(t *testing.T) {
	f := newRefreshFixture(t)
	gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
	gitIn(t, f.repo, "checkout", "-q", "-b", "feature")
	offBranch := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "checkout", "-q", "main")
	gitIn(t, f.repo, "branch", "-q", "-D", "feature")
	const typo = "0123456789abcdef0123456789abcdef01234567"
	outs := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")},
		RefreshPack{Name: "q", Source: f.source(typo)}, RefreshPack{Name: "r", Source: f.source(offBranch)})
	for _, o := range []Outcome{outs[0], outs[2]} {
		if o.Err != nil || o.FetchErr != nil || o.Commit == "" {
			t.Errorf("%s: outcome = %+v, want it resolved with nothing to report", o.Name, o)
		}
	}
	o := outs[1]
	t.Logf("Err = %v", o.Err)
	if o.Err == nil || !strings.Contains(o.Err.Error(), "fetching it by its id failed") ||
		!strings.Contains(o.Err.Error(), "not our ref "+typo) || errors.Is(o.Err, ErrNotFetched) {
		t.Errorf("Err = %v, want the failed fetch of %s by its id, with git's reason", o.Err, typo[:8])
	}
	a := mustParse(t, f.source(typo))
	pinnedFetchFailures.Delete(f.store.decidedKey(a))
	if _, err := f.store.Resolve(a, "q"); err == nil || !strings.Contains(err.Error(), "not found on the remote") {
		t.Errorf("a later process's Resolve error = %v, want the remote's lack of it", err)
	}
}

// A MALFORMED COMMIT NEVER ENTERS THE STORE, however it is asked for: every git run that receives
// objects checks them (fsckArgs), and no other run receives any. The remote holds a commit fsck
// rejects, pinned by its id, on a branch (so the refresh's fetch of the branches meets it) or on
// none (so the fetch of the pinned commit does). The lookup's own fetch used to receive it with
// no check at all, and the pack was delivered at it. Now the pack is unusable, its error names
// fsck's objection, and the mirror does not hold the commit.
func TestRefreshRefusesAMalformedPinnedCommit(t *testing.T) {
	requireNoLazyFetchSwitch(t)
	for _, onBranch := range []bool{false, true} {
		t.Run(fmt.Sprintf("on a branch %v", onBranch), func(t *testing.T) {
			f := newRefreshFixture(t)
			gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
			f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")})
			bad := malformedCommit(t, f.repo)
			if onBranch {
				gitIn(t, f.repo, "branch", "bad", bad)
			}
			pack := RefreshPack{Name: "q", Source: f.source(bad)}
			o := f.refresh(t, false, pack)[0]
			t.Logf("Err = %v", o.Err)
			if o.Commit != "" || o.Err == nil || !strings.Contains(o.Err.Error(), "badDate") {
				t.Errorf("outcome = %+v, want the pack unusable and fsck's badDate named", o)
			}
			if _, err := f.store.Resolve(mustParse(t, pack.Source), "q"); err == nil ||
				!strings.Contains(err.Error(), "badDate") {
				t.Errorf("Resolve error = %v, want fsck's badDate named", err)
			}
			if mirrorHolds(t, f.store.mirrorPath(mustParse(t, pack.Source).Repo), bad) {
				t.Errorf("the malformed commit %s is in the mirror", bad[:8])
			}
		})
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

// (k) FSCK ON EVERY RUN THAT RECEIVES OBJECTS: the clone, the fetch, the fetch of a pinned
// commit on no branch or tag (fetchPinnedCommits), the one-request prefetch of the files a
// checkout needs (prefetchBlobs), and the checkout (whose lazy blob fetch inherits `-c`). NO
// OTHER RUN MAY RECEIVE ANY: every run without fsck, a lookup such as rev-parse, runs with
// GIT_NO_LAZY_FETCH=1, and every run with it with GIT_NO_LAZY_FETCH=0. The prompt hygiene
// reaches the child's environment.
//
// The remote allows the blobless filter (uploadpack.allowFilter), as a real host does. Without
// it git ignores --filter over file:// and sends every file with the clone, so the prefetch
// finds nothing missing and never runs, and this test would not see the one run that now
// receives every file a checkout reads.
func TestRefreshChecksObjectsOnEveryNetworkRun(t *testing.T) {
	f := newRefreshFixture(t)
	gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
	gitIn(t, f.repo, "checkout", "-q", "-b", "feature")
	pinned := commitFile(t, f.repo, "two", "2")
	gitIn(t, f.repo, "checkout", "-q", "main")
	gitIn(t, f.repo, "branch", "-q", "-D", "feature")
	log := filepath.Join(t.TempDir(), "argv")
	f.store.Git = writeScript(t, "echo \"$GIT_TERMINAL_PROMPT|$GIT_NO_LAZY_FETCH|$*\" >> '"+log+"'\nexec git \"$@\"\n")
	for _, o := range f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")},
		RefreshPack{Name: "q", Source: f.source(pinned)}) {
		if o.Err != nil {
			t.Fatal(o.Err)
		}
	}
	data, _ := os.ReadFile(log)
	const prefetch, pinnedFetch = "prefetch (fetch --stdin)", "pinned (fetch <commit>)"
	runs := []string{"clone", "fetch", pinnedFetch, prefetch, "checkout", "rev-parse"}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.SplitN(line, "|", 3)
		if len(fields) != 3 {
			t.Fatalf("log line %q", line)
		}
		prompt, lazy, argv := fields[0], fields[1], fields[2]
		if prompt != "0" {
			t.Errorf("git ran with GIT_TERMINAL_PROMPT=%q: %s", prompt, argv)
		}
		fsck := strings.Contains(argv, "transfer.fsckObjects=true")
		if want := map[bool]string{true: "0", false: "1"}[fsck]; lazy != want {
			t.Errorf("git ran with GIT_NO_LAZY_FETCH=%q, want %q (fsck %v): %s", lazy, want, fsck, argv)
		}
		for _, sub := range runs {
			match := strings.Contains(" "+argv+" ", " "+sub+" ")
			switch sub {
			case prefetch:
				match = strings.Contains(" "+argv+" ", " fetch ") && strings.HasSuffix(argv, " --stdin")
			case pinnedFetch:
				match = strings.Contains(" "+argv+" ", " fetch ") && strings.HasSuffix(argv, " "+pinned)
			}
			if match {
				seen[sub] = true
				if want := sub != "rev-parse"; fsck != want {
					t.Errorf("%s ran with fsck %v, want %v: %s", sub, fsck, want, argv)
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
