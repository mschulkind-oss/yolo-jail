package callbroker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// withheldClassifierScript is a fake pack classifier. A call `update <item>` first asks the
// declared lookup; with the lookup's value it resolves to the cell the item's name says (the
// fake world's "fact": an item named issue-* is the asking kind, anything else the other); with
// the lookup withheld it names both cells as candidates. adv is the label it gives answers to a
// /1 request: /2 advertises that it can take a withheld fact.
func withheldClassifierScript(adv string) string {
	sum := fmt.Sprintf("%x", sha256.Sum256(nil))
	chain := `[{"type":"task","id":"'"$id"'"},{"type":"project","id":"team:alpha"}]`
	return `request=$(cat)
id=$(printf '%s' "$request" | sed -n 's/.*"argv":\["update","\([^"]*\)"\].*/\1/p')
case "$request" in
*'"argv":["lookup",'*)
  printf '{"version":"yolo.call-broker-classifier/` + adv + `","classification":{"argv":["lookup","a1"],"stdin_sha256":"` + sum + `","reads":[[{"type":"project","id":"team:alpha"}]]}}' ;;
*'"status":"withheld"'*)
  printf '{"version":"yolo.call-broker-classifier/2","classification":{"argv":["update","'"$id"'"],"stdin_sha256":"` + sum + `","writes":[{"candidates":["change","rename"],"chain":` + chain + `,"description":"update item"}]}}' ;;
*'"facts":[{'*)
  cell=change
  case "$id" in issue-*) cell=rename ;; esac
  printf '{"version":"yolo.call-broker-classifier/` + adv + `","classification":{"argv":["update","'"$id"'"],"stdin_sha256":"` + sum + `","writes":[{"cell":"'"$cell"'","chain":` + chain + `,"description":"update item"}]}}' ;;
*)
  printf '{"version":"yolo.call-broker-classifier/` + adv + `","lookups":[{"name":"task-project","arguments":["a1"]}]}' ;;
esac`
}

// factFixture is the timed selected-launch fixture with two write cells, change (a) and
// rename (b), a scoped read, and a pack classifier whose lookup can be withheld.
func factFixture(t *testing.T, adv string, readAsk, readCeiling brokerpolicy.Value, change, rename [2]brokerpolicy.Value) *onceFixture {
	t.Helper()
	f := selectedTimedOnceFixture(t)
	source := f.h.input.Source
	matcher, err := brokerpolicy.NewScopeMatcher("exact", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cells := []brokerpolicy.Cell{
		{Name: "change", Kind: brokerpolicy.Write, Tables: []string{"working_set", "all"}},
		{Name: "rename", Kind: brokerpolicy.Write, Tables: []string{"all"}},
		{Name: "blocked", Kind: brokerpolicy.Write, Tables: []string{"all"}},
	}
	var ceilings, asks []brokerpolicy.Rule
	for _, rule := range []struct {
		cell   string
		tables []string
		value  [2]brokerpolicy.Value
	}{{"change", []string{"working_set", "all"}, change}, {"rename", []string{"all"}, rename}} {
		for _, table := range rule.tables {
			asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: rule.cell, Table: table, Value: rule.value[0], Origin: "fixture " + rule.cell + " ask"})
			ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: rule.cell, Table: table, Value: rule.value[1], Origin: "fixture " + rule.cell + " ceiling"})
		}
	}
	asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Value: readAsk, Origin: "fixture read ask"})
	ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Value: readCeiling, Origin: "fixture read ceiling"})
	policy, err := brokerpolicy.NewPolicy(source, matcher, cells, true, false, ceilings, asks, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.livePolicy = func() (brokerpolicy.Policy, error) { return policy, nil }
	f.h.classifier.Identities = fixtureClassifier(t, withheldClassifierScript(adv)).Identities
	f.store.Entries = RequestEntries
	f.store.TimedLimit = ApprovalTimedLimit
	return f
}

func (f *onceFixture) lookups(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "lookup-effects"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "LOOKUP\n")
}

func (f *onceFixture) noProviderSend(t *testing.T) {
	t.Helper()
	if f.lookups(t) != 0 || f.effects(t) != 0 {
		t.Fatalf("a provider call was made: lookups=%d writes=%d", f.lookups(t), f.effects(t))
	}
}

var (
	falseOff   = [2]brokerpolicy.Value{brokerpolicy.False, brokerpolicy.OnRequest}
	askOnReq   = [2]brokerpolicy.Value{brokerpolicy.Ask, brokerpolicy.OnRequest}
	runAlways  = [2]brokerpolicy.Value{brokerpolicy.True, brokerpolicy.Always}
	issue, prr = "issue-70", "pr-9"
)

// answerOnce answers the one pending request Allow once, as the host's yolo approve does.
func answerOnce(t *testing.T, f *onceFixture) brokerstore.Request {
	t.Helper()
	pending := f.pending(t)
	if len(pending) != 1 {
		t.Fatalf("want one pending request: %+v", pending)
	}
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	return pending[0]
}

func TestGenericWithheldFactCandidates(t *testing.T) {
	t.Run("C6", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		v, _, err := f.call(t, "ask", "update", issue)
		if err != nil || v.Verdict != "asks" {
			t.Fatalf("withheld fact did not ask: %+v %v", v, err)
		}
		f.noProviderSend(t)
		pending := f.pending(t)
		if len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 1 {
			t.Fatalf("request: %+v", pending)
		}
		write := pending[0].CallWrites[0]
		if write.Role != brokerstore.RoleWithheldWrite || len(write.Candidates) != 1 || write.Candidates[0] != "rename" {
			t.Fatalf("the request must list the fact read and only the candidates not refused: %+v", write)
		}
		if strings.Contains(fmt.Sprintf("%+v %+v", pending[0], v), `{"project"`) {
			t.Fatal("request text holds provider data")
		}
	})
	t.Run("C7", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		if v, _, err := f.call(t, "ask", "update", issue); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		answerOnce(t, f)
		if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "runs" || f.lookups(t) != 1 || f.effects(t) != 1 {
			t.Fatalf("answered call: %+v %v lookups=%d writes=%d", v, err, f.lookups(t), f.effects(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || snap.Grants[0].Uses != 0 {
			t.Fatalf("one spend expected: %+v %v", snap.Grants, err)
		}
		if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "asks" || f.effects(t) != 1 {
			t.Fatalf("a retry did not ask again: %+v %v", v, err)
		}
	})
	t.Run("C7a", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		if v, _, err := f.call(t, "ask", "update", prr); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		answerOnce(t, f)
		v, _, err := f.call(t, "act", "update", prr)
		if err != nil || v.Verdict != "refused" || f.lookups(t) != 1 || f.effects(t) != 0 || !strings.Contains(v.Next, "fixture change ask") {
			t.Fatalf("the refused cell was not refused naming its line: %+v %v lookups=%d writes=%d", v, err, f.lookups(t), f.effects(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 {
			t.Fatalf("the answer is spent at the fact read and not refunded: %+v %v", snap.Grants, err)
		}
	})
	t.Run("C7b", func(t *testing.T) {
		for _, read := range [][2]brokerpolicy.Value{{brokerpolicy.True, brokerpolicy.Always}, {brokerpolicy.Ask, brokerpolicy.OnRequest}, {brokerpolicy.True, brokerpolicy.Never}} {
			f := factFixture(t, "2", read[0], read[1], falseOff, falseOff)
			v, _, err := f.call(t, "act", "update", issue)
			if err != nil || v.Verdict != "refused" || len(f.pending(t)) != 0 {
				t.Fatalf("read %v: all-refused candidates: %+v %v requests=%d", read, v, err, len(f.pending(t)))
			}
			if read[1] != brokerpolicy.Always {
				f.noProviderSend(t)
			}
		}
	})
	t.Run("C7d", func(t *testing.T) {
		for _, read := range [][2]brokerpolicy.Value{{brokerpolicy.Ask, brokerpolicy.OnRequest}, {brokerpolicy.True, brokerpolicy.Never}} {
			f := factFixture(t, "2", read[0], read[1], runAlways, askOnReq)
			if v, _, err := f.call(t, "ask", "update", issue); err != nil || v.Verdict != "asks" {
				t.Fatalf("read %v: ask: %+v %v", read, v, err)
			}
			f.noProviderSend(t)
			if read[1] == brokerpolicy.Never {
				if reads := f.pending(t)[0].CallReads; len(reads) != 0 {
					t.Fatalf("a refused fact read is a request subject: %+v", reads)
				}
			}
			answerOnce(t, f)
			if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "runs" || f.lookups(t) != 0 || f.effects(t) != 1 {
				t.Fatalf("read %v: bound argv must run with no provider read: %+v %v lookups=%d writes=%d", read, v, err, f.lookups(t), f.effects(t))
			}
		}
	})
	t.Run("C7e", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.True, brokerpolicy.Never, falseOff, askOnReq)
		v, _, err := f.call(t, "act", "update", issue)
		if err != nil || v.Verdict != "refused" || len(f.pending(t)) != 0 {
			t.Fatalf("owner's case: %+v %v requests=%d", v, err, len(f.pending(t)))
		}
		f.noProviderSend(t)
		if !strings.Contains(v.Next, "fixture read ceiling") || !strings.Contains(v.Next, "fixture change ask") {
			t.Fatalf("the refusal must name both lines: %q", v.Next)
		}
	})
	t.Run("C7f", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.True, brokerpolicy.Never, falseOff, askOnReq)
		chain := []brokerstore.CallResource{{Type: "task", ID: issue}, {Type: "project", ID: "team:alpha"}}
		entry := brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingCallPrefix + "rename",
			Resource: brokerstore.CallResourceKey(chain), By: "human", Expires: time.Now().Add(time.Hour)}
		if _, _, err := f.store.AddWorking(entry); err != nil {
			t.Fatal(err)
		}
		if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "refused" || len(f.pending(t)) != 0 {
			t.Fatalf("a live timed entry overrode a refused fact: %+v %v", v, err)
		}
		f.noProviderSend(t)
	})
	t.Run("C7g", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.True, brokerpolicy.Always, falseOff, askOnReq)
		if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "asks" || f.lookups(t) == 0 || f.effects(t) != 0 {
			t.Fatalf("an issue must ask after the fact is read: %+v %v lookups=%d", v, err, f.lookups(t))
		}
		before := f.lookups(t)
		if v, _, err := f.call(t, "act", "update", prr); err != nil || v.Verdict != "refused" || f.lookups(t) == before || f.effects(t) != 0 {
			t.Fatalf("a PR must be refused after the fact is read: %+v %v lookups=%d", v, err, f.lookups(t))
		}
	})
	t.Run("C7h", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		if v, _, err := f.call(t, "ask", "update", issue); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		entries, err := f.store.DecideWorking(f.pending(t)[0].ID, brokerstore.Decision{For: 10 * time.Minute})
		if err != nil || len(entries) != 2 {
			t.Fatalf("timed entries are the read and the candidate not refused: %+v %v", entries, err)
		}
		kinds := map[string]bool{}
		for _, e := range entries {
			kinds[e.Kind] = true
		}
		if !kinds[brokerstore.WorkingCallPrefix+"read"] || !kinds[brokerstore.WorkingCallPrefix+"rename"] {
			t.Fatalf("entries: %+v", entries)
		}
		if v, _, err := f.call(t, "act", "update", issue); err != nil || v.Verdict != "runs" || f.lookups(t) == 0 || f.effects(t) != 1 {
			t.Fatalf("the held call must read the fact and run: %+v %v lookups=%d writes=%d", v, err, f.lookups(t), f.effects(t))
		}
		if v, _, err := f.call(t, "act", "update", prr); err != nil || v.Verdict != "refused" || f.effects(t) != 1 {
			t.Fatalf("a PR is still refused: %+v %v", v, err)
		}
	})
	t.Run("C7n", func(t *testing.T) {
		f := factFixture(t, "1", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		v, _, err := f.call(t, "act", "update", issue)
		if err != nil || v.Verdict != "refused" || !strings.Contains(v.Next, "fixture read ask") || len(f.pending(t)) != 0 {
			t.Fatalf("an old /1 classifier's withheld lookup must refuse naming the read line: %+v %v", v, err)
		}
		f.noProviderSend(t)
	})
	t.Run("H18", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, askOnReq, askOnReq)
		if v, _, err := f.call(t, "ask", "update", issue); err != nil || v.Verdict != "asks" {
			t.Fatalf("ask: %+v %v", v, err)
		}
		id := f.pending(t)[0].ID
		if _, err := f.store.DecideWorking(id, brokerstore.Decision{For: 30 * time.Minute}); err == nil || !strings.Contains(err.Error(), "15m") {
			t.Fatalf("30m must be refused at the 15m read cap: %v", err)
		}
		entries, err := f.store.DecideWorking(id, brokerstore.Decision{For: 10 * time.Minute})
		if err != nil || len(entries) != 3 {
			t.Fatalf("10m: the read and both candidates: %+v %v", entries, err)
		}
		for _, e := range entries {
			if !e.Expires.Equal(entries[0].Expires) {
				t.Fatalf("entries differ in expiry: %+v", entries)
			}
		}
	})
}

// The approval and spend gate replays a withheld request through the classifier and the
// current policy; a request edited in any bound field is not the one that was asked.
func TestScopedWithheldRequestIsReplayedNotTrusted(t *testing.T) {
	f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
	if v, _, err := f.call(t, "ask", "update", issue); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", v, err)
	}
	filed := f.pending(t)[0]
	_, _, working, now, err := f.store.InspectContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.revalidate(filed.WithWorkingState(working, now)); err != nil {
		t.Fatalf("the request as filed was not replayed: %v", err)
	}
	for name, edit := range map[string]func(*brokerstore.Request){
		"candidate widened to a refused cell": func(q *brokerstore.Request) {
			q.CallWrites[0].Candidates = []string{"change", "rename"}
			q.CallWrites[0].Cell = "change"
		},
		"fact read dropped": func(q *brokerstore.Request) { q.CallReads = nil },
		"resource changed":  func(q *brokerstore.Request) { q.CallWrites[0].Chain[0].ID = "issue-71" },
		"role downgraded":   func(q *brokerstore.Request) { q.CallWrites[0].Role, q.CallWrites[0].Candidates = "", nil },
	} {
		edited := filed
		edited.CallWrites = append([]brokerstore.CallWrite(nil), filed.CallWrites...)
		edited.CallWrites[0].Chain = append([]brokerstore.CallResource(nil), filed.CallWrites[0].Chain...)
		edit(&edited)
		edited.Digest = brokerstore.CommandDigest(edited)
		if _, err := f.h.revalidate(edited.WithWorkingState(working, now)); err == nil {
			t.Fatalf("%s: an edited request was accepted", name)
		}
	}
}
