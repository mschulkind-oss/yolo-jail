package callbroker

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// scopedFixture gives the selected-launch fixture a scoped decision leaf: a read
// ask and ceiling, and the same ask and ceiling for the change cell on both of
// its tables. Nothing in a launch publishes scoped asks yet, so livePolicy is the
// seam a test substitutes; everything after it is the production transaction.
func scopedFixture(t *testing.T, readAsk, readCeiling, changeAsk, changeCeiling brokerpolicy.Value) *onceFixture {
	t.Helper()
	f := selectedOnceFixtureWithStanding(t, true)
	source := f.h.input.Source
	matcher, err := brokerpolicy.NewScopeMatcher("exact", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cells := []brokerpolicy.Cell{
		{Name: "change", Kind: brokerpolicy.Write, Tables: []string{"working_set", "all"}},
		{Name: "blocked", Kind: brokerpolicy.Write, Tables: []string{"all"}},
	}
	var ceilings, asks []brokerpolicy.Rule
	for _, table := range []string{"working_set", "all"} {
		asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: "change", Table: table, Value: changeAsk, Origin: "fixture change ask"})
		ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: "change", Table: table, Value: changeCeiling, Origin: "fixture change ceiling"})
	}
	asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Value: readAsk, Origin: "fixture read ask"})
	ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Value: readCeiling, Origin: "fixture read ceiling"})
	policy, err := brokerpolicy.NewPolicy(source, matcher, cells, true, false, ceilings, asks, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.livePolicy = func() (brokerpolicy.Policy, error) { return policy, nil }
	f.store.Entries = RequestEntries
	return f
}

// mixedClassifier makes the handler's classifier answer every call with one read
// and one write of the same canonical argv, which the fixture host program runs
// as a write. The read is of project readProject.
func mixedClassifier(t *testing.T, f *onceFixture, readProject string) {
	t.Helper()
	answer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"argv":["update-pr","1"],"stdin_sha256":"%x","reads":[[{"type":"project","id":%q}]],"writes":[{"cell":"change","chain":[{"type":"pr","id":"#1"},{"type":"project","id":"team:alpha"}],"description":"update test PR"}]}}`, sha256.Sum256(nil), readProject)
	f.h.classifier.Identities = fixtureClassifier(t, "cat >/dev/null; printf '%s' '"+answer+"'").Identities
}

func TestScopedOperationApprovals(t *testing.T) {
	const (
		ask    = brokerpolicy.Ask
		onReq  = brokerpolicy.OnRequest
		always = brokerpolicy.Always
		yes    = brokerpolicy.True
	)
	t.Run("C1", func(t *testing.T) {
		f := scopedFixture(t, ask, onReq, ask, onReq)
		if v, _, err := f.call(t, "ask", "read"); err != nil || v.Verdict != "asks" || f.reads(t) != 0 {
			t.Fatalf("read did not ask: %+v %v reads=%d", v, err, f.reads(t))
		}
		pending := f.pending(t)
		if len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 0 || pending[0].CallReads[0].Chain[0].ID != "team:alpha" {
			t.Fatalf("durable subject: %+v", pending)
		}
		if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "read"); err != nil || v.Verdict != "runs" || f.reads(t) != 1 {
			t.Fatalf("answered read: %+v %v reads=%d", v, err, f.reads(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || len(snap.Grants) != 1 || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 {
			t.Fatalf("grant not spent once: %+v %v", snap.Grants, err)
		}
		if v, _, err := f.call(t, "act", "read"); err != nil || v.Verdict != "asks" || f.reads(t) != 1 {
			t.Fatalf("second run did not ask again: %+v %v reads=%d", v, err, f.reads(t))
		}
	})
	t.Run("C2", func(t *testing.T) {
		f := scopedFixture(t, ask, onReq, ask, onReq)
		if v, _, err := f.call(t, "ask", "read"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "read-beta"); err != nil || v.Verdict != "asks" || f.reads(t) != 0 {
			t.Fatalf("a different read ran on another read's answer: %+v %v reads=%d", v, err, f.reads(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || snap.Grants[0].Uses != 1 {
			t.Fatalf("a different read spent the answer: %+v %v", snap.Grants, err)
		}
	})
	t.Run("C3", func(t *testing.T) {
		f := scopedFixture(t, ask, onReq, ask, onReq)
		if v, _, err := f.call(t, "ask", "read"); err != nil || v.Verdict != "asks" || !strings.Contains(v.Next, "--for") {
			t.Fatalf("ask: %+v %v", v, err)
		}
		entries, err := f.store.DecideWorking(f.pending(t)[0].ID, brokerstore.Decision{For: 10 * time.Minute})
		if err != nil || len(entries) != 1 || !entries[0].Timed() {
			t.Fatalf("timed read answer: %+v %v", entries, err)
		}
		for i := 0; i < 2; i++ {
			if v, _, err := f.call(t, "act", "read"); err != nil || v.Verdict != "runs" {
				t.Fatalf("run %d under the timed answer: %+v %v", i, v, err)
			}
		}
		if f.reads(t) != 2 || len(f.pending(t)) != 1 {
			t.Fatalf("reads=%d requests=%d", f.reads(t), len(f.pending(t)))
		}
		if v, _, err := f.call(t, "act", "read-beta"); err != nil || v.Verdict != "asks" || f.reads(t) != 2 {
			t.Fatalf("a neighboring chain ran on a timed answer: %+v %v reads=%d", v, err, f.reads(t))
		}
	})
	t.Run("C4", func(t *testing.T) {
		f := scopedFixture(t, ask, onReq, ask, onReq)
		mixedClassifier(t, f, "team:alpha")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		pending := f.pending(t)
		if len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 1 {
			t.Fatalf("one request must name both: %+v", pending)
		}
		unbound := pending[0]
		unbound.CallReads = nil
		if brokerstore.CommandDigest(unbound) == pending[0].Digest {
			t.Fatal("the digest does not bind the read")
		}
		if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		mixedClassifier(t, f, "team:beta")
		if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "asks" || f.effects(t) != 0 {
			t.Fatalf("a different read ran on this answer: %+v %v effects=%d", v, err, f.effects(t))
		}
		mixedClassifier(t, f, "team:alpha")
		if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
			t.Fatalf("one answer for both: %+v %v effects=%d", v, err, f.effects(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 {
			t.Fatalf("one answer spent more than once: %+v %v", snap.Grants, err)
		}
	})
	t.Run("C5", func(t *testing.T) {
		f := scopedFixture(t, brokerpolicy.False, onReq, ask, onReq)
		mixedClassifier(t, f, "team:alpha")
		v, _, err := f.call(t, "ask", "update-pr", "1")
		if err != nil || v.Verdict != "refused" || len(f.pending(t)) != 0 || f.effects(t) != 0 || f.reads(t) != 0 {
			t.Fatalf("a refused read with an asking write: %+v %v requests=%d", v, err, len(f.pending(t)))
		}
		if !strings.Contains(v.Next, "fixture read ask") {
			t.Fatalf("refusal does not name the governing line: %q", v.Next)
		}
	})
	t.Run("running read is not a subject", func(t *testing.T) {
		f := scopedFixture(t, yes, always, ask, onReq)
		mixedClassifier(t, f, "team:alpha")
		if v, _, err := f.call(t, "ask", "update-pr", "1"); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		if pending := f.pending(t); len(pending) != 1 || len(pending[0].CallReads) != 0 || len(pending[0].CallWrites) != 1 {
			t.Fatalf("a read that runs under standing authority became a subject: %+v", pending)
		}
	})
}

func TestScopedPredictionHasNoEffects(t *testing.T) {
	t.Run("C8", func(t *testing.T) {
		f := scopedFixture(t, brokerpolicy.Ask, brokerpolicy.OnRequest, brokerpolicy.Ask, brokerpolicy.OnRequest)
		mixedClassifier(t, f, "team:alpha")
		for _, argv := range [][]string{{"update-pr", "1"}} {
			if v, _, err := f.call(t, "evaluate", argv...); err != nil || v.Verdict != "asks" {
				t.Fatalf("evaluate: %+v %v", v, err)
			}
		}
		if v, _, err := f.call(t, "evaluate", "read"); err != nil || v.Verdict != "asks" {
			t.Fatalf("evaluate read: %+v %v", v, err)
		}
		if len(f.pending(t)) != 0 || f.effects(t) != 0 || f.reads(t) != 0 {
			t.Fatalf("a prediction filed or ran: requests=%d effects=%d reads=%d", len(f.pending(t)), f.effects(t), f.reads(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || len(snap.Grants) != 0 {
			t.Fatalf("a prediction spent: %+v %v", snap.Grants, err)
		}
	})
}

func TestScopedRefusalHasNoAnonymousFallback(t *testing.T) {
	t.Run("G20", func(t *testing.T) {
		f := scopedFixture(t, brokerpolicy.True, brokerpolicy.Never, brokerpolicy.True, brokerpolicy.Always)
		for _, argv := range [][]string{{"read"}, {"update-pr", "1"}} {
			if argv[0] == "update-pr" {
				mixedClassifier(t, f, "team:alpha")
			}
			v, _, err := f.call(t, "act", argv...)
			if err != nil || v.Verdict != "refused" || !strings.Contains(v.Next, "fixture read ceiling") {
				t.Fatalf("%v: a never read was not refused naming its line: %+v %v", argv, v, err)
			}
		}
		if f.reads(t) != 0 || f.effects(t) != 0 || len(f.pending(t)) != 0 {
			t.Fatalf("a refused read sent or filed: reads=%d effects=%d requests=%d", f.reads(t), f.effects(t), len(f.pending(t)))
		}
	})
}
