package packsrc

// patchcheck_test.go pins a patched fork's CHECK (patchcheck.go; docs/reference/patched-forks.md §4,
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

func TestFreshCheckUsesStartedSequenceInsteadOfWallClock(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	before, err := u.store.CheckBoundary(w.Owner)
	if err != nil || before.Seq != 0 {
		t.Fatalf("initial fresh-check boundary = %+v, %v; want 0", before, err)
	}
	first := u.store.CheckPatched(w, CheckOptions{FreshAfter: &before,
		Now: func() time.Time { return u.now }})
	if first.Err != nil || !first.Ran || !first.Record.Fresh.Qualifies(before, "") || first.Record.Fresh.Seq != 1 {
		t.Fatalf("fresh check = %+v, want first post-registration start", first)
	}

	// A request registered after that check started must not treat its completion as fresh. Move
	// the clock backwards as well: only the monotonic start sequence can force the follow-up.
	afterStart, err := u.store.CheckBoundary(w.Owner)
	if err != nil || afterStart.Seq != 1 {
		t.Fatalf("post-start boundary = %+v, %v; want 1", afterStart, err)
	}
	u.now = u.now.Add(-time.Hour)
	second := u.store.CheckPatched(w, CheckOptions{FreshAfter: &afterStart,
		Now: func() time.Time { return u.now }})
	if second.Err != nil || !second.Ran || !second.Record.Fresh.Qualifies(afterStart, "") || second.Record.Fresh.Seq != 2 {
		t.Fatalf("post-start request = %+v, want a qualifying follow-up check", second)
	}

	// A concurrent request registered before the first check started can reuse that checked winner;
	// it neither checks again nor requires the record's wall clock to be recent.
	broken := &Store{Dir: u.store.Dir, Git: "/nonexistent/git", Getenv: noStagedTree}
	shared := broken.CheckPatched(w, CheckOptions{FreshAfter: &before,
		Now: func() time.Time { return u.now }})
	if shared.Err != nil || shared.Ran || !shared.Record.Fresh.Qualifies(before, "") || shared.Record.Fresh.Seq != 2 {
		t.Fatalf("pre-start concurrent request = %+v, want reuse of a post-boundary check", shared)
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
	if len(f.List) != 0 || !f.BaseOnBranch || f.Problem != "" || f.NoVersion != "" {
		t.Errorf("check = %+v, want an empty list with the base on the branch, and no note (PF-D60)", f)
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

// A GIT SOURCE HAS NO THROTTLE (LPC-D6): a check ten minutes after the last runs, whatever the old hour
// said, and an explicit act still runs too. CheckDue remains for the status lines and the background
// advance's schedule: an edit to what a check reads is due at once, and the interval is only displayed.
func TestAGitSourceIsCheckedAtEveryLaunch(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	u.now = u.now.Add(10 * time.Minute)
	begun := false
	res := u.store.CheckPatched(w, CheckOptions{Now: func() time.Time { return u.now },
		Begin: func() (func(string), func()) { begun = true; return nil, func() {} }})
	if !res.Ran || !begun || res.Err != nil || res.Record == nil || res.Record.Seq != 2 {
		t.Errorf("a check inside the old interval = %+v (begun %v), want it run", res, begun)
	}
	// An edited follow is due at once.
	w2 := w
	w2.Follow = "head"
	if due, why := CheckDue(res.Record, mustInputs(t, w2), u.now, 0); !due || !strings.Contains(why, "changed") {
		t.Errorf("an edited follow is due=%v (%q), want due at once", due, why)
	}
	// An explicit act checks too.
	if res := u.check(t, w, true); !res.Ran || res.Record.Seq != 3 {
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

// THE STAMP IS THE LAST ATTEMPT: a check whose fetch failed writes its stamp and names the failure, and
// the next launch checks again (LPC-D6), rather than waiting out an hour.
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
	if res.Record.CheckedAt != u.now.Add(-10*time.Minute).Unix() {
		t.Errorf("the stamp = %d, want the failed attempt's", res.Record.CheckedAt)
	}
	if again := u.check(t, w, false); !again.Ran {
		t.Error("the check after a failed one did not run: a failed attempt must not throttle the next launch")
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

// THE LOSER OF A CHECK RACE PROBES AGAIN (LPC-D6, §5): a check that waited for the record lock re-reads
// the record the other check wrote and, with no throttle, runs its own (a qualifying FreshAfter check is the
// only thing it reuses, TestFreshCheckBoundaryExcludesInFlightAndSharesFollowup).
func TestACheckThatWaitedProbesAfterTheOthers(t *testing.T) {
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
	if !res.Ran || res.Record == nil || res.Record.Seq != 8 {
		t.Errorf("the waiter = %+v, want its own check after the other's (seq 8)", res)
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

func TestFreshBoundaryRegistersDuringTransportThenSharesAFollowup(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	git, started, release := blockingGit(t)
	u.store.Git = git
	defer release()
	beforeStart, err := u.store.CheckBoundary(w.Owner)
	if err != nil || beforeStart.Seq != 0 {
		t.Fatalf("pre-STARTED request: %+v, %v", beforeStart, err)
	}
	firstDone := make(chan CheckResult, 1)
	go func() {
		firstDone <- u.store.CheckPatched(w, CheckOptions{FreshAfter: &beforeStart, Now: func() time.Time { return u.now }})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !started() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !started() {
		t.Fatal("the first check did not reach its paused git command")
	}
	startedRecord, err := u.store.CheckBoundary(w.Owner)
	if err != nil || startedRecord.Seq != 1 {
		t.Fatalf("paused check did not persist its start before transport: record=%+v err=%v", startedRecord, err)
	}

	boundaryDone := make(chan struct {
		seq CheckStart
		err error
	}, 1)
	go func() {
		seq, err := u.store.CheckBoundary(w.Owner)
		boundaryDone <- struct {
			seq CheckStart
			err error
		}{seq, err}
	}()
	var boundary struct {
		seq CheckStart
		err error
	}
	select {
	case boundary = <-boundaryDone:
	case <-time.After(5 * time.Second):
		t.Fatal("registration blocked on paused transport")
	}
	secondBoundary, err := u.store.CheckBoundary(w.Owner)
	if err != nil || secondBoundary != boundary.seq {
		t.Fatalf("concurrent boundary = %+v, %v", secondBoundary, err)
	}
	release()
	first := <-firstDone
	if first.Err != nil || !first.Ran || !first.Record.Fresh.Qualifies(beforeStart, "") {
		t.Fatalf("first check: %+v", first)
	}
	if boundary.err != nil || boundary.seq.Seq != 1 {
		t.Fatalf("request boundary = %+v, %v; want the earlier check's start sequence 1", boundary.seq, boundary.err)
	}
	followup := u.store.CheckPatched(w, CheckOptions{FreshAfter: &boundary.seq,
		Now: func() time.Time { return u.now }})
	if followup.Err != nil || !followup.Ran || !followup.Record.Fresh.Qualifies(boundary.seq, "") || followup.Record.Fresh.Seq != 2 {
		t.Fatalf("post-registration follow-up = %+v, want a new start after boundary 1", followup)
	}
	shared := u.store.CheckPatched(w, CheckOptions{FreshAfter: &secondBoundary, Now: func() time.Time { return u.now.Add(-time.Hour) }})
	if shared.Err != nil || shared.Ran || !shared.Record.Fresh.Qualifies(secondBoundary, "") {
		t.Fatalf("shared followup after clock rollback: %+v", shared)
	}
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

// A BRANCH WITH NO VERSION TAG AT ALL IS AN EMPTY LIST under a release rule (PF-D60), as one whose
// versions all predate the series' base is, so a first advance builds the base: no problem, and a
// note saying why, which a reader completes with where the fork stays and which names
// `follow: "head"`. `follow: "head"` lists the tip and carries no note. A release:<prefix> rule over
// tags of another name is the same.
func TestABranchWithNoVersionTagIsAnEmptyListThatSaysWhy(t *testing.T) {
	u := newPatchedUpstream(t)
	gitIn(t, u.repo, "tag", "-d", "v1.0.0")
	u.release(t, "nightly", map[int]string{14: "fourteen"})
	tip := u.release(t, "", map[int]string{14: "fourteen", 20: "twenty"})
	res := u.check(t, u.want(t, "main", ""), true)
	f := res.Record.Check
	if len(f.List) != 0 || f.Problem != "" || !f.BaseOnBranch ||
		!strings.Contains(f.NoVersion, "carries no version tag that `follow: \"release\"` reads") {
		t.Errorf("a tagless branch under release = %+v, want an empty list with the note, the base on it", f)
	}
	line := f.NoVersionLine("the series' base 1234abcd")
	for _, w := range []string{f.NoVersion + ", so what runs stays at the series' base 1234abcd until a tag appears or " +
		"`follow` changes", "`follow: \"head\"` follows the branch's commits"} {
		if !strings.Contains(line, w) {
			t.Errorf("the note's line lacks %q:\n%s", w, line)
		}
	}
	res = u.check(t, u.want(t, "main", "head"), true)
	if got := listLabels(res.Record.Check.List); got != shortCommit(tip)+"*" || res.Record.Check.Problem != "" ||
		res.Record.Check.NoVersion != "" {
		t.Errorf("under head the tagless branch lists %q (%+v), want its tip and no note", got, res.Record.Check)
	}
	u.release(t, "v2.0.0", map[int]string{14: "fourteen", 20: "twenty", 21: "x"})
	res = u.check(t, u.want(t, "main", "release:agent@"), true)
	if f := res.Record.Check; f.Problem != "" || len(f.List) != 0 ||
		!strings.Contains(f.NoVersion, "`follow: \"release:agent@\"` reads (a tag named `agent@`") {
		t.Errorf("release:agent@ over v-tags = %+v, want the note naming the prefix", f)
	}
	// THE FIRST VERSION TAG ENDS THE NOTE: the release rule lists it.
	res = u.check(t, u.want(t, "main", ""), true)
	if f := res.Record.Check; f.Problem != "" || f.NoVersion != "" || listLabels(f.List) != "v2.0.0" {
		t.Errorf("once v2.0.0 is tagged the check = %+v, want it listed and no note", f)
	}
}
