package callbroker

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// editUserAndHold rewrites the host user config the live broker re-reads at every call, then
// files the working-set entry the write needs.
func editUserAndHold(t *testing.T, f *onceFixture, edit func(string) string) {
	t.Helper()
	raw, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	next := edit(string(raw))
	if next == string(raw) {
		t.Fatal("fixture user config not rewritten: " + string(raw))
	}
	if err := os.WriteFile(f.userPath, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
}

const scopedHeader = "\n[scopes.narrow]\nsource=\"local-tasks\"\nresource=\"team:alpha\"\n[scopes.narrow.allow]\n"

// Design 3.7 and the governing rule: a never naming a read must stop the read, in every spelling
// the config accepts, until the scoped decision engine is wired (plan U3).
func TestNeverOnAReadRefusesTheLiveCallBrokerRead(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"scoped never read": func(s string) string { return s + scopedHeader + "never=[\"read\"]\n" },
		"source never read": func(s string) string {
			return strings.Replace(s, `on_request = ["all.change"]`, `on_request = ["all.change"]`+"\n"+`never = ["read"]`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := selectedOnceFixtureWithStanding(t, true)
			editUserAndHold(t, f, edit)
			v, _, _ := f.call(t, "act", "read")
			if v.Verdict == "runs" {
				t.Fatalf("a read ran although the host config says never: %+v", v)
			}
			if v.Next == "" {
				t.Fatal("the refusal names no next step")
			}
		})
	}
}

// A read with no never still runs (the control that keeps the refusal honest).
func TestAReadWithoutNeverStillRuns(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	v, _, err := f.call(t, "act", "read")
	if err != nil || v.Verdict != "runs" {
		t.Fatalf("control read: %+v %v", v, err)
	}
}

// Design 3.2: a narrower on_request under a broad always governs; a write must not run unasked.
func TestScopedOnRequestUnderAlwaysDoesNotRunTheLiveWrite(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	// The launch froze an always ceiling with no request: the write runs unasked unless a line says otherwise.
	f.h.input.EffectiveCells["change"] = config.CallBrokerCellPolicy{Tables: map[string]config.CallBrokerTablePolicy{
		"all":         {Request: "absent", Ceiling: "always", Decision: "run"},
		"working_set": {Request: "absent", Ceiling: "always", Decision: "run"}}}
	editUserAndHold(t, f, func(s string) string {
		s = strings.Replace(s, `on_request = ["all.change"]`, `always = ["all", "working_set"]`, 1)
		return s + scopedHeader + "on_request=[\"all\", \"working_set\"]\n"
	})
	f.call(t, "act", "update-pr", "1")
	if f.effects(t) != 0 {
		t.Fatal("a write ran unasked although a narrower line says on_request")
	}
}
