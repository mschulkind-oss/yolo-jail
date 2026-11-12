package ghbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAccessFramesRejectMalformedVersionAndOperation(t *testing.T) {
	for _, raw := range []string{
		`{"op":"evaluate","version":2,"argv":["pr","merge","1"]}`,
		`{"op":"future","version":1,"argv":["pr","merge","1"]}`,
		`{"op":"evaluate","version":1.0,"argv":["pr","merge","1"]}`,
		`{"op":"evaluate","version":1,"argv":[]}`,
		`{"op":"status","version":1,"operation":"status","request":"r-one"}`,
		`{"version":1,"argv":["pr","merge","1"]}`,
		`{"op":"evaluate","version":"1","argv":["pr","merge","1"]}`,
	} {
		decoded, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		req := requestFrom(&hostservice.Session{Request: decoded.(*jsonx.OrderedMap)})
		if req.Invalid == "" {
			t.Errorf("accepted malformed/unsupported frame %s: %+v", raw, req)
		}
	}
	for _, raw := range []string{`{"argv":["pr","view","1"]}`, `{"operation":"status","request":"r-one"}`} {
		decoded, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		req := requestFrom(&hostservice.Session{Request: decoded.(*jsonx.OrderedMap)})
		if req.Invalid != "" || req.Op != "" || req.Version != 0 {
			t.Errorf("legacy frame refused: %s: %+v", raw, req)
		}
	}
}

func TestAccessEnvelopeRequiresVersionedAccessOperation(t *testing.T) {
	for _, raw := range []string{
		`{"access":{"argv":["pr","merge","1"]}}`,
		`{"access":{"argv":["pr","merge","1"]},"repo":"o/r"}`,
		`{"access":{"access":{"op":"status","version":1}}}`,
	} {
		decoded, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if req := requestFrom(&hostservice.Session{Request: decoded.(*jsonx.OrderedMap)}); req.Invalid == "" {
			t.Errorf("accepted legacy-shaped access envelope %s: %+v", raw, req)
		}
	}
	decoded, err := jsonx.Decode([]byte(`{"access":{"op":"evaluate","version":1,"argv":["pr","merge","1"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req := requestFrom(&hostservice.Session{Request: decoded.(*jsonx.OrderedMap)}); req.Invalid != "" || req.Op != "evaluate" || req.Version != 1 {
		t.Fatalf("valid access envelope rejected: %+v", req)
	}
}

func TestAccessStatusIsHostAssertedWorkspaceOnly(t *testing.T) {
	b, _ := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	fixtureStore := &brokerstore.Store{Dir: b.store.Dir, Source: Source, Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	newInput := func() brokerstore.Request {
		return brokerstore.Request{Jail: jail, Start: b.startID, Workspace: b.workspace, LaunchID: b.authority.scope.LaunchID,
			Argv: []string{"/host/private/argument-secret"}, Repo: "github.com/o/r", Set: SetReadWrite,
			StdinDigest: "stdin-hash", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1},
			HookFailure: "hook failed", HookOutput: "HOOKSECRET /host/private/settings"}
	}
	ownPending, err := fixtureStore.File(newInput())
	if err != nil {
		t.Fatal(err)
	}
	other := newInput()
	other.Jail, other.Start, other.Workspace = "other-jail", "other-start", "/workspace/other-project"
	otherPending, err := fixtureStore.File(other)
	if err != nil {
		t.Fatal(err)
	}
	status, err := b.accessStatus(context.Background(), jail)
	if err != nil {
		t.Fatal(err)
	}
	body := string(marshalAccess(status))
	if !strings.Contains(body, ownPending.ID) || strings.Contains(body, otherPending.ID) || strings.Contains(body, "/workspace/other-project") {
		t.Fatalf("workspace projection included wrong request: %s", body)
	}
	for _, secret := range []string{b.workspace, "/host/private/argument-secret", "HOOKSECRET", "/host/private/settings"} {
		if strings.Contains(body, secret) {
			t.Errorf("readout leaked %q: %s", secret, body)
		}
	}
}

func TestAccessStatusReportsInertHostGHWithoutLeakingReason(t *testing.T) {
	b, _ := windowFixture(t)
	b.execute = nil
	b.unavailable = "gh refused because of /host/private/path"
	jail := paths.JailShortHash(b.authority.scope.Container)
	status, err := b.accessStatus(context.Background(), jail)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "inert" || status.Next == "" || strings.Contains(status.Next, "/host/private/path") {
		t.Fatalf("inert status=%+v", status)
	}
}

func TestServeAccessAskFilesWithoutMerging(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	argv := []string{"pr", "merge", "40", "--squash", "-R", "o/r"}
	call := func(op string) evaluation {
		t.Helper()
		var got evaluation
		code := b.ServeContext(context.Background(), Request{Op: op, Version: AccessProtocolVersion, Mode: map[string]string{"evaluate": "evaluate", "ask": "ask"}[op], Argv: argv, Repo: "o/r"}, jail,
			func(p []byte) { _ = json.Unmarshal(p, &got) }, func([]byte) {})
		if code != 0 {
			t.Fatalf("%s exit %d: %+v", op, code, got)
		}
		return got
	}
	predicted := call("evaluate")
	if predicted.Schema != "yolo.access.prediction/1" || predicted.Verdict != "asks" || predicted.CommandDigest == "" || predicted.ReliesOnUntil != nil {
		t.Fatalf("plain prediction=%+v", predicted)
	}
	if reqs, _, err := b.store.List(); err != nil || len(reqs) != 0 || executed.Load() != 0 {
		t.Fatalf("plain prediction filed or executed: requests=%+v exec=%d err=%v", reqs, executed.Load(), err)
	}
	first := call("ask")
	second := call("ask")
	if first.Verdict != "asks" || !strings.HasPrefix(first.RequestID, "r-") || second.RequestID != first.RequestID {
		t.Fatalf("ask results first=%+v second=%+v", first, second)
	}
	if len(first.Next) != 2 || first.Next[0].Run != "yolo wait "+first.RequestID || first.Next[1].Run != "yolo approve "+first.RequestID {
		t.Fatalf("ask response lost its filed request menu: %+v", first.Next)
	}
	if first.CommandDigest != predicted.CommandDigest {
		t.Fatalf("digest changed: %s vs %s", first.CommandDigest, predicted.CommandDigest)
	}
	if reqs, _, err := b.store.List(); err != nil || len(reqs) != 1 || reqs[0].ID != first.RequestID || executed.Load() != 0 {
		t.Fatalf("ask should file one pending request without merging: requests=%+v exec=%d err=%v", reqs, executed.Load(), err)
	}
	if _, err := b.store.Decide(first.RequestID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if code := b.ServeContext(context.Background(), Request{Argv: argv, Repo: "o/r"}, jail, func([]byte) {}, func([]byte) {}); code != 0 || executed.Load() != 1 {
		t.Fatalf("approved real act exit=%d dispatches=%d", code, executed.Load())
	}
}

func TestServeAccessAskDoesNotSpendGrantAlreadyLive(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	req := writeReq()
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed request: %d", code)
	}
	pending, _, err := b.store.List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("requests=%+v err=%v", pending, err)
	}
	grant, err := b.store.Decide(pending[0].ID, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Op: "ask", Version: AccessProtocolVersion, Mode: "ask", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("pre-ask with live grant exit=%d result=%+v", code, prediction)
	}
	if prediction.ReliesOnUntil == nil || !prediction.ReliesOnUntil.Equal(grant.Expires) {
		t.Fatalf("pre-ask omitted the live grant's expiry: prediction=%+v grant=%+v", prediction, grant)
	}
	_, grants, err := b.store.List()
	if err != nil || len(grants) != 1 || grants[0].ID != grant.ID || grants[0].Uses != 1 || executed.Load() != 0 {
		t.Fatalf("pre-ask spent live grant or dispatched: grants=%+v executed=%d err=%v", grants, executed.Load(), err)
	}
}

func TestServeAccessEvaluateRecognizesTimedGrantExpiry(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	req := writeReq()
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed request: %d", code)
	}
	pending, _, err := b.store.List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("requests=%+v err=%v", pending, err)
	}
	grant, err := b.store.Decide(pending[0].ID, "pending", brokerstore.Decision{For: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Op: "evaluate", Version: AccessProtocolVersion, Mode: "evaluate", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("timed-grant prediction exit=%d result=%+v", code, prediction)
	}
	if prediction.ReliesOnUntil == nil || !prediction.ReliesOnUntil.Equal(grant.Expires) || executed.Load() != 0 {
		t.Fatalf("timed grant expiry or inertness missing: prediction=%+v grant=%+v executed=%d", prediction, grant, executed.Load())
	}
	_, grants, err := b.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != grant.Uses {
		t.Fatalf("prediction spent timed grant: grants=%+v err=%v", grants, err)
	}
}

func TestServeAccessAskReportsGrantArrivingBeforeFiling(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	req := writeReq()
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed request: %d", code)
	}
	pending, _, err := b.store.List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("requests=%+v err=%v", pending, err)
	}
	var arrived brokerstore.Grant
	landed := false
	b.store.AfterTransaction = func(error) {
		if landed {
			return
		}
		landed = true
		arrived, err = b.store.Decide(pending[0].ID, "pending", brokerstore.Decision{})
		if err != nil {
			t.Errorf("land grant between prediction and filing: %v", err)
		}
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Op: "ask", Version: AccessProtocolVersion, Mode: "ask", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("pre-ask race exit=%d result=%+v", code, prediction)
	}
	if prediction.RequestID != "" || !strings.Contains(prediction.CoveredBy, arrived.ID) || prediction.ReliesOnUntil == nil || !prediction.ReliesOnUntil.Equal(arrived.Expires) {
		t.Fatalf("pre-ask did not report the grant without a request or expiry: prediction=%+v grant=%+v", prediction, arrived)
	}
	_, grants, err := b.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != arrived.Uses || executed.Load() != 0 {
		t.Fatalf("pre-ask consumed grant or executed command: grants=%+v executed=%d err=%v", grants, executed.Load(), err)
	}
}

func TestServeAccessAskSeesWorkingSetEntryArrivingBeforeFiling(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	landed := false
	var added brokerstore.WorkingEntry
	b.store.AfterTransaction = func(error) {
		if landed {
			return
		}
		landed = true
		var err error
		added, _, err = b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r",
			Kind: brokerstore.WorkingPR, Resource: "#1", By: "human"})
		if err != nil {
			t.Errorf("land working-set entry before filing: %v", err)
		}
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Op: "ask", Version: AccessProtocolVersion, Mode: "ask", Argv: writeReq().Argv, Repo: "o/r"}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 {
		t.Fatalf("ask exit=%d prediction=%+v", code, prediction)
	}
	if prediction.Verdict != "runs" || prediction.CoveredBy != "working-set "+added.ID || prediction.RequestID != "" {
		t.Fatalf("late working-set coverage was missed: %+v entry=%+v", prediction, added)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 0 || executed.Load() != 0 {
		t.Fatalf("late coverage filed or executed: requests=%+v executed=%d err=%v", requests, executed.Load(), err)
	}
}

func TestServeAccessAskSeesUserGrantArrivingBeforeFiling(t *testing.T) {
	b, executed := windowFixture(t)
	userConfig := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userConfig), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userConfig, []byte(`{"brokered":{"github":{"grant_kinds":["create-pr"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	argv := []string{"pr", "create", "--title", "title", "--body", "body", "-R", "o/r"}
	landed := false
	b.store.AfterTransaction = func(error) {
		if landed {
			return
		}
		landed = true
		scope, err := brokerscope.ReadFile(b.authority.path)
		if err != nil {
			t.Errorf("read live scope: %v", err)
			return
		}
		scope.Grants = append(scope.Grants, brokerscope.Grant{Kind: GrantCreatePR, Repo: "github.com/o/r"})
		if _, err := brokerscope.Write(scope); err != nil {
			t.Errorf("land user grant before filing: %v", err)
		}
	}
	var prediction evaluation
	jail := paths.JailShortHash(b.authority.scope.Container)
	if code := b.ServeContext(context.Background(), Request{Op: "ask", Version: AccessProtocolVersion, Mode: "ask", Argv: argv, Repo: "o/r"}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 {
		t.Fatalf("ask exit=%d prediction=%+v", code, prediction)
	}
	if prediction.Verdict != "runs" || prediction.CoveredBy != GrantCreatePR || prediction.RequestID != "" {
		t.Fatalf("late user grant coverage was missed: %+v", prediction)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 0 || executed.Load() != 0 {
		t.Fatalf("late user grant filed or executed: requests=%+v executed=%d err=%v", requests, executed.Load(), err)
	}
}

func TestAccessStatusProjectsCooldownForUnpersistedExpiry(t *testing.T) {
	b, _ := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	if code := b.ServeContext(context.Background(), writeReq(), jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatal(code)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	q := requests[0]
	q.Filed = time.Now().Add(-65 * time.Minute)
	data, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.store.Dir, "requests", q.ID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	want := q.Expires().Add(10 * time.Minute)
	env, _ := frontedEndpoint(t, b, productionEndpointDirs(authorityContainer)["container"])
	body, code := ForwardAccess("status", nil, env)
	var status AccessStatus
	if err := json.Unmarshal(body, &status); err != nil || code != 0 || !status.Limits.CooldownUntil.Equal(want) || status.Limits.Pending != 0 || len(status.Requests) != 1 || status.Requests[0].State != "expired" {
		t.Fatalf("status=%+v want cooldown=%v exit=%d err=%v body=%s", status, want, code, err, body)
	}
	if status.Requests[0].ID != q.ID {
		t.Fatalf("status request=%+v want id=%s", status.Requests[0], q.ID)
	}
	after, _, _, _, err := b.store.InspectContext(context.Background())
	if err != nil || len(after) != 1 || after[0].State != "pending" {
		t.Fatalf("readout mutated expired pending record: %+v err=%v", after, err)
	}
}

func TestServeAccessStdinBinding(t *testing.T) {
	b, executed := windowFixture(t)
	jail := paths.JailShortHash(b.authority.scope.Container)
	argv := []string{"api", "-X", "POST", "repos/o/r/issues/1", "--input", "-"}
	ask := func(body string) evaluation {
		t.Helper()
		req := Request{Op: "ask", Version: AccessProtocolVersion, Mode: "ask", Argv: argv, Repo: "o/r", Stdin: []byte(body), StdinSent: true}
		var got evaluation
		if code := b.ServeContext(context.Background(), req, jail, func(p []byte) { _ = json.Unmarshal(p, &got) }, func([]byte) {}); code != 0 {
			t.Fatal(code)
		}
		return got
	}
	first, second := ask("first exact body"), ask("second exact body")
	if first.CommandDigest == second.CommandDigest || first.RequestID == second.RequestID {
		t.Fatalf("stdin not bound: first=%+v second=%+v", first, second)
	}
	requests, grants, err := b.store.List()
	if err != nil || len(requests) != 2 || len(grants) != 0 || executed.Load() != 0 {
		t.Fatalf("requests=%+v grants=%+v exec=%d err=%v", requests, grants, executed.Load(), err)
	}
	if _, err := b.store.Decide(first.RequestID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	third := ask("third body")
	if third.RequestID == first.RequestID {
		t.Fatalf("different stdin joined the first request: %+v", third)
	}
	_, grants, err = b.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 1 || executed.Load() != 0 {
		t.Fatalf("pre-ask consumed grant: grants=%+v exec=%d err=%v", grants, executed.Load(), err)
	}
	for _, body := range []string{"first exact body", "second exact body"} {
		sum := sha256.Sum256([]byte(body))
		want := hex.EncodeToString(sum[:])
		found := false
		for _, q := range requests {
			if q.StdinDigest == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no stored request carried exact stdin digest %s", want)
		}
	}
}

// GW-D80: the access status carries the pattern marker of a scope grant, so a grant the gate
// approved as a pattern and one an older build recorded on the same spelling are told apart by
// whoever reads the status. `repo` keeps its spelling for both.
func TestAccessStatusMarksAPatternScopeGrantApartFromALegacyOne(t *testing.T) {
	b, _ := windowFixture(t)
	rawScopeGrants(t, b, scopeFileGrants(t, `[{"kind":"read","repo":"*"},`+
		`{"kind":"runs","repo":"github.com/o/*","workflow":"legacy.yml"},`+
		`{"kind":"runs","repo":"github.com/o/*","workflow":"live.yml","repo_pattern":true}]`))
	status, err := b.accessStatus(context.Background(), paths.JailShortHash(b.authority.scope.Container))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ScopeGrants []struct {
			Kind        string `json:"kind"`
			Repo        string `json:"repo"`
			Name        string `json:"name"`
			RepoPattern bool   `json:"repo_pattern"`
		} `json:"scope_grants"`
	}
	body := marshalAccess(status)
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, g := range decoded.ScopeGrants {
		if g.Kind == "runs" {
			if g.Repo != "github.com/o/*" {
				t.Errorf("a runs grant's repo is %q, want the spelling it was recorded with", g.Repo)
			}
			marked[g.Name] = g.RepoPattern
		}
	}
	if pattern, ok := marked["live.yml"]; !ok || !pattern {
		t.Errorf("the grant approved as a pattern is not marked: %s", body)
	}
	if pattern, ok := marked["legacy.yml"]; !ok || pattern {
		t.Errorf("the legacy grant is missing or marked: %s", body)
	}
	if strings.Contains(string(body), `"repo_pattern":false`) {
		t.Errorf("an unmarked grant carries the marker field: %s", body)
	}
}
