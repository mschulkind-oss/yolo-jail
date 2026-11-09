package brokerstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDecideWorkingFailedLaterResourcePreservesPriorCoverage exercises the real
// decision boundary, including a fresh store reader after an answer fails.
func TestDecideWorkingFailedLaterResourcePreservesPriorCoverage(t *testing.T) {
	s, _ := workingFixture(t)
	prior, _, err := s.AddWorking(branch("/ws", "refs/heads/prior"))
	if err != nil {
		t.Fatal(err)
	}
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/new", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingTag, Resource: "refs/tags/invalid-permanent", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideWorking(q.ID, Decision{Add: true}); err == nil {
		t.Fatal("accepted invalid later entry")
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/new"); err != nil || ok {
		t.Fatalf("failed answer leaked first resource: %v %v", ok, err)
	}
	if held, ok, err := fresh.LiveWorking("/ws", prior.Repo, prior.Resource); err != nil || !ok || held.ID != prior.ID {
		t.Fatalf("prior authority lost: %+v %v %v", held, ok, err)
	}
	if got, err := fresh.Status(q.Jail, q.Start, q.ID); err != nil || got.State != "pending" {
		t.Fatalf("failed answer changed request: %+v %v", got, err)
	}
}

func TestDecideWorkingFailedAuditDoesNotCommitAnyResource(t *testing.T) {
	s, _ := workingFixture(t)
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("audit unavailable")
	s.Audit = func(_ context.Context, _ string, _ Request, _ Grant) error { return failure }
	if _, err = s.DecideWorking(q.ID, Decision{Add: true}); !errors.Is(err, failure) {
		t.Fatalf("audit failure: %v", err)
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	for _, resource := range []string{"refs/heads/one", "refs/heads/two"} {
		if _, ok, err := fresh.LiveWorking("/ws", q.Repo, resource); err != nil || ok {
			t.Fatalf("%s leaked: %v %v", resource, ok, err)
		}
	}
}

// TestDecideWorkingRequestRenameFailureKeepsTheWholeAnswerPending injects a
// real filesystem rename failure at the single commit point, after planning.
func TestDecideWorkingRequestRenameFailureKeepsTheWholeAnswerPending(t *testing.T) {
	s, _ := workingFixture(t)
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "requests", q.ID+".json")
	backup := path + ".saved"
	auditPath := filepath.Join(s.Dir, "test-audit.log")
	s.Audit = func(_ context.Context, _ string, _ Request, _ Grant) error {
		if err := os.WriteFile(auditPath, []byte("decision recorded before commit\n"), 0600); err != nil {
			return err
		}
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		return os.Mkdir(path, 0700) // rename of the answer over this directory must fail
	}
	if _, err := s.DecideWorking(q.ID, Decision{Add: true}); err == nil {
		t.Fatal("commit unexpectedly succeeded")
	}
	if audit, err := os.ReadFile(auditPath); err != nil || string(audit) != "decision recorded before commit\n" {
		t.Fatalf("required audit receipt: %q %v", audit, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	for _, resource := range []string{"refs/heads/one", "refs/heads/two"} {
		if _, ok, err := fresh.LiveWorking("/ws", q.Repo, resource); err != nil || ok {
			t.Fatalf("%s leaked: %v %v", resource, ok, err)
		}
	}
	if got, err := fresh.Status(q.Jail, q.Start, q.ID); err != nil || got.State != "pending" {
		t.Fatalf("answer after failed rename: %+v %v", got, err)
	}
}

func TestDecideWorkingCommittedBatchFreshReadersAndIndividualRevocation(t *testing.T) {
	s, clock := workingFixture(t)
	first, _, err := s.AddWorking(WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: WorkingBranch, Resource: "refs/heads/one", By: "human", Expires: clock.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DecideWorking(q.ID, Decision{For: 30 * time.Minute})
	if err != nil || len(result) != 2 {
		t.Fatalf("answer: %+v %v", result, err)
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	snap, err := fresh.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Working) != 2 {
		t.Fatalf("fresh snapshot: %+v", snap.Working)
	}
	public, err := json.Marshal(snap.Requests)
	if err != nil || bytes.Contains(public, []byte("working_entries")) {
		t.Fatalf("private commit data leaked into request readout: %s %v", public, err)
	}
	for _, resource := range []string{"refs/heads/one", "refs/heads/two"} {
		e, ok, err := fresh.LiveWorking("/ws", q.Repo, resource)
		if err != nil || !ok || !e.Expires.Equal(clock.Add(30*time.Minute)) {
			t.Fatalf("%s: %+v %v %v", resource, e, ok, err)
		}
	}
	if result[0].ID != first.ID {
		t.Fatalf("existing id replaced: %+v", result)
	}
	if err := fresh.EndWorking(result[0].ID, EndRevoked); err != nil {
		t.Fatal(err)
	}
	fresh = &Store{Dir: s.Dir, Now: s.Now}
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/one"); err != nil || ok {
		t.Fatalf("revoked entry returned: %v %v", ok, err)
	}
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/two"); err != nil || !ok {
		t.Fatalf("other entry lost: %v %v", ok, err)
	}
	*clock = clock.Add(31 * time.Minute)
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/two"); err != nil || ok {
		t.Fatalf("timed entry remained: %v %v", ok, err)
	}
}

func TestUncommittedOrMalformedWorkingAnswerIsNeverAuthority(t *testing.T) {
	for _, tc := range []struct{ name, state, answer, resource string }{
		{"pending", "pending", AnswerAdd, "refs/heads/unauthorized"},
		{"denied", "denied", AnswerAdd, "refs/heads/unauthorized"},
		{"expired", "expired", AnswerFor, "refs/heads/unauthorized"},
		{"wrong-answer", "allowed", AnswerOnce, "refs/heads/unauthorized"},
		{"wrong-workspace", "allowed", AnswerAdd, "refs/heads/unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := workingFixture(t)
			q, err := s.File(Request{Jail: "j", Start: "s", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "d", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
			if err != nil {
				t.Fatal(err)
			}
			q.State, q.Answer, q.Decided = tc.state, tc.answer, s.now()
			workspace := q.Workspace
			if tc.name == "wrong-workspace" {
				workspace = "/other"
			}
			e := WorkingEntry{ID: "w-11111111111111111111111111111111", Workspace: workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: tc.resource, By: "human", Added: s.now(), Revision: 1}
			data, err := json.Marshal(requestRecord{Request: q, WorkingEntries: []WorkingEntry{e}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Dir, "requests", q.ID+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			fresh := &Store{Dir: s.Dir, Now: s.Now}
			if _, ok, err := fresh.LiveWorking("/ws", q.Repo, e.Resource); ok || tc.name != "wrong-workspace" && err != nil {
				t.Fatalf("uncommitted entry: ok=%v err=%v", ok, err)
			}
			if tc.name == "wrong-workspace" {
				if _, ok, err := fresh.LiveWorking(workspace, q.Repo, e.Resource); ok || err == nil {
					t.Fatalf("malformed committed entry: ok=%v err=%v", ok, err)
				}
			}
		})
	}
}

func TestWorkingDecisionUpdatesAndRetentionNeverResurrectOldCoverage(t *testing.T) {
	s, clock := workingFixture(t)
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"}, {Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"}}, nil
	}
	seed := func() Request {
		t.Helper()
		q, err := s.File(Request{Jail: "j", Start: "s", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "d", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	first := seed()
	got, err := s.DecideWorking(first.ID, Decision{For: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	second := seed()
	if _, err := s.DecideWorking(second.ID, Decision{For: 30 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	if e, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/one"); err != nil || !ok || !e.Expires.Equal(clock.Add(30*time.Minute)) {
		t.Fatalf("second answer lost: %+v %v %v", e, ok, err)
	}
	if err := fresh.EndWorking(got[0].ID, EndRevoked); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/one"); err != nil || ok {
		t.Fatalf("revoked old batch resurrected: %v %v", ok, err)
	}
	*clock = clock.Add(8 * 24 * time.Hour)
	fresh = &Store{Dir: s.Dir, Now: s.Now}
	if all, err := fresh.Working(); err != nil || len(all) != 0 {
		t.Fatalf("retained/resurrected entries: %+v %v", all, err)
	}
	if _, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/one"); err != nil || ok {
		t.Fatalf("pruned request resurrected prior record: %v %v", ok, err)
	}
}

func TestDecideWorkingConcurrentFreshReaderSeesWholeAnswer(t *testing.T) {
	s, _ := workingFixture(t)
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "s", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "d", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.Audit = func(_ context.Context, _ string, _ Request, _ Grant) error { close(entered); <-release; return nil }
	result := make(chan error, 1)
	go func() { _, err := s.DecideWorking(q.ID, Decision{Add: true}); result <- err }()
	<-entered
	reader := &Store{Dir: s.Dir, Now: s.Now}
	type observed struct {
		n     int
		state string
		err   error
	}
	seen := make(chan observed, 1)
	go func() {
		snap, err := reader.Snapshot()
		o := observed{err: err}
		if err == nil {
			o.n = len(snap.Working)
			for _, r := range snap.Requests {
				if r.ID == q.ID {
					o.state = r.State
				}
			}
		}
		seen <- o
	}()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	o := <-seen
	if o.err != nil || (o.n != 0 || o.state != "pending") && (o.n != 2 || o.state != "allowed") {
		t.Fatalf("fresh reader saw a partial answer: %+v", o)
	}
	snap, err := reader.Snapshot()
	if err != nil || len(snap.Working) != 2 {
		t.Fatalf("after commit: %+v %v", snap.Working, err)
	}
}

func TestRequestWireCannotFileCommittedWorkingEntries(t *testing.T) {
	s, _ := workingFixture(t)
	raw := `{"Jail":"j","Start":"s","Workspace":"/ws","Argv":["push"],"Repo":"github.com/o/r","StdinDigest":"d","Set":"push","Policy":{"Window":900000000000,"MaxWindow":3600000000000,"Uses":1},"working_entries":[{"ID":"w-11111111111111111111111111111111","Workspace":"/ws","Repo":"github.com/o/r","Kind":"branch","Resource":"refs/heads/forged","By":"human","Revision":1}]}`
	var supplied Request
	if err := json.Unmarshal([]byte(raw), &supplied); err != nil {
		t.Fatal(err)
	}
	if len(supplied.workingEntries) != 0 {
		t.Fatal("caller supplied store-private entries")
	}
	q, err := s.File(supplied)
	if err != nil {
		t.Fatal(err)
	}
	if q.State != "pending" {
		t.Fatalf("filed request state %q", q.State)
	}
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	if _, ok, err := fresh.LiveWorking("/ws", "github.com/o/r", "refs/heads/forged"); err != nil || ok {
		t.Fatalf("caller created coverage: %v %v", ok, err)
	}
}

// TestDecideWorkingFailedEntryRecordStoresNoResource is BX-D18 (pack-brokers.md): an answer
// that changes several resources is stored for every resource or for none. Beside the request
// record that commits it, an answer writes each entry as its own working/ record, the copy a
// running broker older than committed answers reads. A failure on the second of those writes
// must leave no resource stored: not the request's commit (a current reader's view), not the
// first entry's record (an older broker's view), and the prior timed entry the answer would
// have extended keeps its old bytes.
func TestDecideWorkingFailedEntryRecordStoresNoResource(t *testing.T) {
	s, now := workingFixture(t)
	prior, _, err := s.AddWorking(WorkingEntry{Workspace: "/ws", Repo: "github.com/o/r", Kind: WorkingBranch,
		Resource: "refs/heads/one", By: "human", Expires: now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	priorPath := filepath.Join(s.Dir, "working", prior.ID+".json")
	priorBytes, err := os.ReadFile(priorPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Entries = func(q Request) ([]WorkingEntry, error) {
		return []WorkingEntry{
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/one", By: "human"},
			{Workspace: q.Workspace, Repo: q.Repo, Kind: WorkingBranch, Resource: "refs/heads/two", By: "human"},
		}, nil
	}
	q, err := s.File(Request{Jail: "j", Start: "start", Workspace: "/ws", Argv: []string{"push"}, Repo: "github.com/o/r", StdinDigest: "digest", Set: "push", Policy: Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected working record write failure")
	workingWrites := 0
	s.beforeWrite = func(kind, _ string) error {
		if kind == "working" {
			workingWrites++
			if workingWrites == 2 {
				return failure
			}
		}
		return nil
	}
	var audited []string
	s.Audit = func(_ context.Context, kind string, _ Request, _ Grant) error {
		audited = append(audited, kind)
		return nil
	}
	if _, err := s.DecideWorking(q.ID, Decision{For: time.Hour}); !errors.Is(err, failure) || !strings.Contains(err.Error(), "still pending") {
		t.Fatalf("answer with a failed entry record: %v; want the injected failure, naming the request still pending", err)
	}
	if workingWrites < 2 {
		t.Fatalf("the injected failure never ran: %d working writes", workingWrites)
	}
	if strings.Join(audited, ",") != "decision,decision-withdrawn" {
		t.Fatalf("audit of an undone answer: %v; want the decision followed by its withdrawal", audited)
	}
	s.beforeWrite = nil
	fresh := &Store{Dir: s.Dir, Now: s.Now}
	if _, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/two"); err != nil || ok {
		t.Fatalf("a failed answer stored its second resource for a current reader: %v %v", ok, err)
	}
	if held, ok, err := fresh.LiveWorking("/ws", q.Repo, "refs/heads/one"); err != nil || !ok || held.ID != prior.ID || !held.Expires.Equal(prior.Expires) {
		t.Fatalf("a failed answer changed the prior entry for a current reader: %+v %v %v", held, ok, err)
	}
	if got, err := fresh.Status(q.Jail, q.Start, q.ID); err != nil || got.State != "pending" {
		t.Fatalf("a failed answer left the request %+v (%v); want it pending", got, err)
	}
	// An older broker reads working/ alone: it must see exactly the prior record, unchanged.
	files, err := os.ReadDir(filepath.Join(s.Dir, "working"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	if len(names) != 1 || names[0] != prior.ID+".json" {
		t.Fatalf("a failed answer left working records %v; want only the prior %s", names, prior.ID)
	}
	if after, err := os.ReadFile(priorPath); err != nil || !bytes.Equal(after, priorBytes) {
		t.Fatalf("a failed answer rewrote the prior entry's record: %s (%v)", after, err)
	}
	// The same answer, retried once the store writes again, stores both resources.
	entries, err := s.DecideWorking(q.ID, Decision{For: time.Hour})
	if err != nil || len(entries) != 2 {
		t.Fatalf("retried answer: %+v %v", entries, err)
	}
}
