package packsrc

// refresh_probe_test.go pins the launch-time PROBE that replaced the hourly stamp test in fetchStep
// (docs/design/local-pack-clones.md, LPC-D1/LPC-D2): a branch pack is asked of its remote with one
// ls-remote at every launch, an unmoved branch runs no fetch, a moved one fetches once, and the
// stamp is a display of "last fetched" that no decision reads. A recording git stands between the
// store and the real one, so "no fetch ran" is read from what git was asked, not from timing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// recordingGit is a git that appends its arguments to a log, one line per run, then runs the real
// git. ran counts the runs that had the named subcommand as a whole argument.
func recordingGit(t *testing.T) (git string, ran func(sub string) int, reset func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	git = filepath.Join(dir, "git")
	script := "#!/bin/sh\necho \"$@\" >> " + shquote.Quote(log) + "\nexec " + shquote.Quote(real) + " \"$@\"\n"
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ran = func(sub string) int {
		data, _ := os.ReadFile(log)
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			for _, f := range strings.Fields(line) {
				if f == sub {
					n++
					break
				}
			}
		}
		return n
	}
	reset = func() { _ = os.Remove(log) }
	return git, ran, reset
}

// AN UNMOVED BRANCH RUNS NO FETCH, whatever the stamp's age: two hours on, the probe says the remote
// is where the mirror is, so git is asked once and nothing is fetched or said.
func TestAnUnmovedBranchIsProbedAndNotFetched(t *testing.T) {
	f := newRefreshFixture(t)
	git, ran, reset := recordingGit(t)
	f.store.Git = git
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)

	reset()
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if o.Err != nil || o.FetchErr != nil || o.Fetched || o.Commit != c1 || o.Disclosure() != "" {
		t.Fatalf("unmoved branch: %+v, want no fetch at %s and nothing said", o, c1)
	}
	if got := ran("ls-remote"); got != 1 {
		t.Errorf("ls-remote ran %d times, want one probe", got)
	}
	if got := ran("fetch"); got != 0 {
		t.Errorf("fetch ran %d times for an unmoved branch, want none", got)
	}
}

// A MOVED BRANCH FETCHES AT ONCE, INSIDE THE OLD HOUR: the stamp that used to hold the fetch off is
// fresh, the remote has a new commit, and the launch reaches it with one fetch.
func TestAMovedBranchIsFetchedInsideTheOldInterval(t *testing.T) {
	f := newRefreshFixture(t)
	git, ran, reset := recordingGit(t)
	f.store.Git = git
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	c2 := commitFile(t, f.repo, "two", "2")

	reset()
	f.now = f.now.Add(time.Minute)
	o := f.refresh(t, false, pack)[0]
	if o.Err != nil || !o.Fetched || o.Commit != c2 {
		t.Fatalf("moved branch: %+v, want a fetch reaching %s", o, c2)
	}
	if got := ran("fetch"); got != 1 {
		t.Errorf("fetch ran %d times, want exactly one", got)
	}
	if got, want := o.Disclosure(), "Updated pack p: main "+c1[:8]+" → "+c2[:8]; got != want {
		t.Errorf("Disclosure() = %q, want %q", got, want)
	}
}

// THE STAMP IS DISPLAY ONLY: a missing stamp does not make an unmoved branch fetch.
func TestTheStampDecidesNothing(t *testing.T) {
	f := newRefreshFixture(t)
	git, ran, reset := recordingGit(t)
	f.store.Git = git
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	a, _ := Parse(pack.Source)
	if err := os.Remove(f.store.stampPath(a)); err != nil {
		t.Fatal(err)
	}
	reset()
	if o := f.refresh(t, false, pack)[0]; o.Fetched {
		t.Fatalf("an unmoved branch with no stamp fetched: %+v", o)
	}
	if got := ran("fetch"); got != 0 {
		t.Errorf("fetch ran %d times with no stamp, want none", got)
	}
}

// A PROBE THAT CANNOT REACH THE REMOTE runs the held copy with the failure kept for the warning, and
// does not fall through to a fetch that would wait out the same outage.
func TestAFailedProbeRunsTheHeldCopyWithoutFetching(t *testing.T) {
	f := newRefreshFixture(t)
	git, ran, reset := recordingGit(t)
	f.store.Git = git
	c1 := f.head(t)
	pack := RefreshPack{Name: "p", Source: f.source("main")}
	f.refresh(t, false, pack)
	f.breakRemote(t)

	reset()
	f.now = f.now.Add(2 * BranchRefreshInterval)
	o := f.refresh(t, false, pack)[0]
	if o.Err != nil || o.FetchErr == nil || o.Commit != c1 || o.Fetched {
		t.Fatalf("unreachable remote: %+v, want the held %s with the probe error kept", o, c1)
	}
	if !strings.Contains(o.Warning(), "using the cached "+c1[:8]) {
		t.Errorf("Warning() = %q, want it to name the cached commit", o.Warning())
	}
	if got := ran("fetch"); got != 0 {
		t.Errorf("fetch ran %d times after a failed probe, want none", got)
	}
}

// A FROZEN REF IS NEVER PROBED: a tag and a full commit never move, so asking the remote about them
// would be a round trip that decides nothing.
func TestAFrozenRefIsNotProbed(t *testing.T) {
	f := newRefreshFixture(t)
	git, ran, reset := recordingGit(t)
	f.store.Git = git
	c1 := f.head(t)
	gitIn(t, f.repo, "tag", "v1")
	for _, ref := range []string{"v1", c1} {
		pack := RefreshPack{Name: "p", Source: f.source(ref)}
		f.refresh(t, false, pack)
		reset()
		f.now = f.now.Add(2 * BranchRefreshInterval)
		if o := f.refresh(t, false, pack)[0]; o.Fetched || o.Commit != c1 {
			t.Fatalf("ref %s: %+v, want the frozen %s", ref, o, c1)
		}
		if got := ran("ls-remote"); got != 0 {
			t.Errorf("ref %s: ls-remote ran %d times, want none", ref, got)
		}
	}
}

// A PATCHED FORK'S CHECK PROBES TAGS TOO: a release tag on a commit the mirror already holds leaves the
// branch tip where it was, yet the check must fetch it, as its stale stamp used to make it do.
func TestAForkCheckFetchesANewTagOnAnUnmovedBranch(t *testing.T) {
	u := newPatchedUpstream(t)
	git, ran, reset := recordingGit(t)
	u.store.Git = git
	w := u.want(t, "main", "")
	u.check(t, w, false)

	reset()
	u.now = u.now.Add(2 * BranchRefreshInterval)
	if res := u.check(t, w, false); res.Record.Check.Fetched || ran("fetch") != 0 {
		t.Fatalf("nothing moved: fetched %v, %d fetch runs, want none", res.Record.Check.Fetched, ran("fetch"))
	}
	gitIn(t, u.repo, "tag", "v1.0.1") // a new tag on the tip the mirror holds
	reset()
	u.now = u.now.Add(2 * BranchRefreshInterval)
	if res := u.check(t, w, false); !res.Record.Check.Fetched || ran("fetch") != 1 {
		t.Fatalf("a new tag: fetched %v, %d fetch runs, want one", res.Record.Check.Fetched, ran("fetch"))
	}
}
