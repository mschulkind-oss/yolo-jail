package callbroker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// approvalSide is the store `yolo approve` builds for a selected call_broker pack
// (approvalStores in internal/cli): the same records, the live core revalidator, one entry per
// write, and the smallest max_for as the timed ceiling.
func (f *onceFixture) approvalSide() *brokerstore.Store {
	return &brokerstore.Store{Dir: f.store.Dir, Source: f.store.Source, PublishRecords: true, Audit: f.store.Audit,
		Revalidate: ApprovalAuthority, Entries: RequestEntries, TimedLimit: ApprovalTimedLimit}
}

func TestSelectedCLITimedAnswerIsCappedBySmallestMaxForAndCoversEveryWrite(t *testing.T) {
	f := selectedTimedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1", "r1"); err != nil || v.Verdict != "asks" ||
		!strings.Contains(v.Next, "--for 15m") {
		t.Fatalf("ask: %v %+v", err, v)
	}
	pending := f.pending(t)
	if len(pending) != 1 || len(pending[0].CallWrites) != 2 {
		t.Fatalf("pending = %+v", pending)
	}
	id := pending[0].ID
	approve := f.approvalSide()

	// change allows 8h and rename 1h: the call is capped at 1h.
	_, err := approve.DecideWorking(id, brokerstore.Decision{For: 2 * time.Hour})
	if !errors.Is(err, brokerstore.ErrRefusedByPolicy) || !strings.Contains(err.Error(), "--for 1h") {
		t.Fatalf("2h answer over the smallest max_for: %v", err)
	}
	if got := f.pending(t); len(got) != 1 || got[0].ID != id {
		t.Fatalf("a refused answer changed the request: %+v", got)
	}
	if _, err := approve.DecideWorking(id, brokerstore.Decision{Add: true}); err == nil {
		t.Fatal("a pack request took a permanent working-set answer")
	}
	if _, err := approve.DecideWorking(id, brokerstore.Decision{Session: true}); err == nil {
		t.Fatal("a pack request took a session answer")
	}
	entries, err := approve.DecideWorking(id, brokerstore.Decision{For: time.Hour})
	if err != nil || len(entries) != 2 {
		t.Fatalf("1h answer: %v %+v", err, entries)
	}
	for _, e := range entries {
		if !brokerstore.IsCallKind(e.Kind) || !e.Timed() {
			t.Fatalf("entry %+v is not a timed call entry", e)
		}
	}

	// The answer covers the call for its window, not once.
	for i := 0; i < 2; i++ {
		if v, _, err := f.call(t, "act", "update", "a1", "r1"); err != nil || v.Verdict != "runs" {
			t.Fatalf("act %d under the timed answer: %v %+v", i, err, v)
		}
	}
	if f.effects(t) != 2 {
		t.Fatalf("effects = %d, want 2", f.effects(t))
	}
	// Each write is covered on its own: a subset runs, a resource outside the answer asks.
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "runs" {
		t.Fatalf("subset: %v %+v", err, v)
	}
	if v, _, err := f.call(t, "act", "update", "a2"); err != nil || v.Verdict != "asks" {
		t.Fatalf("an unanswered resource ran: %v %+v", err, v)
	}
	if f.effects(t) != 3 {
		t.Fatalf("effects = %d, want 3", f.effects(t))
	}
}

func TestTimedCallEntryNeverOverridesARefusingTable(t *testing.T) {
	f := selectedTimedOnceFixture(t)
	call := Classification{Writes: []Write{{Cell: "rename", Chain: []Resource{{Type: "task", ID: "r1"}}}}}
	key := brokerstore.CallResourceKey([]brokerstore.CallResource{{Type: "task", ID: "r1"}})
	now := time.Now()
	entry := brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingCallPrefix + "rename",
		Resource: key, By: "human", Expires: now.Add(time.Hour)}
	cells := f.h.input.EffectiveCells
	if v := decideCall(call, cells, []brokerstore.WorkingEntry{entry}, f.home, f.h.input.Source, now); v.Verdict != "runs" {
		t.Fatalf("timed entry did not answer the ask: %+v", v)
	}
	refusing := map[string]config.CallBrokerCellPolicy{}
	for name, p := range cells {
		refusing[name] = p
	}
	p := refusing["rename"]
	setting := p.Tables["all"]
	setting.Decision = "refuse"
	p.Tables = map[string]config.CallBrokerTablePolicy{"all": setting}
	refusing["rename"] = p
	if v := decideCall(call, refusing, []brokerstore.WorkingEntry{entry}, f.home, f.h.input.Source, now); v.Verdict != "refused" {
		t.Fatalf("timed entry overrode a refusing table: %+v", v)
	}
	expired := entry
	expired.Expires = now.Add(-time.Second)
	if v := decideCall(call, cells, []brokerstore.WorkingEntry{expired}, f.home, f.h.input.Source, now); v.Verdict != "asks" {
		t.Fatalf("expired timed entry still covered: %+v", v)
	}
}
