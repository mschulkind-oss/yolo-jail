package callbroker

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestSelectedCLITwoWriteAllowOnceBindsFullOrderedBundle(t *testing.T) {
	f := selectedOnceFixture(t)
	v, _, err := f.call(t, "ask", "update", "a1", "a2")
	if err != nil || v.Verdict != "asks" {
		t.Fatalf("two-write ask: %+v %v", v, err)
	}
	original := f.pending(t)[0]
	if len(original.CallWrites) != 2 || original.CallWrites[0].Chain[0].ID != "a1" || original.CallWrites[1].Chain[0].ID != "a2" {
		t.Fatalf("missing ordered complete bundle: %+v", original.CallWrites)
	}
	if _, err := f.store.Decide(original.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	changed, _, err := f.call(t, "act", "update", "a2", "a1")
	if err != nil || changed.Verdict != "asks" || f.effects(t) != 0 {
		t.Fatalf("reordered writes consumed grant: %+v %v", changed, err)
	}
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
		t.Fatalf("reordered writes spent grant: %+v", snap.Grants)
	}
	exact, _, err := f.call(t, "act", "update", "a1", "a2")
	if err != nil || exact.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("original bundle did not run exactly once: %+v %v", exact, err)
	}
}
