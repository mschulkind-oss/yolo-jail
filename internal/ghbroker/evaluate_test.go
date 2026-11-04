package ghbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestEvaluateDecisionMatchesActForCurrentStanding(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*testing.T, *Broker) Request
		verdict  string
		actCodes []int
	}{
		{name: "no read grant", verdict: "refused", actCodes: []int{ExitUsage}, setup: func(t *testing.T, b *Broker) Request {
			if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, b.authority.scope.Repos, nil); err != nil {
				t.Fatal(err)
			}
			return writeReq()
		}},
		{name: "out of scope", verdict: "refused", actCodes: []int{ExitUsage}, setup: func(_ *testing.T, _ *Broker) Request {
			return Request{Argv: []string{"pr", "comment", "1", "--body", "hello", "-R", "private/repo"}, Repo: "o/r"}
		}},
		{name: "uncovered merge", verdict: "asks", actCodes: []int{ExitTempFail}, setup: func(_ *testing.T, _ *Broker) Request {
			return Request{Argv: []string{"pr", "merge", "1", "--squash", "-R", "o/r"}, Repo: "o/r"}
		}},
		{name: "user grant", verdict: "runs", actCodes: []int{0}, setup: func(t *testing.T, b *Broker) Request {
			grantCreatePR(t, b, true)
			return Request{Argv: []string{"pr", "create", "--title", "t", "--body", "b", "--head", "agent/x"}, Repo: "o/r"}
		}},
		{name: "working set", verdict: "runs", actCodes: []int{0}, setup: func(t *testing.T, b *Broker) Request {
			addEvaluationWorking(t, b, brokerstore.WorkingPR, time.Time{})
			return writeReq()
		}},
		{name: "timed grant", verdict: "runs", actCodes: []int{0}, setup: func(t *testing.T, b *Broker) Request {
			addEvaluationWorking(t, b, brokerstore.WorkingPR, time.Now().Add(time.Hour))
			return writeReq()
		}},
		{name: "allow once", verdict: "runs", actCodes: []int{0}, setup: func(t *testing.T, b *Broker) Request {
			req := writeReq()
			jail := paths.JailShortHash(b.authority.scope.Container)
			if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
				t.Fatalf("seed request exit %d", code)
			}
			requests, _, err := b.store.List()
			if err != nil || len(requests) != 1 {
				t.Fatalf("seed requests=%+v err=%v", requests, err)
			}
			if _, err := b.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			return req
		}},
		{name: "failed fact read", verdict: "asks", actCodes: []int{ExitTempFail}, setup: func(t *testing.T, b *Broker) Request {
			addEvaluationWorking(t, b, brokerstore.WorkingPR, time.Time{})
			return writeReq()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := windowFixture(t)
			b.waitLimit = time.Millisecond
			readExit, proposedWrites := 0, 0
			b.execute = func(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
				if len(argv) == 2 && argv[0] == "api" && argv[1] == "repos/o/r/issues/1" {
					if readExit != 0 {
						return RunResult{Exit: readExit}
					}
					out([]byte(`{"number":1,"pull_request":{"merged_at":null}}`))
					return RunResult{Exit: 0}
				}
				proposedWrites++
				return RunResult{Exit: 0}
			}
			if tc.name == "failed fact read" {
				readExit = 1
			}
			req := tc.setup(t, b)
			jail := paths.JailShortHash(b.authority.scope.Container)
			beforeWrites := proposedWrites
			var result evaluation
			code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: req.Argv, Repo: req.Repo, Stdin: req.Stdin, StdinSent: req.StdinSent}, jail,
				func(p []byte) { _ = json.Unmarshal(p, &result) }, func([]byte) {})
			if code != 0 || result.Verdict != tc.verdict {
				t.Fatalf("evaluate exit=%d prediction=%+v want verdict %q", code, result, tc.verdict)
			}
			if proposedWrites != beforeWrites {
				t.Fatalf("evaluate ran proposed write %d times", proposedWrites-beforeWrites)
			}
			if code = b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); !slices.Contains(tc.actCodes, code) {
				t.Fatalf("act exit %d, expected one of %v", code, tc.actCodes)
			}
			if proposedWrites > beforeWrites+1 {
				t.Fatalf("act dispatched proposed write %d times", proposedWrites-beforeWrites)
			}
		})
	}
}

func TestEvaluateExpiredPendingAndExactAllowMatchActWithoutRewriting(t *testing.T) {
	for _, kind := range []string{"pending cooldown", "lapsed exact grant"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := windowFixture(t)
			req := writeReq()
			if code := b.ServeContext(context.Background(), req, paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func([]byte) {}); code != ExitTempFail {
				t.Fatalf("seed request exit %d", code)
			}
			requests, _, err := b.store.List()
			if err != nil || len(requests) != 1 {
				t.Fatalf("requests=%+v err=%v", requests, err)
			}
			q := requests[0]
			var oldGrant brokerstore.Grant
			if kind == "lapsed exact grant" {
				oldGrant, err = b.store.Decide(q.ID, "pending", brokerstore.Decision{})
				if err != nil {
					t.Fatal(err)
				}
				b.store.Now = func() time.Time { return oldGrant.Expires.Add(time.Second) }
			} else {
				b.store.Now = func() time.Time { return q.Expires().Add(5 * time.Minute) }
			}
			qPath := filepath.Join(b.store.Dir, "requests", q.ID+".json")
			before, err := os.ReadFile(qPath)
			if err != nil {
				t.Fatal(err)
			}
			var prediction evaluation
			code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: req.Argv, Repo: req.Repo},
				paths.JailShortHash(b.authority.scope.Container), func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {})
			if code != 0 || prediction.Verdict != "refused" {
				t.Fatalf("expired %s: exit=%d prediction=%+v", kind, code, prediction)
			}
			after, err := os.ReadFile(qPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("evaluate rewrote expired %s record: %v", kind, err)
			}
			if code = b.ServeContext(context.Background(), req, paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func([]byte) {}); code == 0 {
				t.Fatalf("act allowed expired %s", kind)
			}
		})
	}
}

func TestEvaluateMergedEntryCleansUpBeforeExactGrantDispatch(t *testing.T) {
	b, _ := windowFixture(t)
	req := writeReq()
	jail := paths.JailShortHash(b.authority.scope.Container)
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed request exit %d", code)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	if _, err := b.store.Decide(requests[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	entry, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace,
		Repo: "github.com/o/r", Kind: brokerstore.WorkingPR, Resource: "#1", By: GrantCreatePR})
	if err != nil {
		t.Fatal(err)
	}
	proposed := 0
	b.execute = func(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
		if slices.Equal(argv, []string{"api", "repos/o/r/issues/1"}) {
			out([]byte(`{"number":1,"pull_request":{"merged_at":"2026-11-04T00:00:00Z"}}`))
			return RunResult{Exit: 0}
		}
		proposed++
		return RunResult{Exit: 0}
	}
	path := filepath.Join(b.store.Dir, "working", entry.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("evaluate=%+v exit=%d", prediction, code)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("evaluate cleaned working-set record: %v", err)
	}
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != 0 || proposed != 1 {
		t.Fatalf("act exit=%d proposed writes=%d", code, proposed)
	}
	_, _, working, _, err := b.store.InspectContext(context.Background())
	if err != nil || len(working) != 1 || working[0].ID != entry.ID || working[0].Live() {
		t.Fatalf("act did not end merged working entry: working=%+v err=%v", working, err)
	}
}

func TestEvaluateRefusesBrokerHandledUnknownAuthStatusJSONField(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	req := Request{Mode: "evaluate", Argv: []string{"auth", "status", "--json=login"}}
	var result evaluation
	code := f.b.ServeContext(context.Background(), req, "jail-1", func(p []byte) { _ = json.Unmarshal(p, &result) }, func([]byte) {})
	if code != 0 || result.Verdict != "refused" {
		t.Fatalf("evaluate exit=%d prediction=%+v", code, result)
	}
	if code = f.b.ServeContext(context.Background(), Request{Argv: req.Argv}, "jail-1", func([]byte) {}, func([]byte) {}); code != 1 {
		t.Fatalf("act exit %d, want broker refusal for unsupported auth status field", code)
	}
}

func addEvaluationWorking(t *testing.T, b *Broker, kind string, expires time.Time) {
	t.Helper()
	if _, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: "github.com/o/r", Kind: kind, Resource: "#1", By: GrantCreatePR, Expires: expires}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluatePrefersLiveExactGrantOverExpiredExactGrant(t *testing.T) {
	first, _ := windowFixture(t)
	req := writeReq()
	if code := first.ServeContext(context.Background(), req, paths.JailShortHash(first.authority.scope.Container), func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("first request exit %d", code)
	}
	requests, _, err := first.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("first requests=%+v err=%v", requests, err)
	}
	base := time.Now().UTC()
	first.store.Now = func() time.Time { return base }
	second, _ := relaunchBroker(t, "second-exact", "second-exact-container", first.workspace)
	second.store.Now = func() time.Time { return base }
	secondReq := requests[0]
	secondReq.ID, secondReq.Jail, secondReq.Start = "", paths.JailShortHash(second.authority.scope.Container), second.startID
	secondReq.LaunchID, secondReq.Digest, secondReq.State = second.authority.scope.LaunchID, "", ""
	filed, err := second.store.FileContext(context.Background(), secondReq)
	if err != nil {
		t.Fatal(err)
	}
	oldGrant, err := first.store.Decide(requests[0].ID, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	second.store.Now = func() time.Time { return base.Add(30 * time.Second) }
	if _, err := second.store.Decide(filed.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	later := oldGrant.Expires.Add(time.Second)
	first.store.Now = func() time.Time { return later }
	second.store.Now = func() time.Time { return later }
	requests, _, err = first.store.List()
	if err != nil || len(requests) != 2 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	jail := paths.JailShortHash(second.authority.scope.Container)
	var prediction evaluation
	if code := second.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("evaluate with expired-first/live-second grants: exit=%d prediction=%+v", code, prediction)
	}
	if code := second.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != 0 {
		t.Fatalf("act did not spend other live exact grant: exit=%d", code)
	}
}

func TestEvaluateUsesOneLockedGrantAndTimeView(t *testing.T) {
	b, _ := windowFixture(t)
	req := writeReq()
	jail := paths.JailShortHash(b.authority.scope.Container)
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitTempFail {
		t.Fatalf("seed request exit %d", code)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	base := time.Now().UTC()
	b.store.Now = func() time.Time { return base }
	grant, err := b.store.Decide(requests[0].ID, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	before, after := grant.Expires.Add(-time.Second), grant.Expires.Add(time.Second)
	var reads atomic.Int32
	b.store.Now = func() time.Time {
		if reads.Add(1) == 1 {
			return before
		}
		return after
	}
	var prediction evaluation
	if code := b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: req.Argv, Repo: req.Repo}, jail,
		func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {}); code != 0 || prediction.Verdict != "runs" {
		t.Fatalf("evaluate crossed grant expiry between reads: exit=%d prediction=%+v clock reads=%d before=%s expiry=%s", code, prediction, reads.Load(), before, grant.Expires)
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("evaluate read store clock %d times, want one locked view", got)
	}
	if code := b.ServeContext(context.Background(), req, jail, func([]byte) {}, func([]byte) {}); code != ExitNoPerm {
		t.Fatalf("act after expiry exit=%d, want lapsed-grant refusal", code)
	}
}

func TestAuthStatusEmptyJSONFieldsRefuseInEvaluateAndAct(t *testing.T) {
	for _, fields := range [][]string{{"--json=hosts,"}, {"--json", "hosts,"}, {"--json=,hosts"}, {"--json="}, {"--json", ""}, {"--json"}} {
		t.Run(strings.Join(fields, "_"), func(t *testing.T) {
			f := newBrokerFixture(t, "2.101.0")
			argv := append([]string{"auth", "status"}, fields...)
			var prediction evaluation
			code := f.b.ServeContext(context.Background(), Request{Mode: "evaluate", Argv: argv}, "jail-1",
				func(p []byte) { _ = json.Unmarshal(p, &prediction) }, func([]byte) {})
			if code != 0 || prediction.Verdict != "refused" {
				t.Fatalf("evaluate %q: code=%d prediction=%+v", argv, code, prediction)
			}
			if code = f.b.ServeContext(context.Background(), Request{Argv: argv}, "jail-1", func([]byte) {}, func([]byte) {}); code != 1 && code != ExitUsage {
				t.Fatalf("act %q exit=%d, want invalid-field or parser refusal", argv, code)
			}
		})
	}
}
