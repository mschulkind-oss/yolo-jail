package brokerstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func pr(ws string, n string) WorkingEntry {
	return WorkingEntry{Workspace: ws, Repo: "github.com/o/r", Kind: WorkingPR, Resource: "#" + n, By: "create-pr"}
}

// TestWorkingGitHubKindsLastingAndTimed pins which GitHub kinds may join the working set for
// good: a PR or an issue may, and a merge, a creation or a repository-level write needs an
// expiry; an unknown kind is refused.
func TestWorkingGitHubKindsLastingAndTimed(t *testing.T) {
	s, now := workingFixture(t)
	for _, k := range []string{WorkingPR, WorkingIssue} {
		if _, added, err := s.AddWorking(WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: k, Resource: "#" + k, By: "human"}); err != nil || !added {
			t.Fatalf("lasting %s: %v %v", k, added, err)
		}
	}
	for _, k := range []string{WorkingMerge, WorkingCreatePR, WorkingCreateIssue, WorkingRepo} {
		e := WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: k, Resource: "#9", By: "human"}
		if _, _, err := s.AddWorking(e); err == nil {
			t.Errorf("a lasting %s entry was recorded", k)
		}
		e.Expires = now.Add(15 * time.Minute)
		if _, added, err := s.AddWorking(e); err != nil || !added {
			t.Errorf("timed %s: %v %v", k, added, err)
		}
	}
	if _, _, err := s.AddWorking(WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: "deploy", Resource: "#1", By: "human"}); err == nil {
		t.Error("an unknown kind was recorded")
	}
}

// TestWorkingUpsertKeepsKindsApart pins that a timed merge grant on a PR is its own entry, not
// the PR's lasting one returned back: the same resource under two kinds is two records.
func TestWorkingUpsertKeepsKindsApart(t *testing.T) {
	s, now := workingFixture(t)
	p, _, err := s.AddWorking(pr("/ws", "32"))
	if err != nil {
		t.Fatal(err)
	}
	m, added, err := s.AddWorking(WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: WorkingMerge, Resource: "#32",
		By: "human", Expires: now.Add(time.Hour)})
	if err != nil || !added || m.ID == p.ID {
		t.Fatalf("merge entry %+v added %v %v (want a new entry beside %s)", m, added, err, p.ID)
	}
	got, ok, err := s.LiveWorkingKind("/ws", "github.com/o/r", "#32", WorkingMerge)
	if err != nil || !ok || got.ID != m.ID {
		t.Fatalf("live merge %+v %v %v", got, ok, err)
	}
	got, ok, err = s.LiveWorkingKind("/ws", "github.com/o/r", "#32", WorkingPR, WorkingIssue)
	if err != nil || !ok || got.ID != p.ID {
		t.Fatalf("live PR %+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.LiveWorkingKind("/ws", "github.com/o/r", "#33", WorkingPR); ok {
		t.Fatal("another number was covered")
	}
	*now = now.Add(2 * time.Hour)
	if _, ok, _ := s.LiveWorkingKind("/ws", "github.com/o/r", "#32", WorkingMerge); ok {
		t.Fatal("an expired merge grant still covered")
	}
}

// TestDispatchWorkingGatesAndRevokes pins the one-transaction write: no live entry runs
// nothing, a refusing gate runs nothing, and a revoked entry no longer covers.
func TestDispatchWorkingGatesAndRevokes(t *testing.T) {
	s, _ := workingFixture(t)
	ctx := context.Background()
	ran := 0
	dispatch := func(WorkingEntry) error { ran++; return nil }
	ok := func(WorkingEntry) error { return nil }
	if _, found, err := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingPR}, ok, dispatch); err != nil || found || ran != 0 {
		t.Fatalf("no entry: found %v err %v ran %d", found, err, ran)
	}
	e, _, err := s.AddWorking(pr("/ws", "5"))
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("authority withdrawn")
	if _, _, err := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingPR},
		func(WorkingEntry) error { return denied }, dispatch); !errors.Is(err, denied) || ran != 0 {
		t.Fatalf("refusing gate: err %v ran %d", err, ran)
	}
	got, found, err := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingPR}, ok, dispatch)
	if err != nil || !found || got.ID != e.ID || ran != 1 {
		t.Fatalf("covered: %+v found %v err %v ran %d", got, found, err, ran)
	}
	if _, _, err := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingIssue}, ok, dispatch); err != nil || ran != 1 {
		t.Fatalf("another kind covered: err %v ran %d", err, ran)
	}
	if _, err := s.RevokeIn(e.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingPR}, ok, dispatch); found || ran != 1 {
		t.Fatalf("revoked entry covered: found %v ran %d", found, ran)
	}
	if _, _, err := s.DispatchWorkingContext(ctx, "/ws", "github.com/o/r", "#5", []string{WorkingPR}, nil, dispatch); err == nil {
		t.Fatal("a write with no gate was accepted")
	}
}

// TestEndWorkingResourceEndsTheMergedPR pins that a merge ends the PR's lasting entry and its
// timed grants of the named kinds, and leaves other resources and workspaces.
func TestEndWorkingResourceEndsTheMergedPR(t *testing.T) {
	s, now := workingFixture(t)
	for _, e := range []WorkingEntry{pr("/ws", "7"), pr("/ws", "8"), pr("/other", "7"),
		{Workspace: "/ws", Repo: "github.com/o/r", Kind: WorkingMerge, Resource: "#7", By: "human", Expires: now.Add(time.Hour)}} {
		if _, _, err := s.AddWorking(e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.EndWorkingResource("/ws", "github.com/o/r", "#7", EndMerged, WorkingPR, WorkingIssue, WorkingMerge)
	if err != nil || n != 2 {
		t.Fatalf("ended %d %v (want the PR and its merge grant)", n, err)
	}
	all, _ := s.Working()
	for _, w := range all {
		ended := !w.Live()
		want := w.Workspace == "/ws" && w.Resource == "#7"
		if ended != want {
			t.Errorf("entry %+v ended %v, want %v", w, ended, want)
		}
		if ended && w.EndReason != EndMerged {
			t.Errorf("entry %+v ended as %q", w, w.EndReason)
		}
	}
	if _, err := s.EndWorkingResource("/ws", "github.com/o/r", "#8", "closed", WorkingPR); err == nil {
		t.Error("an unknown end reason was accepted")
	}
}
