package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// secretReq is a GitHub write whose --body carries a fake token, the case
// request-history-and-privacy.md §6.1 masks.
func secretReq(id, state string) RequestView {
	r := req(id, state)
	r.Source, r.Workspace, r.Jail = "github", "/home/example/code/app", "jail1"
	r.Argv = []string{"secret", "set", "NAME", "--body", "ghp_FAKEFAKEFAKE", "-Fx=ghp_GLUED"}
	r.Command = "secret set NAME --body ghp_FAKEFAKEFAKE -Fx=ghp_GLUED"
	r.CommandDigest, r.StdinDigest = "sha256:cmd", "sha256:stdin"
	r.Title = "set a secret"
	return r
}

func historyOut(t *testing.T, p Published, key string) HistoryOut {
	t.Helper()
	for _, e := range p.History.Entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no history entry %s in %+v", key, p.History.Entries)
	return HistoryOut{}
}

// assertMaskedDetail checks the published last-seen request: every field, the secret values
// masked exactly as a pending request's are, the digests visible, the raw command never.
func assertMaskedDetail(t *testing.T, e HistoryOut, wantState string) {
	t.Helper()
	d := e.Request
	if d == nil {
		t.Fatalf("history entry %s carries no last-seen request", e.Key)
	}
	if d.ID != e.Key || d.Workspace != "/home/example/code/app" || d.Jail != "jail1" || d.Title != "set a secret" || d.State != wantState {
		t.Fatalf("last-seen request fields: %+v", d)
	}
	if d.CommandDigest != "sha256:cmd" || d.StdinDigest != "sha256:stdin" {
		t.Fatalf("digests must stay visible: %+v", d)
	}
	r := secretReq(e.Key, wantState)
	if want := MaskedCommand(r.Argv); d.Command != want {
		t.Fatalf("command %q, want the masked %q", d.Command, want)
	}
	if strings.Contains(d.Command, "ghp_") {
		t.Fatalf("the raw secret reached the command: %q", d.Command)
	}
	secrets := 0
	for _, it := range d.Argv {
		if it.Secret {
			secrets++
		}
	}
	if secrets != 2 {
		t.Fatalf("want both secret values masked, got %d: %+v", secrets, d.Argv)
	}
}

// RH §6.2: a history entry keeps the request as last seen, masked like pending detail.
func TestHistoryKeepsTheRequestAsLastSeen(t *testing.T) {
	r := secretReq("r-1", "pending")
	m := snap(New(t0), at(1), []RequestView{r}, nil)

	// Decided by an event: the event's request is the last seen.
	d := r
	d.State, d.HookFailure = "denied", "hook exited 1"
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &d}}, at(5))
	p := Publish(m, at(6), Options{})
	e := historyOut(t, p, "r-1")
	assertMaskedDetail(t, e, "denied")
	if e.Request.HookFailure != "hook exited 1" {
		t.Fatalf("last-seen request is not the event's: %+v", e.Request)
	}

	// A late, lower-ranked filed event keeps the outcome and the later detail.
	f := r
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvFiled, Time: at(2), Request: &f}}, at(7))
	assertMaskedDetail(t, historyOut(t, Publish(m, at(8), Options{}), "r-1"), "denied")

	// Vanished with no event: the copy the last snapshot held is kept.
	r2 := secretReq("r-2", "pending")
	m = snap(m, at(10), []RequestView{r2}, nil)
	r2b := r2
	r2b.HookOutput = "refreshed"
	m = snap(m, at(11), []RequestView{r2b}, nil)
	m = snap(m, at(20), nil, nil)
	e2 := historyOut(t, Publish(m, at(21), Options{}), "r-2")
	assertMaskedDetail(t, e2, "pending")
	if e2.Request.HookOutput != "refreshed" || e2.Outcome != "no longer pending; outcome not observed" {
		t.Fatalf("vanished entry: %+v / %+v", e2, e2.Request)
	}

	// The published JSON never holds the raw command yolo printed.
	b, err := json.Marshal(Publish(m, at(22), Options{}).History)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--body ghp_") {
		t.Fatalf("raw command published in history: %s", b)
	}
}

// A snapshot refreshes the detail of a request already in history and still pending, so the
// entry holds what was last seen, not what was first seen.
func TestHistoryDetailFollowsLaterSnapshots(t *testing.T) {
	r := secretReq("r-1", "pending")
	m := Reduce(New(t0), EventIn{EventView{Schema: 1, Event: EvFiled, Time: at(1), Request: &r}}, at(1))
	r2 := r
	r2.HookFailure = "hook exited 127"
	m = snap(m, at(3), []RequestView{r2}, nil)
	e := historyOut(t, Publish(m, at(4), Options{}), "r-1")
	if e.Request == nil || e.Request.HookFailure != "hook exited 127" || e.Outcome != "filed" {
		t.Fatalf("%+v / %+v", e, e.Request)
	}
}

// Grant entries carry no request detail; the grant's identifiers are its whole record.
func TestGrantHistoryCarriesNoRequestDetail(t *testing.T) {
	g := grant("g-1", "r-1")
	m := snap(New(t0), at(1), nil, []GrantView{g})
	m = snap(m, at(2), nil, nil)
	if e := historyOut(t, Publish(m, at(3), Options{}), "g-1"); e.Request != nil {
		t.Fatalf("grant entry carries a request: %+v", e.Request)
	}
}
