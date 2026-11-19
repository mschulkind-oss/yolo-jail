package brokerstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// kinds is the event kinds Diff reports between two reads of s, around step.
func kinds(t *testing.T, s *Store, step func()) ([]string, []Event) {
	t.Helper()
	before, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	step()
	after, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	evs := Diff(before, after)
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out, evs
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v, want %v", got, want)
	}
}

// Every change a watch reports, from filing to each way a grant ends.
func TestDiffReportsEveryEvent(t *testing.T) {
	s, now, input := fixture(t)
	var r Request
	got, evs := kinds(t, s, func() {
		var err error
		if r, err = s.File(input); err != nil {
			t.Fatal(err)
		}
	})
	wantKinds(t, got, EventRequestFiled)
	if evs[0].Request.ID != r.ID || evs[0].Request.State != "pending" {
		t.Fatalf("filed event: %+v", evs[0].Request)
	}
	var g Grant
	got, evs = kinds(t, s, func() {
		var err error
		if g, err = s.Decide(r.ID, "pending", Decision{For: time.Hour, Uses: 2}); err != nil {
			t.Fatal(err)
		}
	})
	wantKinds(t, got, EventRequestDecided)
	if evs[0].Request.State != "allowed" || evs[0].Grant == nil || evs[0].Grant.ID != g.ID {
		t.Fatalf("decided event carries no grant: %+v", evs[0])
	}
	consume := func() {
		if _, ok, err := s.Consume(input.Workspace, input.Jail, input.Set, r.Digest, s.Revalidate); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	got, evs = kinds(t, s, consume)
	wantKinds(t, got, EventGrantUsed)
	if evs[0].Grant.Uses != 1 || evs[0].Request == nil || evs[0].Request.ID != r.ID {
		t.Fatalf("used event: %+v", evs[0])
	}
	got, evs = kinds(t, s, consume)
	wantKinds(t, got, EventGrantUsed, EventGrantEnded)
	if evs[1].Grant.EndedBy() != EndUsed {
		t.Fatal(evs[1].Grant)
	}

	// Revoked, and expired.
	input.Argv = []string{"pr", "merge", "2"}
	r2, _ := s.File(input)
	g2, err := s.Decide(r2.ID, "pending", Decision{For: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = kinds(t, s, func() {
		if err := s.Revoke(g2.ID); err != nil {
			t.Fatal(err)
		}
	})
	wantKinds(t, got, EventGrantRevoked)
	input.Argv = []string{"pr", "merge", "3"}
	r3, _ := s.File(input)
	if _, err := s.Decide(r3.ID, "pending", Decision{For: time.Minute}); err != nil {
		t.Fatal(err)
	}
	input.Argv = []string{"pr", "merge", "4"}
	if _, err := s.File(input); err != nil {
		t.Fatal(err)
	}
	got, evs = kinds(t, s, func() { *now = now.Add(PendingLife) })
	wantKinds(t, got, EventRequestExpired, EventGrantEnded)
	if evs[1].Grant.EndedBy() != EndExpired {
		t.Fatal(evs[1].Grant)
	}

	// The jail stopping refuses what is pending and leaves what is live (BB-D99).
	input.Argv = []string{"pr", "merge", "5"}
	r5, _ := s.File(input)
	if _, err := s.Decide(r5.ID, "pending", Decision{For: time.Minute}); err != nil {
		t.Fatal(err)
	}
	input.Argv = []string{"pr", "merge", "6"}
	if _, err := s.File(input); err != nil {
		t.Fatal(err)
	}
	got, evs = kinds(t, s, func() {
		if err := s.End(input.Jail, input.Start); err != nil {
			t.Fatal(err)
		}
	})
	wantKinds(t, got, EventRequestDecided)
	if evs[0].Request.State != "refused" {
		t.Fatal(evs[0].Request)
	}
}

// A request filed and decided between two reads is reported as both, the filing first.
func TestDiffSplitsAFilingDecidedBetweenReads(t *testing.T) {
	s, _, input := fixture(t)
	got, evs := kinds(t, s, func() {
		r, _ := s.File(input)
		if _, err := s.Decide(r.ID, "pending", Decision{Deny: true}); err != nil {
			t.Fatal(err)
		}
	})
	wantKinds(t, got, EventRequestFiled, EventRequestDecided)
	if evs[0].Request.State != "pending" || evs[1].Request.State != "denied" {
		t.Fatal(evs[0].Request.State, evs[1].Request.State)
	}
	// The filing still carries the state it is already in, so a request hook asks nothing.
	if evs[0].Current != "denied" {
		t.Fatalf("the filing's current state is %q, want denied", evs[0].Current)
	}
}

// The four refusals a front end gives its own exit code are distinguishable.
func TestDecideRefusalKinds(t *testing.T) {
	s, _, input := fixture(t)
	input.Policy.MaxWindow, input.Policy.Session = time.Hour, false
	r, _ := s.File(input)
	if _, err := s.Decide(r.ID, "pending", Decision{For: 2 * time.Hour}); !errors.Is(err, ErrRefusedByPolicy) {
		t.Fatalf("ceiling: %v", err)
	}
	if _, err := s.Decide("r-00000000000000000000000000000000", "pending", Decision{}); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.Revoke("g-00000000000000000000000000000000"); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("unknown grant: %v", err)
	}
	if _, err := s.Decide(r.ID, "pending", Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(r.ID, "pending", Decision{}); !errors.Is(err, ErrAlreadyDecided) || !strings.Contains(err.Error(), "already denied") {
		t.Fatalf("decided: %v", err)
	}
	input.Argv = []string{"pr", "merge", "9"}
	r2, _ := s.File(input)
	s.Revalidate = func(Request) (Policy, error) {
		return Policy{}, errors.New("request holder is not this live host launch")
	}
	if _, err := s.Decide(r2.ID, "pending", Decision{}); !errors.Is(err, ErrNotConfirmed) || errors.Is(err, ErrRefusedByPolicy) {
		t.Fatalf("stale authority: %v", err)
	}
}

// Watch opens with the state, then reports each change until its context ends.
func TestWatchOpensWithStateThenEmitsEvents(t *testing.T) {
	s, _, input := fixture(t)
	s.Now = nil
	input.Argv = []string{"pr", "merge", "0"}
	first, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var opened []Request
	var got []string
	started := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- s.Watch(ctx, 5*time.Millisecond, func(snap Snapshot) { opened = snap.Requests; close(started) },
			func(ev Event) { mu.Lock(); got = append(got, ev.Kind); mu.Unlock() }, nil)
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("the watch ended before it opened: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never handed over its opening state")
	}
	if len(opened) != 1 || opened[0].ID != first.ID {
		t.Fatalf("opened with %+v", opened)
	}
	input.Argv = []string{"pr", "merge", "1"}
	if _, err := s.File(input); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wantKinds(t, got, EventRequestFiled)
}

// Every JSON object of the surface carries "schema": 1, and the state keeps only what is
// open: pending requests and live grants.
func TestSurfaceJSONCarriesSchemaAndOpenRecordsOnly(t *testing.T) {
	s, _, input := fixture(t)
	r, _ := s.File(input)
	g, _ := s.Decide(r.ID, "pending", Decision{For: time.Hour})
	input.Argv = []string{"pr", "merge", "2"}
	pending, _ := s.File(input)
	input.Argv = []string{"pr", "merge", "3"}
	r3, _ := s.File(input)
	g3, _ := s.Decide(r3.ID, "pending", Decision{})
	_ = s.Revoke(g3.ID)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.StateView(snap))
	var doc struct {
		Schema   int              `json:"schema"`
		Requests []map[string]any `json:"requests"`
		Grants   []map[string]any `json:"grants"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 || len(doc.Requests) != 1 || len(doc.Grants) != 1 {
		t.Fatalf("%s", b)
	}
	req, grant := doc.Requests[0], doc.Grants[0]
	for _, k := range []string{"id", "source", "jail", "workspace", "set", "argv", "command", "repo", "filed", "expires", "state"} {
		if _, ok := req[k]; !ok {
			t.Errorf("request has no %q: %s", k, b)
		}
	}
	if req["id"] != pending.ID || req["source"] != "github" || req["schema"] != float64(1) || req["command"] != "pr merge 2" {
		t.Errorf("request %v", req)
	}
	if grant["id"] != g.ID || grant["coverage"] != "set" || grant["uses_left"] != float64(25) || grant["schema"] != float64(1) || grant["state"] != "live" {
		t.Errorf("grant %v", grant)
	}
	ev := s.EventView(Event{Kind: EventGrantRevoked, Request: &r3, Grant: &g3}, time.Now())
	b, _ = json.Marshal(ev)
	if !strings.Contains(string(b), `"schema":1,"event":"grant-revoked"`) || !strings.Contains(string(b), `"coverage":"command"`) {
		t.Errorf("event %s", b)
	}
}

func TestStateViewWithEndedIncludesDecidedEndedAndHorizon(t *testing.T) {
	s, now, input := fixture(t)
	s.PublishRecords = true

	// 1) Allowed once and spent (tests answer=once, via, spent_at, ended grant with EndUsed).
	rOnce, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	gOnce, err := s.Decide(rOnce.ID, "pending", Decision{Via: ViaHost})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Consume(input.Workspace, input.Jail, input.Set, rOnce.Digest, s.Revalidate); err != nil || !ok {
		t.Fatalf("consume once: ok=%v err=%v", ok, err)
	}

	// 2) Denied with note (tests answer=deny, via, denial_note).
	input.Argv = []string{"pr", "merge", "2", "--body", "super-secret-body"}
	rDeny, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(rDeny.ID, "pending", Decision{Deny: true, DenialNote: "use release branch", Via: ViaOnRequest}); err != nil {
		t.Fatal(err)
	}

	// 3) Allowed for duration, then revoked (tests answer=for, until, ended grant with EndRevoked).
	input.Argv = []string{"pr", "merge", "3"}
	rFor, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	gFor, err := s.Decide(rFor.ID, "pending", Decision{For: 30 * time.Minute, Uses: 5, Via: ViaHost})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(gFor.ID); err != nil {
		t.Fatal(err)
	}

	// 4) Working-set entries: one live, one revoked, one timed and expired.
	wLive, _, err := s.AddWorking(WorkingEntry{Workspace: input.Workspace, Repo: "github.com/o/r", Kind: WorkingBranch, Resource: "refs/heads/live", By: "create-branch"})
	if err != nil {
		t.Fatal(err)
	}
	wRevoked, _, err := s.AddWorking(WorkingEntry{Workspace: input.Workspace, Repo: "github.com/o/r", Kind: WorkingBranch, Resource: "refs/heads/revoked", By: "human"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndWorking(wRevoked.ID, EndRevoked); err != nil {
		t.Fatal(err)
	}
	wTimed, _, err := s.AddWorking(WorkingEntry{Workspace: input.Workspace, Repo: "github.com/o/r", Kind: WorkingBranch, Resource: "refs/heads/timed", By: "human", Expires: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}

	// 5) Pending request, and expired/refused requests.
	input.Argv = []string{"pr", "merge", "4"}
	rExpired, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(PendingLife) // expires rExpired and wTimed
	input.Argv = []string{"pr", "merge", "5"}
	rRefused, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.End(input.Jail, input.Start); err != nil {
		t.Fatal(err)
	}
	input.Argv = []string{"pr", "merge", "6"}
	rPending, err := s.File(input)
	if err != nil {
		t.Fatal(err)
	}

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Default StateView remains open-only with no ended metadata.
	open := s.StateView(snap)
	if open.IncludesEnded || open.HorizonDays != 0 || open.HorizonNote != "" {
		t.Fatalf("open StateView unexpectedly set ended metadata: %+v", open)
	}
	if len(open.Requests) != 1 || open.Requests[0].ID != rPending.ID {
		t.Fatalf("open requests = %+v, want only %s", open.Requests, rPending.ID)
	}
	if len(open.Grants) != 0 {
		t.Fatalf("open grants = %+v, want none", open.Grants)
	}
	if len(open.Working) != 1 || open.Working[0].ID != wLive.ID {
		t.Fatalf("open working = %+v, want only %s", open.Working, wLive.ID)
	}

	// StateViewWithEnded includes all decided/ended records and horizon metadata.
	ended := s.StateViewWithEnded(snap)
	if ended.Schema != Schema || !ended.IncludesEnded || ended.HorizonDays != EndedRetentionDays || !strings.Contains(ended.HorizonNote, "7 days") {
		t.Fatalf("ended metadata = %+v", ended)
	}
	reqByID := map[string]RequestView{}
	for _, rv := range ended.Requests {
		reqByID[rv.ID] = rv
	}
	if len(reqByID) != 6 {
		t.Fatalf("ended requests count = %d, want 6", len(reqByID))
	}
	if got := reqByID[rOnce.ID]; got.State != "allowed" || got.Answer != AnswerOnce || got.Via != ViaHost || got.SpentAt == nil || got.Workspace != input.Workspace {
		t.Fatalf("rOnce = %+v", got)
	}
	if got := reqByID[rDeny.ID]; got.State != "denied" || got.Answer != AnswerDeny || got.Via != ViaOnRequest || got.DenialNote != "use release branch" || got.Workspace != input.Workspace {
		t.Fatalf("rDeny = %+v", got)
	}
	if got := reqByID[rFor.ID]; got.State != "allowed" || got.Answer != AnswerFor || got.Until == nil || got.Workspace != input.Workspace {
		t.Fatalf("rFor = %+v", got)
	}
	if got := reqByID[rExpired.ID]; got.State != "expired" || got.Decided == nil {
		t.Fatalf("rExpired = %+v", got)
	}
	if got := reqByID[rRefused.ID]; got.State != "refused" || got.Reason != ReasonBrokerStopped {
		t.Fatalf("rRefused = %+v", got)
	}

	grantByID := map[string]GrantView{}
	for _, gv := range ended.Grants {
		grantByID[gv.ID] = gv
	}
	if len(grantByID) != 2 {
		t.Fatalf("ended grants count = %d, want 2", len(grantByID))
	}
	if got := grantByID[gOnce.ID]; got.State != "ended" || got.EndReason != EndUsed || got.Ended == nil || got.Workspace != input.Workspace {
		t.Fatalf("gOnce = %+v", got)
	}
	if got := grantByID[gFor.ID]; got.State != "ended" || got.EndReason != EndRevoked || got.Ended == nil || got.Workspace != input.Workspace {
		t.Fatalf("gFor = %+v", got)
	}

	workByID := map[string]WorkingView{}
	for _, wv := range ended.Working {
		workByID[wv.ID] = wv
	}
	if len(workByID) != 3 {
		t.Fatalf("ended working count = %d, want 3", len(workByID))
	}
	if got := workByID[wLive.ID]; got.State != "live" || got.Ended != nil || got.Workspace != input.Workspace {
		t.Fatalf("wLive = %+v", got)
	}
	if got := workByID[wRevoked.ID]; got.State != "ended" || got.EndReason != EndRevoked || got.Ended == nil || got.Workspace != input.Workspace {
		t.Fatalf("wRevoked = %+v", got)
	}
	if got := workByID[wTimed.ID]; got.State != "ended" || got.EndReason != EndExpired || got.Ended == nil || got.Workspace != input.Workspace {
		t.Fatalf("wTimed = %+v", got)
	}
}
