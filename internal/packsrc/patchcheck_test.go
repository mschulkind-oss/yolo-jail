package packsrc

// patchcheck_test.go pins a patched fork's CHECK (patchcheck.go; docs/design/patched-forks.md §4,
// PF-D5, PF-D10) against a real upstream repository: the follow rule and the walk's list, the
// throttle that runs no git, the attempt that counts, the fetch with no checkout, and the lock
// order.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
	if err := u.store.saveCheckRecord(&CheckRecord{Owner: w.Owner, CheckedAt: u.now.Unix(), Read: in, Seq: 7,
		Check: &CheckFound{Seq: 7, At: u.now.Unix()}}); err != nil {
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

// blockingGit is a git that stops at its first run until release is called: it marks that it
// started, waits for the release file, then runs the real git. A check run through it is held
// inside its record lock after the attempt's save, the state a check killed in its fetch leaves.
func blockingGit(t *testing.T) (bin string, started func() bool, release func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mark, gate := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	bin = filepath.Join(dir, "git")
	writeTestFile(t, bin, "#!/bin/sh\n: > "+shquote.Quote(mark)+"\nwhile [ ! -e "+shquote.Quote(gate)+
		" ]; do sleep 0.05; done\nexec "+shquote.Quote(real)+" \"$@\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	started = func() bool { _, err := os.Stat(mark); return err == nil }
	release = func() { writeTestFile(t, gate, "") }
	return bin, started, release
}

// A CHECK KILLED IN ITS GIT NEVER PAIRS NEW INPUTS WITH THE OLD LIST (§4.2): the attempt's stamp
// is written before the fetch, and what the check read only with what it found. So while a check
// under an edited follow rule is stopped inside its first git — what a kill there would leave on
// disk — the record still reads as the head rule's, the release check is due at once, and the head
// rule's list is no candidate under release.
func TestACheckStoppedInItsGitLeavesAnEditDue(t *testing.T) {
	u := newPatchedUpstream(t)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	u.release(t, "", map[int]string{14: "fourteen", 25: "tip"})
	head, rel := u.want(t, "main", "head"), u.want(t, "main", "")
	u.check(t, head, false)

	bin, started, release := blockingGit(t)
	blocked := &Store{Dir: u.store.Dir, Git: bin, Getenv: noStagedTree}
	u.now = u.now.Add(5 * time.Minute)
	done := make(chan CheckResult, 1)
	go func() {
		done <- blocked.CheckPatched(rel, CheckOptions{Now: func() time.Time { return u.now }})
	}()
	deadline := time.Now().Add(30 * time.Second)
	for !started() {
		if time.Now().After(deadline) {
			release()
			t.Fatal("the release check never ran git")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mid, err := u.store.LoadCheckRecord(rel.Owner)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if due, why := CheckDue(mid, mustInputs(t, rel), u.now, 0); !due {
		t.Errorf("mid-check, the record reads as a fresh check of the release rule (Read %+v): a kill "+
			"here would serve the head rule's list for an hour", mid.Read)
	} else if !strings.Contains(why, "changed") {
		t.Errorf("mid-check the release check is due for %q, want the edit", why)
	}
	if got := mid.Candidates(mustInputs(t, rel)); got != nil {
		t.Errorf("mid-check the release rule's candidates are %q, the head rule's list", listLabels(got))
	}
	if got := listLabels(mid.Candidates(mustInputs(t, head))); !strings.HasSuffix(got, "* v1.1.0 v1.0.0") {
		t.Errorf("mid-check the head rule's own list = %q, want it intact", got)
	}
	release()
	res := <-done
	if res.Err != nil || !res.Ran || res.Record.Read != mustInputs(t, rel) {
		t.Fatalf("the release check = %+v", res)
	}
	if got := listLabels(res.Record.Candidates(res.Inputs)); got != "v1.1.0 v1.0.0" {
		t.Errorf("after the release check the candidates = %q, want v1.1.0 v1.0.0", got)
	}
}

// A CHECK WITH NO ANSWER IS NO ANSWER: a record whose attempt was stamped but whose check never
// finished is due at once, not served for the interval.
func TestAnAttemptWithNoAnswerIsDue(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	r := &CheckRecord{Owner: w.Owner, CheckedAt: u.now.Unix(), Read: mustInputs(t, w)}
	if due, why := CheckDue(r, mustInputs(t, w), u.now, 0); !due || !strings.Contains(why, "finished") {
		t.Errorf("an attempt with no answer is due=%v (%q), want due at once", due, why)
	}
}

// A CHECK WITH A PROBLEM KEEPS THE OUTCOMES: a ref edited to HEAD for one check lists nothing, and
// the conflict recorded before it is still there once the ref is put back (PF-D9: no launch replays
// that key again).
func TestACheckWithAProblemKeepsTheOutcomes(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	w := u.want(t, "main", "")
	u.check(t, w, true)
	conflict := EntryOutcome{Commit: v11, Kind: OutcomeConflict, Series: "s", Yolo: "y", Git: "g", Member: "0001-x.patch"}
	if err := u.store.WithCheckRecord(w.Owner, nil, func(r *CheckRecord, _ error, _ func() error) (bool, error) {
		r.SetOutcome(conflict)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	res := u.check(t, u.want(t, "HEAD", ""), true)
	if res.Record.Check.Problem == "" {
		t.Fatal("?ref=HEAD named a candidate")
	}
	if res.Record.Conflict(v11, "s", "y", "g") == nil {
		t.Errorf("a check with a problem dropped the recorded conflict: %+v", res.Record.Outcomes)
	}
	res = u.check(t, w, true)
	if res.Record.Conflict(v11, "s", "y", "g") == nil {
		t.Errorf("the conflict did not survive back to the branch: %+v", res.Record.Outcomes)
	}
}

// AN OUTCOME LEAVES WITH ITS ENTRY: the record keeps one outcome per entry of the walk's list, so a
// conflict at a version the follow rule no longer lists is dropped by the next check, and one at a
// version it still lists is kept.
func TestAnOutcomeLeavesWithItsEntry(t *testing.T) {
	u := newPatchedUpstream(t)
	v11 := u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	agent := u.release(t, "agent@2.0.0", map[int]string{14: "fourteen", 20: "twenty"})
	w := u.want(t, "main", "")
	u.check(t, w, true)
	if err := u.store.WithCheckRecord(w.Owner, nil, func(r *CheckRecord, _ error, _ func() error) (bool, error) {
		r.SetOutcome(EntryOutcome{Commit: v11, Kind: OutcomeConflict, Series: "s", Yolo: "y", Git: "g"})
		r.SetOutcome(EntryOutcome{Commit: agent, Kind: OutcomeConflict, Series: "s", Yolo: "y", Git: "g"})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	res := u.check(t, u.want(t, "main", "release:agent@"), true)
	if got := listLabels(res.Record.Check.List); got != "agent@2.0.0" {
		t.Fatalf("under release:agent@ the list = %q", got)
	}
	if res.Record.Conflict(v11, "s", "y", "g") != nil {
		t.Error("the conflict at v1.1.0, which left the list, is still recorded")
	}
	if res.Record.Conflict(agent, "s", "y", "g") == nil {
		t.Error("the conflict at agent@2.0.0, still on the list, was dropped")
	}
}

// A HOLD IS THAT COMMIT, ALWAYS (§3.3, PF-D34): a tag or full-commit ref's one entry is a candidate
// whatever version the good build runs, the same for both spellings of one commit, and nothing is
// pending once the good build is that commit.
func TestAHoldOlderThanTheGoodBuildIsACandidate(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "v1.0.0", Follow: "release", Base: "b"}
	good := &GoodBuild{Commit: "c2", Tag: "v1.1.0", Version: "1.1.0"}
	for _, held := range []struct {
		kind  string
		entry ListEntry
	}{{"tag", ListEntry{Commit: "c1", Tag: "v1.0.0", Version: "1.0.0"}}, {"commit", ListEntry{Commit: "c1"}}} {
		r := &CheckRecord{Read: in, Good: good, Check: &CheckFound{RefKind: held.kind, List: []ListEntry{held.entry}}}
		if got := r.Candidates(in); len(got) != 1 || got[0].Commit != "c1" {
			t.Errorf("a %s hold older than the good build: candidates = %q, want the held commit", held.kind, listLabels(got))
		}
		r.Good = &GoodBuild{Commit: "c1", Version: "1.0.0"}
		if got := r.Candidates(in); len(got) != 0 {
			t.Errorf("a %s hold at the good build's commit: candidates = %q, want none", held.kind, listLabels(got))
		}
	}
}

// AN EDITED FOLLOW NAMES A CANDIDATE LIKE ANY OTHER (§3.3, PF-D34): after head → release the good
// build is the old tip, which runs v1.1.0, and the release list is no longer cut by that version —
// while a list that holds the good build's own commit is still cut there, so a fit below it never
// replaces it. A good build that records no inputs is cut by precedence, as the same rule's is.
func TestAnEditedFollowIsCutAtTheGoodBuildsCommit(t *testing.T) {
	head := CheckInputs{Repo: "r", Ref: "main", Follow: "head", Base: "b"}
	rel := head
	rel.Follow = "release"
	list := []ListEntry{{Commit: "c2", Tag: "v1.1.0", Version: "1.1.0"}, {Commit: "c1", Tag: "v1.0.0", Version: "1.0.0"}}
	r := &CheckRecord{Read: rel, Check: &CheckFound{RefKind: "branch", List: list},
		Good: &GoodBuild{Commit: "tip", Version: "1.1.0", Read: &head}}
	if got := listLabels(r.Candidates(rel)); got != "v1.1.0 v1.0.0" {
		t.Errorf("after head → release the candidates = %q, want the release list", got)
	}
	r.Good.Read = nil
	if got := listLabels(r.Candidates(rel)); got != "" {
		t.Errorf("a good build recording no inputs is not cut by precedence: %q", got)
	}
	r.Good.Read = &rel
	if got := listLabels(r.Candidates(rel)); got != "" {
		t.Errorf("under the good build's own rule the candidates = %q, want the precedence cut's none", got)
	}
	// A hold lifted onto the branch: the good build is v1.1.0's commit, on the list, so v1.2.0 is a
	// candidate and v1.0.0, below it, is not.
	tag := rel
	tag.Ref = "v1.1.0"
	r.Check.List = append([]ListEntry{{Commit: "c3", Tag: "v1.2.0", Version: "1.2.0"}}, list...)
	r.Good = &GoodBuild{Commit: "c2", Tag: "v1.1.0", Version: "1.1.0", Read: &tag}
	if got := listLabels(r.Candidates(rel)); got != "v1.2.0" {
		t.Errorf("a hold lifted onto the branch: candidates = %q, want v1.2.0 alone", got)
	}
	// The base is not what the rule follows: a re-exported series keeps the precedence cut, here
	// over a good build whose commit is not on the list (it runs v1.2.0 plus more).
	rebased := rel
	rebased.Base = "b2"
	r.Read, r.Good.Read = rebased, &rel
	r.Good.Commit, r.Good.Version = "c9", "1.2.0"
	if got := listLabels(r.Candidates(rebased)); got != "" {
		t.Errorf("a re-exported series dropped the precedence cut: %q", got)
	}
}
