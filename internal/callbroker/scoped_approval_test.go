package callbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

func TestScopedApprovalMixedRolesExpandWithSeparatedMergeKeys(t *testing.T) {
	chain := []brokerstore.CallResource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:alpha"}}
	q := brokerstore.Request{Workspace: t.TempDir(), Repo: "local-tasks", CallReads: []brokerstore.CallRead{{Cell: "change", Chain: chain}}, CallWrites: []brokerstore.CallWrite{{Cell: "change", Chain: chain}, {Cell: "change", Chain: chain, Role: brokerstore.RoleWithheldWrite, Candidates: []string{"change", "rename"}}}}
	entries, err := RequestEntries(q)
	if err != nil || len(entries) != 4 {
		t.Fatalf("mixed expansion: %+v %v", entries, err)
	}
	keys := map[string]bool{}
	for _, e := range entries {
		key := e.Kind + "/" + e.Resource
		if keys[key] {
			t.Fatalf("roles/candidates merged: %+v", entries)
		}
		keys[key] = true
	}
	store := &brokerstore.Store{Dir: t.TempDir(), Source: "local-tasks", Revalidate: func(current brokerstore.Request) (brokerstore.Policy, error) {
		if current.Digest != brokerstore.CommandDigest(q) {
			return brokerstore.Policy{}, os.ErrPermission
		}
		return current.Policy, nil
	}, Entries: RequestEntries}
	q.Jail, q.Start, q.Set, q.Argv, q.StdinDigest = "j", "s", "local-tasks", []string{"update", "a1"}, "body"
	q.Policy = brokerstore.Policy{Window: brokerstore.TimedGrantDefault, MaxWindow: time.Hour, Uses: 1}
	q.Digest = brokerstore.CommandDigest(q)
	filed, err := store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := store.List()
	if err != nil || len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 2 {
		t.Fatalf("durable subjects: %+v %v", pending, err)
	}
	allowed, err := store.DecideWorking(filed.ID, brokerstore.Decision{For: time.Hour})
	if err != nil || len(allowed) != 4 {
		t.Fatalf("timed bundle: %+v %v", allowed, err)
	}
	for _, entry := range allowed {
		if !entry.Expires.Equal(allowed[0].Expires) {
			t.Fatal("mixed expiries")
		}
	}
}

func republishScopedApprovalInput(t *testing.T, f *onceFixture) {
	t.Helper()
	raw, err := json.Marshal(f.h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.h.original = raw
}

func freshScopedApprovalInput(t *testing.T, f *onceFixture, in callbrokerinput.Launch) (string, string, brokerscope.File, string) {
	t.Helper()
	sf := f.scope
	var err error
	sf.LaunchID, err = brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	sf.BrokerStart, sf.BrokerPID, sf.CallCLIPath, sf.CallCLIHash, sf.CallInput = "", 0, "", "", ""
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	in.ScopeFile = path
	_, block, err := loopholegrant.Read(in.GrantFile)
	if err != nil || block == nil {
		t.Fatalf("fixture grant: %v", err)
	}
	block.LaunchID = sf.LaunchID
	grantRaw, err := loopholegrant.Merge([]byte("{}"), *block)
	if err != nil {
		t.Fatal(err)
	}
	in.GrantFile = filepath.Join(f.home, "fresh-grant.json")
	if err := os.WriteFile(in.GrantFile, grantRaw, 0600); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(f.home, "fresh-selected.json")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.BindCallInput(path, sf, inputPath); err != nil {
		t.Fatal(err)
	}
	sf, err = brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start, err := brokerstore.NewStartID()
	if err != nil {
		t.Fatal(err)
	}
	bound, lease, err := brokercore.BindLease(in.Source, path, sf, start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := brokercore.CloseLease(in.Source, lease); err != nil {
			t.Error(err)
		}
	})
	return inputPath, path, bound, start
}

func TestScopedApprovalExactAnswerSurvivesFreshLiveHolder(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("file: %+v %v", v, err)
	}
	if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	in := f.h.input
	in.FrontJailID = "fresh-launch"
	inputPath, path, bound, start := freshScopedApprovalInput(t, f, in)
	h, err := newCLIHandler(inputPath, f.home, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	h.classifier.deadline = fixtureClassifierDeadline
	if err := brokerscope.BindCallCLI(path, bound, h.program.path, h.program.sha256); err != nil {
		t.Fatal(err)
	}
	bound.CallCLIPath, bound.CallCLIHash = h.program.path, h.program.sha256
	h.scopePath, h.bound, h.start = path, bound, start
	f.h, f.store.Revalidate = h, h.revalidate
	v, _, err := f.call(t, "act", "update", "a1")
	snap, snapErr := f.store.Snapshot()
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 || snap.Grants[0].Uses != 0 {
		t.Fatalf("fresh holder refused: %+v err=%v effects=%d uses=%d", v, err, f.effects(t), snap.Grants[0].Uses)
	}
}

func TestScopedApprovalHTTPAnswerSurvivesFreshLiveHolder(t *testing.T) {
	const body = `{"operationName":"Rename","title":"ready"}`
	var effects atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			effects.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
		}
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(body))
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(body)}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
		t.Fatalf("ask: %d %s", rc, message)
	}
	pending := f.pending(t)
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	inputPath, path, bound, start := freshScopedApprovalInput(t, f, h.input)
	fresh, err := newHTTPHandler(inputPath, f.home, f.store, h.audit)
	if err != nil {
		t.Fatal(err)
	}
	fresh.scopePath, fresh.bound, fresh.start, fresh.httpClient = path, bound, start, executor
	f.store.Revalidate = fresh.revalidateHTTP
	freshForward := selectedHTTPHostFrame(t, fresh)
	changed := call
	changed.Query = "changed=1"
	if _, message := freshForward("act", changed); strings.Contains(message, `"verdict":"runs"`) || effects.Load() != 0 {
		t.Fatalf("changed query spent: %s effects=%d", message, effects.Load())
	}
	snap, err := f.store.Snapshot()
	if err != nil || len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
		t.Fatalf("mutation spent grant: %+v %v", snap, err)
	}
	if rc, message := freshForward("act", call); rc != 0 || !strings.Contains(message, `"status":200`) || effects.Load() != 1 {
		t.Fatalf("fresh HTTP holder: %d %s effects=%d", rc, message, effects.Load())
	}
	snap, err = f.store.Snapshot()
	if err != nil || snap.Grants[0].Uses != 0 {
		t.Fatalf("holder answer unspent: %+v %v", snap, err)
	}
	for _, stored := range snap.Requests {
		if stored.ID == pending[0].ID && stored.LaunchID != pending[0].LaunchID {
			t.Fatalf("holder history rewritten: %+v", stored)
		}
	}
}

func TestScopedApprovalWorkingTableSurvivesTransaction(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	raw, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"all.change"`, `"working_set.change"`, 1))
	if err := os.WriteFile(f.userPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	p := f.h.input.EffectiveCells["change"]
	p.Tables["all"] = config.CallBrokerTablePolicy{Request: "false", Ceiling: "off", Decision: "refuse"}
	f.h.input.EffectiveCells["change"] = p
	republishScopedApprovalInput(t, f)
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	answer, err := f.h.classifier.ClassifyCLI(context.Background(), []string{"update-pr", "1"}, nil, nil)
	if err != nil || answer.Classification == nil {
		t.Fatalf("classify: %v", err)
	}
	_, _, working, now, err := f.store.InspectContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pure := decideCall(*answer.Classification, f.h.input.EffectiveCells, working, f.home, f.h.input.Source, now); pure.Verdict != "runs" {
		t.Fatalf("fixture prediction: %+v", pure)
	}
	v, _, err := f.call(t, "act", "update-pr", "1")
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("working-table transaction refused: %+v err=%v effects=%d", v, err, f.effects(t))
	}
}

func TestScopedApprovalHTTPWorkingTableSurvivesTransaction(t *testing.T) {
	const body = `{"title":"ready"}`
	var effects atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			effects.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"#1","project":{"id":"team:alpha"}}`))
		}
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(body))
	manifestPath := filepath.Join(f.home, "test-pack", "loopholes", "tasks", "manifest.jsonc")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"http_resource":"task"`, `"http_resource":"pr"`, 1))
	if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	decl, err := loopholedecl.Decode(raw, filepath.Dir(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	h.input.DeclarationHash = loopholedecl.CallBrokerDeclarationHash(decl.CallBroker)
	h.input.HTTPBinding.DeclarationHash = h.input.DeclarationHash
	h.input.Lookups = decl.CallBroker.Lookups
	writeAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}},"writes":[{"cell":"change","chain":[{"type":"pr","id":"#1"},{"type":"project","id":"team:alpha"}],"description":"change PR"}]}}`, digestHTTPBody(true, []byte(body)))
	lookupAnswer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"GET","path":"/items/%%231","query":"","headers":[],"body":{"mode":"original","present":false,"sha256":%q}},"reads":[[{"type":"pr","id":"#1"},{"type":"project","id":"team:alpha"}]]}}`, digestHTTPBody(false, nil))
	classifier := fixtureHTTPClassifier(t, "input=$(cat)\ncase \"$input\" in *'/items/'*) cat <<'LOOKUP'\n"+lookupAnswer+"\nLOOKUP\n;; *) cat <<'WRITE'\n"+writeAnswer+"\nWRITE\n;; esac")
	h.classifier.Identities, h.input.ProgramIdentities = classifier.Identities, classifier.Identities
	policy := h.input.EffectiveCells["change"]
	policy.Tables["all"] = config.CallBrokerTablePolicy{Request: "false", Ceiling: "off", Decision: "refuse"}
	policy.Tables["working_set"] = config.CallBrokerTablePolicy{Request: "default", Ceiling: "on_request", Decision: "run"}
	h.input.EffectiveCells["change"] = policy
	raw, err = json.Marshal(h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	h.original = raw
	raw, err = os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.userPath, []byte(strings.Replace(string(raw), `"all.change"`, `"working_set.change"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(body)}
	if rc, message := forward("act", call); rc != 0 || !strings.Contains(message, `"status":200`) || effects.Load() != 1 {
		t.Fatalf("HTTP working-table transaction: rc=%d message=%s effects=%d", rc, message, effects.Load())
	}
}

func TestScopedApprovalTimedNoncallNeverSelectsMembershipTable(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	p := f.h.input.EffectiveCells["change"]
	p.Tables["all"] = config.CallBrokerTablePolicy{Request: "false", Ceiling: "off", Decision: "refuse"}
	f.h.input.EffectiveCells["change"] = p
	republishScopedApprovalInput(t, f)
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	call := Classification{Writes: []Write{{Cell: "change", Chain: []Resource{{Type: "pr", ID: "#1"}, {Type: "project", ID: "team:alpha"}}}}}
	_, _, working, now, err := f.store.InspectContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if verdict := decideCall(call, f.h.input.EffectiveCells, working, f.home, f.h.input.Source, now); verdict.Verdict != "refused" {
		t.Fatalf("timed noncall selected membership: %+v", verdict)
	}
	if v, _, err := f.call(t, "act", "update-pr", "1"); err != nil || v.Verdict != "refused" || f.effects(t) != 0 {
		t.Fatalf("timed membership dispatched: %+v %v", v, err)
	}
	if pending := f.pending(t); len(pending) != 0 {
		t.Fatalf("refused operation filed: %+v", pending)
	}
}

func TestScopedApprovalReplayUsesActualCallerContext(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", v, err)
	}
	if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// This event lands after the initial classification and at the store view
	// boundary; replay must not reset the caller's now-cancelled execute budget.
	f.store.AfterTransaction = func(error) { cancel() }
	v, _, err := f.h.serveCLI(ctx, "act", f.h.input.FrontJailID, []string{"update", "a1"}, nil)
	f.store.AfterTransaction = nil
	snapshot, snapErr := f.store.Snapshot()
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if err == nil || v.Verdict == "runs" || f.effects(t) != 0 || snapshot.Grants[0].Uses != 1 {
		t.Fatalf("cancelled replay spent or ran: %+v %v snapshot=%+v", v, err, snapshot)
	}
}

func TestScopedApprovalActualStartupPlacement(t *testing.T) {
	for _, mode := range []string{"direct", "symlink", "native-home-exemption"} {
		t.Run(mode, func(t *testing.T) {
			old, inputPath, _, store := testCallHandler(t)
			workspace := t.TempDir()
			program := filepath.Join(workspace, "tasks")
			if mode == "native-home-exemption" {
				program = filepath.Join(os.Getenv("HOME"), "bin", "exempt-tasks")
				workspace = os.Getenv("HOME")
			}
			if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if mode == "symlink" {
				link := filepath.Join(os.Getenv("HOME"), "bin", "tasks-link")
				if err := os.Symlink(program, link); err != nil {
					t.Fatal(err)
				}
				old.input.Program = link
			} else {
				old.input.Program = program
			}
			raw, err := json.Marshal(old.input)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inputPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			wantRefused := mode != "native-home-exemption"
			if why := brokercore.PlacementRefusal("tasks", workspace)(program); (why != "") != wantRefused {
				t.Fatalf("placement fixture: %q", why)
			}
			h, err := newCLIHandler(inputPath, workspace, store, old.audit)
			if wantRefused && (err == nil || h != nil) {
				t.Fatal("workspace-writable program accepted at actual startup")
			}
			if !wantRefused && err != nil {
				t.Fatal(err)
			}
		})
	}
}
