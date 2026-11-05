package callbroker

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestSelectedCLIActUsesLateStandingCoverageWithoutSpendingExactGrant(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	// This fixture models a separate, already authorized working-set source; it
	// does not activate --for or --add on the pack approval CLI.
	if v, _, err := f.call(t, "ask", "update-pr"); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask %+v %v", v, err)
	}
	id := f.pending(t)[0].ID
	if _, err := f.store.Decide(id, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "update-pr")
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("covered act: %+v %v effects=%d", v, err, f.effects(t))
	}
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
		t.Fatalf("standing coverage spent grant: %+v", snap.Grants)
	}
}
