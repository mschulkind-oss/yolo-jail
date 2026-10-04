package packsrc

// patchcheck_test.go pins a patched fork's CHECK (patchcheck.go; docs/design/patched-forks.md §4,
// PF-D5, PF-D10) against a real upstream repository: the follow rule and the walk's list, the
// throttle that runs no git, the attempt that counts, the fetch with no checkout, and the lock
// order.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// THE LIST UNDER release: the version tags merged into the branch that contain the series' base,
// newest first by precedence, the base's own tag included — no pre-release, no tag that is not a
// version, no version the branch does not contain.
func TestTheCheckListsTheVersionsThatContainTheBase(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	u.release(t, "v1.2.0-rc.1", map[int]string{14: "fourteen", 20: "twenty"})
	u.release(t, "nightly", map[int]string{14: "fourteen", 21: "x"})
	top := u.release(t, "v1.10.0", map[int]string{14: "fourteen", 22: "y"})
	// A version on a commit the branch does not contain is invisible.
	gitIn(t, u.repo, "checkout", "-q", "-b", "side")
	u.release(t, "v9.0.0", map[int]string{1: "side"})
	gitIn(t, u.repo, "checkout", "-q", "main")

	res := u.check(t, u.want(t, "main", ""), false)
	if !res.Ran {
		t.Fatal("a first check did not run")
	}
	f := res.Record.Check
	if f == nil || f.Problem != "" {
		t.Fatalf("check found %+v", f)
	}
	if got := listLabels(f.List); got != "v1.10.0 v1.1.0 v1.0.0" {
		t.Errorf("the walk's list = %q, want v1.10.0 v1.1.0 v1.0.0", got)
	}
	if f.List[0].Commit != top || f.RefKind != "branch" || !f.BaseOnBranch || !f.Fetched {
		t.Errorf("check = %+v, want the branch followed, its base on it, fetched", f)
	}
	if res.Record.Seq != 1 || f.Seq != 1 {
		t.Errorf("sequence = %d/%d, want 1", res.Record.Seq, f.Seq)
	}
	// NO CHECKOUT: the check leaves no tree in the pack store, which nothing reaps.
	mustNotExist(t, filepath.Join(u.store.Dir, "trees"), "a check checked a commit out")
}

// UNDER head the branch's tip comes first, then the versions; under release:<prefix> only that
// prefix's tags count.
func TestTheFollowRuleHeadAndPrefix(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	u.release(t, "agent@2.0.0", map[int]string{14: "fourteen", 20: "twenty"})
	tip := u.release(t, "", map[int]string{14: "fourteen", 20: "twenty", 25: "tip"})

	res := u.check(t, u.want(t, "main", "head"), false)
	if got := listLabels(res.Record.Check.List); got != shortCommit(tip)+"* v1.1.0 v1.0.0" {
		t.Errorf("under head the list = %q, want the tip then the versions", got)
	}
	u.now = u.now.Add(2 * time.Hour)
	res = u.check(t, u.want(t, "main", "release:agent@"), false)
	if got := listLabels(res.Record.Check.List); got != "agent@2.0.0" {
		t.Errorf("under release:agent@ the list = %q, want agent@2.0.0 alone", got)
	}
}

// A BASE PAST THE NEWEST VERSION lists nothing — no older version passes for an update — and the
// branch still contains the base, which a first advance builds (PF-D10).
func TestABasePastTheNewestVersionListsNothing(t *testing.T) {
	u := newPatchedUpstream(t)
	// Re-export the series onto a base past every tag.
	newBase := u.release(t, "", map[int]string{14: "fourteen"})
	gitIn(t, u.repo, "checkout", "-q", "-b", "fork2")
	commitFile(t, u.repo, "f.txt", thirtyLines(map[int]string{14: "fourteen", 10: "ten"}))
	os.RemoveAll(filepath.Join(u.pack, "patches"))
	gitIn(t, u.repo, "format-patch", "-q", "--base="+newBase, "-o", filepath.Join(u.pack, "patches"), "main..fork2")
	gitIn(t, u.repo, "checkout", "-q", "main")

	res := u.check(t, u.want(t, "main", ""), false)
	f := res.Record.Check
	if len(f.List) != 0 || !f.BaseOnBranch || f.Problem != "" {
		t.Errorf("check = %+v, want an empty list with the base on the branch", f)
	}
}

// A TAG OR A FULL COMMIT HOLDS: the list is that commit alone. HEAD and an abbreviated commit name
// nothing that holds, and are the fork's reason.
func TestARefThatHoldsAndOnesThatAreRefused(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	res := u.check(t, u.want(t, "v1.0.0", ""), false)
	if f := res.Record.Check; f.RefKind != "tag" || listLabels(f.List) != "v1.0.0" {
		t.Errorf("a tag ref = %+v, want it held at v1.0.0", f)
	}
	res = u.check(t, u.want(t, u.base, ""), false)
	if f := res.Record.Check; f.RefKind != "commit" || len(f.List) != 1 || f.List[0].Commit != u.base {
		t.Errorf("a commit ref = %+v, want it held there", f)
	}
	for _, ref := range []string{"HEAD", u.base[:10]} {
		res = u.check(t, u.want(t, ref, ""), true)
		if f := res.Record.Check; !strings.Contains(f.Problem, "neither a branch, a tag nor a full commit") ||
			len(f.List) != 0 {
			t.Errorf("?ref=%s = %+v, want it refused with the spellings that work", ref, f)
		}
	}
	res = u.check(t, u.want(t, "no-such-branch", ""), true)
	if f := res.Record.Check; !strings.Contains(f.Problem, "names no branch, tag or commit") {
		t.Errorf("an unresolvable ref = %+v", f)
	}
}

// A BASE THE UPSTREAM DOES NOT HAVE is no candidate list, and the reason names the re-export.
func TestABaseTheUpstreamLacksIsTheReason(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	w.Base = strings.Repeat("b", 40)
	res := u.check(t, w, false)
	if f := res.Record.Check; !strings.Contains(f.Problem, "is not a commit of") ||
		!strings.Contains(f.Problem, "git format-patch --base") {
		t.Errorf("a missing base = %+v", f)
	}
}

// THE THROTTLE IS A FILE READ: inside the interval, with nothing it reads changed, a check runs no
// git — proven with a git that does not exist — and an edit to what it reads makes it due at once.
func TestTheThrottleRunsNoGitInsideTheInterval(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	broken := &Store{Dir: u.store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	begun := false
	u.now = u.now.Add(59 * time.Minute)
	res := broken.CheckPatched(w, CheckOptions{Now: func() time.Time { return u.now },
		Begin: func() (func(string), func()) { begun = true; return nil, func() {} }})
	if res.Ran || begun || res.Err != nil || res.Record == nil || res.Record.Seq != 1 {
		t.Errorf("a check inside the interval = %+v (begun %v), want the record read and no git", res, begun)
	}
	// An edited follow is due at once.
	w2 := w
	w2.Follow = "head"
	if due, why := CheckDue(res.Record, mustInputs(t, w2), u.now, 0); !due || !strings.Contains(why, "changed") {
		t.Errorf("an edited follow is due=%v (%q), want due at once", due, why)
	}
	// And past the interval, due.
	if due, _ := CheckDue(res.Record, mustInputs(t, w), u.now.Add(2*time.Minute), 0); !due {
		t.Error("a check past the interval is not due")
	}
	// An explicit act ignores the throttle.
	if res := u.check(t, w, true); !res.Ran || res.Record.Seq != 2 {
		t.Errorf("a forced check = %+v, want it run", res)
	}
}

func mustInputs(t *testing.T, w PatchedWant) CheckInputs {
	t.Helper()
	in, _, _, err := w.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// THE CHECK'S STAMP IS ITS OWN, NOT THE MIRROR'S: a branch pack on the same repository refreshed
// just now leaves the mirror's stamp fresh, and the check still runs — without a fetch, since the
// mirror is current.
func TestAPackRefreshOfTheSameBranchDoesNotSkipTheCheck(t *testing.T) {
	u := newPatchedUpstream(t)
	if _, err := u.store.Refresh([]RefreshPack{{Name: "a-pack", Source: u.source("main")}},
		RefreshOptions{Now: func() time.Time { return u.now }}); err != nil {
		t.Fatal(err)
	}
	res := u.check(t, u.want(t, "main", ""), false)
	if !res.Ran || res.Record.Check.Fetched || len(res.Record.Check.List) != 1 {
		t.Errorf("after a pack refresh of the branch, the check = %+v (%+v), want it run from the "+
			"fresh mirror with no fetch", res, res.Record.Check)
	}
}

// AN ATTEMPT COUNTS: a check whose fetch failed writes its stamp, so the next launch inside the
// hour runs no git, and the record names the failure.
func TestAFailedFetchStillStampsTheAttempt(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	if err := os.Rename(u.repo, u.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(u.repo+".gone", u.repo) })
	u.now = u.now.Add(2 * time.Hour)
	res := u.check(t, w, false)
	f := res.Record.Check
	if !res.Ran || f.FetchErr == "" || f.Fetched || listLabels(f.List) != "v1.0.0" {
		t.Errorf("an offline check = %+v, want the failure noted and the mirror's answer kept", f)
	}
	u.now = u.now.Add(10 * time.Minute)
	if again := u.check(t, w, false); again.Ran {
		t.Error("the check after a failed one ran inside the interval: the attempt did not count")
	}
}

// A RE-POINTED TAG IS NOT FOLLOWED, even by an explicit act's forced fetch: the mirror keeps the
// object the tag first named (PF-D4).
func TestARePointedTagIsNotFollowed(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, true)
	u.release(t, "", map[int]string{14: "fourteen"})
	gitIn(t, u.repo, "tag", "-f", "v1.0.0")
	res := u.check(t, w, true)
	if l := res.Record.Check.List; len(l) != 1 || l[0].Commit != u.base {
		t.Errorf("after the tag moved upstream the list = %+v, want v1.0.0 still at the base %s", l, u.base)
	}
}

// THE LOCK ORDER is the record lock, then the mirror's (PF-D17): a check waiting for the record
// lock holds no mirror lock. Were it inverted, the probe's non-blocking take of the mirror lock
// below would fail while the check waits.
func TestTheCheckTakesTheRecordLockBeforeTheMirrors(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	a, _ := Parse(w.Source)
	unlock, err := u.store.lockCheckRecord(w.Owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	done := make(chan CheckResult)
	go func() {
		done <- u.store.CheckPatched(w, CheckOptions{Force: true, Now: func() time.Time { return u.now },
			Begin: func() (func(string), func()) {
				return func(string) { close(waiting) }, func() {}
			}})
	}()
	select {
	case <-waiting:
	case <-time.After(30 * time.Second):
		t.Fatal("the check never waited for the record lock")
	}
	f, err := os.OpenFile(filepath.Join(u.store.Dir, "locks", mirrorSlug(a.Repo)+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("the mirror lock is held while the check waits for the record lock: %v", err)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
	unlock()
	if res := <-done; !res.Ran {
		t.Errorf("the check did not run once the record lock was free: %+v", res)
	}
}

// THE LOSER OF A CHECK RACE TAKES THE WINNER'S: a check that waited for the record lock re-reads
// the stamp the other check wrote and runs no git.
func TestACheckThatWaitedReadsTheOthersStamp(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	in := mustInputs(t, w)
	unlock, err := u.store.lockCheckRecord(w.Owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	done := make(chan CheckResult)
	broken := &Store{Dir: u.store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	go func() {
		done <- broken.CheckPatched(w, CheckOptions{Now: func() time.Time { return u.now },
			Begin: func() (func(string), func()) { return func(string) { close(waiting) }, func() {} }})
	}()
	<-waiting
	// The "other launch" writes a fresh check while this one waits.
	if err := u.store.saveCheckRecord(&CheckRecord{Owner: w.Owner, CheckedAt: u.now.Unix(), Read: in, Seq: 7}); err != nil {
		t.Fatal(err)
	}
	unlock()
	res := <-done
	if res.Ran || res.Record == nil || res.Record.Seq != 7 {
		t.Errorf("the waiter = %+v, want the other check's record and no git", res)
	}
}

// AboveGood cuts the list at the good build: the tip when it differs, and versions of higher
// precedence than the good build runs.
func TestAboveGoodCutsTheListAtTheGoodBuild(t *testing.T) {
	list := []ListEntry{{Commit: "t", Tip: true}, {Commit: "c3", Tag: "v1.3.0", Version: "1.3.0"},
		{Commit: "c2", Tag: "v1.2.0", Version: "1.2.0"}, {Commit: "c1", Tag: "v1.1.0", Version: "1.1.0"}}
	if got := listLabels(AboveGood(list, nil)); got != "t* v1.3.0 v1.2.0 v1.1.0" {
		t.Errorf("with no good build = %q, want the whole list", got)
	}
	if got := listLabels(AboveGood(list, &GoodBuild{Commit: "c2", Version: "1.2.0"})); got != "t* v1.3.0" {
		t.Errorf("above v1.2.0 = %q, want the tip and v1.3.0", got)
	}
	if got := listLabels(AboveGood(list, &GoodBuild{Commit: "t", Version: "1.3.0"})); got != "" {
		t.Errorf("at the tip, which runs v1.3.0 = %q, want nothing", got)
	}
	if got := listLabels(AboveGood(list, &GoodBuild{Commit: "base"})); got != "t* v1.3.0 v1.2.0 v1.1.0" {
		t.Errorf("above a good build that runs no version = %q, want every entry", got)
	}
}
