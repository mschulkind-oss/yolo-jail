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
