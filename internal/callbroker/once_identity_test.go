package callbroker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestSelectedCLIExactOnceRejectsForgedCanonicalWritesAndOriginalInput(t *testing.T) {
	f := selectedOnceFixture(t)
	v, _, err := f.call(t, "ask", "update", "a1")
	if err != nil || v.Verdict != "asks" {
		t.Fatalf("ask %+v %v", v, err)
	}
	original := f.pending(t)[0]
	if _, err := f.store.Decide(original.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*brokerstore.Request)
	}{
		{"wrong original argv", func(q *brokerstore.Request) { q.OriginalArgv = []string{"read"} }},
		{"wrong stdin digest", func(q *brokerstore.Request) { q.StdinDigest = strings.Repeat("a", 64) }},
		{"wrong cell", func(q *brokerstore.Request) { q.CallWrites[0].Cell = "blocked" }},
		{"wrong root", func(q *brokerstore.Request) { q.CallWrites[0].Chain[1].ID = "team:beta" }},
		{"wrong parent order", func(q *brokerstore.Request) {
			q.CallWrites[0].Chain[0], q.CallWrites[0].Chain[1] = q.CallWrites[0].Chain[1], q.CallWrites[0].Chain[0]
		}},
		{"wrong holder", func(q *brokerstore.Request) { q.Jail = "forged-jail" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var q brokerstore.Request
			if err := json.Unmarshal(data, &q); err != nil {
				t.Fatal(err)
			}
			tc.change(&q)
			q.Digest = brokerstore.CommandDigest(q)
			if tc.name == "wrong holder" {
				answer, err := f.h.classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, nil, f.h.lookupWithClassifier(f.h.input.FrontJailID, f.h.classifier))
				if err != nil || answer.Classification == nil {
					t.Fatalf("fixture classification: %v", err)
				}
				v, _, err := f.h.boundWrite(context.Background(), "act", q, *answer.Classification, Verdict{Verdict: "asks"}, nil)
				if err == nil || v.Verdict != "refused" {
					t.Fatal("wrong authenticated holder passed transaction gate")
				}
			} else {
				_, spent, filed, err := f.store.SpendExactOrFileContext(context.Background(), q, f.h.revalidate, func() error {
					t.Error("forged call dispatched")
					return nil
				})
				if tc.name == "wrong stdin digest" {
					// Different original stdin has a different exact request. The
					// classifier sees only the hash; the old grant cannot cover it.
					if err != nil || spent || filed.ID == "" {
						t.Fatalf("changed stdin did not ask again: filed=%+v spent=%t err=%v", filed, spent, err)
					}
				} else if err == nil || spent {
					t.Fatalf("forged call was accepted: spent=%t err=%v", spent, err)
				}
			}
			if f.effects(t) != 0 {
				t.Fatal("forged call had a host effect")
			}
			snap, err := f.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
				t.Fatalf("forged call spent once grant: %+v", snap.Grants)
			}
		})
	}
}

func TestSelectedCLIExactOnceRefusesWholeCallWhenSecondWriteIsFalse(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask %+v %v", v, err)
	}
	if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "update", "a1", "b1")
	if err != nil || v.Verdict != "refused" || f.effects(t) != 0 {
		t.Fatalf("second refused write had effect: %+v %v", v, err)
	}
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
		t.Fatalf("false second write spent grant: %+v", snap.Grants)
	}
}
