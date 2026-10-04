package packsrc

// heldat_test.go pins CheckRecord.HeldAt (patchcheck.go; docs/design/patched-forks.md §8's held
// suffix): what holds the newest candidate back, read offline — the first entry the walk reaches
// that a conflict or a failed build of the series as it stands stops, the last check's problem, or
// nothing when the first entry may still fit.

import "testing"

func TestHeldAtNamesWhatHoldsTheNewestCandidate(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "main", Follow: "release", Base: "b"}
	v12, v11 := ListEntry{Commit: "c12", Tag: "v1.2.0", Version: "1.2.0"}, ListEntry{Commit: "c11", Tag: "v1.1.0", Version: "1.1.0"}
	rec := func(outcomes ...EntryOutcome) *CheckRecord {
		return &CheckRecord{Read: in, Check: &CheckFound{RefKind: "branch", List: []ListEntry{v12, v11}},
			Good: &GoodBuild{Commit: "c10", Version: "1.0.0"}, Outcomes: outcomes}
	}
	if h := rec().HeldAt(in, "s", "r"); h != nil {
		t.Errorf("with nothing recorded HeldAt = %+v, want nil", h)
	}
	h := rec(EntryOutcome{Commit: "c12", Kind: OutcomeConflict, Series: "s", Member: "0001.patch"}).HeldAt(in, "s", "r")
	if h == nil || h.Kind != OutcomeConflict || h.Entry.Commit != "c12" || h.Member != "0001.patch" {
		t.Errorf("a conflict at the newest = %+v", h)
	}
	if h := rec(EntryOutcome{Commit: "c12", Kind: OutcomeConflict, Series: "other"}).HeldAt(in, "s", "r"); h != nil {
		t.Errorf("another series' conflict holds the series as it stands: %+v", h)
	}
	h = rec(EntryOutcome{Commit: "c12", Kind: OutcomeBuildFailed, Series: "s", Recipe: "r", Error: "boom"}).HeldAt(in, "s", "r")
	if h == nil || h.Kind != OutcomeBuildFailed || h.Error != "boom" {
		t.Errorf("a failed build at the newest = %+v", h)
	}
	if h := rec(EntryOutcome{Commit: "c11", Kind: OutcomeConflict, Series: "s"}).HeldAt(in, "s", "r"); h != nil {
		t.Errorf("a conflict below an entry that may still fit holds it: %+v", h)
	}
	r := rec()
	r.Check.Problem = "?ref=HEAD names neither"
	if h := r.HeldAt(in, "s", "r"); h == nil || h.Kind != HeldByProblem {
		t.Errorf("the check's problem = %+v", h)
	}
	if h := r.HeldAt(CheckInputs{Repo: "r", Ref: "dev"}, "s", "r"); h != nil {
		t.Errorf("a record of another rule's check holds this one: %+v", h)
	}
}

// AN APPLY ERROR HOLDS THE FIRST ENTRY THE WALK WOULD REACH (PF-D45) while the record's last walk of
// this check's list ended in one, and nothing once a later check runs: that check retries it.
func TestHeldAtNamesAnApplyErrorOfTheLastCheck(t *testing.T) {
	in := CheckInputs{Repo: "r", Ref: "main", Follow: "release", Base: "b"}
	v12, v11 := ListEntry{Commit: "c12", Tag: "v1.2.0", Version: "1.2.0"}, ListEntry{Commit: "c11", Tag: "v1.1.0", Version: "1.1.0"}
	rec := &CheckRecord{Read: in, Seq: 4, Check: &CheckFound{Seq: 4, RefKind: "branch", List: []ListEntry{v12, v11}},
		Good: &GoodBuild{Commit: "c10", Version: "1.0.0"}, ApplyErr: &ApplyError{Seq: 4, Error: "git timed out"},
		Outcomes: []EntryOutcome{{Commit: "c12", Kind: OutcomeConflict, Series: "other"}}}
	h := rec.HeldAt(in, "s", "r")
	if h == nil || h.Kind != HeldByApplyError || h.Entry.Commit != "c12" || h.Error != "git timed out" {
		t.Errorf("an apply error of the last check = %+v", h)
	}
	rec.Seq, rec.Check.Seq = 5, 5
	if h := rec.HeldAt(in, "s", "r"); h != nil {
		t.Errorf("an apply error of an earlier check holds the next one: %+v", h)
	}
	if rec.ApplyErrAtLastCheck() != nil {
		t.Error("ApplyErrAtLastCheck answers for an earlier check")
	}
}

// A WALK WITH NONE OF ITS BOUND LEFT (WalkOptions.Spent, PF-D44) is an apply error at once, naming the
// whole bound, and runs no git.
func TestAWalkWithNoneOfItsBoundLeftIsAnApplyError(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Git: "/nonexistent/git-must-not-run"}
	w := s.WalkSeries("file:///r", "", &Series{}, []ListEntry{{Commit: "c"}}, WalkOptions{Spent: ReplayTimeout})
	if w.Err == nil || w.Err.Error() != "the series' replay ran out of its "+ReplayTimeout.String() || len(w.Results) != 0 {
		t.Errorf("a walk with nothing left = %+v", w)
	}
}
