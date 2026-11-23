package callbroker

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// Design 3.2 rule 5.1: a never token naming the cell's own token refuses, even under a broader on_request.
func TestSourceNeverRefusesLiveCallBrokerWrite(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	raw, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(raw), `on_request = ["all.change"]`, `on_request = ["all", "working_set"]`+"\n"+`never = ["working_set.change", "all.change"]`, 1)
	if s == string(raw) {
		t.Fatal("fixture user config not rewritten: " + string(raw))
	}
	if err := os.WriteFile(f.userPath, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "update-pr", "1")
	_ = v
	_ = err
	if f.effects(t) != 0 {
		t.Fatalf("write ran although host ceiling says never")
	}
}

// Narrower scoped ceiling `never` (design 3.2: narrower line governs) on a /2 production launch.
func TestScopedNeverRefusesLiveCallBrokerWrite(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	raw, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw) + "\n[scopes.narrow]\nsource=\"local-tasks\"\nresource=\"team:alpha\"\n[scopes.narrow.allow]\nnever=[\"all\", \"working_set\"]\n"
	if err := os.WriteFile(f.userPath, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "update-pr", "1")
	_ = v
	_ = err
	if f.effects(t) != 0 {
		t.Fatalf("write ran although a narrower scoped ceiling says never")
	}
}
