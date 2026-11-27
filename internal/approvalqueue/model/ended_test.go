package model

import (
	"strings"
	"testing"
)

// endedState is an --ended snapshot (BB-D113): one pending request beside a denied one (with a
// note and a secret-bearing argv), an ended and a live grant, an ended and a live working entry.
func endedState() StateView {
	denied := secretReq("r-old", "denied")
	d := at(30)
	denied.Decided, denied.DenialNote = &d, "not this one"
	gone := grant("g-gone", "r-old")
	gone.Workspace = "/home/example/code/app"
	e := at(40)
	gone.State, gone.Ended, gone.EndReason = "ended", &e, "used"
	wend := WorkingView{Schema: 1, ID: "w-1", Source: "git", Workspace: "/ws", Repo: "o/r", Kind: "branch", Resource: "refs/heads/x", By: "human", Ended: &e, EndReason: "revoked", State: "ended"}
	wlive := WorkingView{Schema: 1, ID: "w-2", Source: "git", Workspace: "/ws", Repo: "o/r", Kind: "branch", Resource: "refs/heads/y", By: "human", State: "live"}
	st := emptyState()
	st.IncludesEnded, st.HorizonDays = true, 7
	st.HorizonNote = "Ended records are retained for 7 days, except that a record a live grant or working-set entry references can be kept longer."
	st.Requests = []RequestView{req("r-pend", "pending"), denied}
	st.Grants = []GrantView{grant("g-live", "r-pend"), gone}
	st.Working = []WorkingView{wlive, wend}
	return st
}

func TestEndedSnapshotSplitsLiveFromHistoryAndMasksLikePending(t *testing.T) {
	m := Reduce(New(t0), SnapshotOK{State: endedState(), FetchStart: at(100)}, at(100))
	if len(m.State.Requests) != 1 || m.State.Requests[0].ID != "r-pend" || len(m.State.Grants) != 1 || m.State.Grants[0].ID != "g-live" || len(m.State.Working) != 1 || m.State.Working[0].ID != "w-2" {
		t.Fatalf("ended records leaked into the live state: %+v", m.State)
	}
	p := Publish(m, at(101), Options{})
	e := historyOut(t, p, "r-old")
	assertMaskedDetail(t, e, "denied")
	if !strings.Contains(e.Outcome, "denied") || !strings.Contains(e.Outcome, "not this one") {
		t.Fatalf("outcome %q should carry the answer and note", e.Outcome)
	}
	if g := historyOut(t, p, "g-gone"); g.Workspace == "" || !strings.Contains(g.Outcome, "used") {
		t.Fatalf("ended grant entry: %+v", g)
	}
	if w := historyOut(t, p, "w-1"); w.Kind != "working" || w.Workspace != "/ws" || !strings.Contains(w.Outcome, "revoked") {
		t.Fatalf("ended working entry: %+v", w)
	}
	for _, k := range []string{"r-pend", "g-live", "w-2"} {
		for _, h := range p.History.Entries {
			if h.Key == k {
				t.Fatalf("live record %s is in history", k)
			}
		}
	}
	if !strings.Contains(p.History.Note, "7 days") || !strings.Contains(p.History.Note, "not a complete history") {
		t.Fatalf("note must state the horizon and never claim completeness: %q", p.History.Note)
	}
}

func TestStoredOutcomeUpgradesANotObservedEntry(t *testing.T) {
	m := snap(New(t0), at(10), []RequestView{req("r-old", "pending")}, nil)
	m = Reduce(m, SnapshotOK{State: endedState(), FetchStart: at(100)}, at(100))
	if e := historyOut(t, Publish(m, at(101), Options{}), "r-old"); !strings.Contains(e.Outcome, "denied") {
		t.Fatalf("stored outcome should replace not-observed: %+v", e)
	}
}

func TestPlainSnapshotKeepsStoredHistory(t *testing.T) {
	m := Reduce(New(t0), SnapshotOK{State: endedState(), FetchStart: at(100)}, at(100))
	m = snap(m, at(110), nil, nil)
	historyOut(t, Publish(m, at(111), Options{}), "r-old")
}
