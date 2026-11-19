package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
)

// `--json` is one document of pending requests and live grants, with "schema": 1.
func TestApproveJSONListsOpenRecords(t *testing.T) {
	s, r := approvalFixture(t)
	s.Source = "github"
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{"--json"}, false, s, &out, &errs); rc != 0 {
		t.Fatal(rc, errs.String())
	}
	var doc struct {
		Schema   int `json:"schema"`
		Requests []struct {
			ID, Source, State, Repo, Command string
		} `json:"requests"`
		Grants []any `json:"grants"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("%v %q", err, out.String())
	}
	if doc.Schema != 1 || len(doc.Requests) != 1 || doc.Requests[0].ID != r.ID || doc.Requests[0].Source != "github" ||
		doc.Requests[0].Command != "pr merge 1" || doc.Grants == nil {
		t.Fatalf("%+v", doc)
	}
	if rc := approveMain(context.Background(), []string{"--json", r.ID}, false, s, &out, &errs); rc != approveExitUsage {
		t.Fatalf("--json with a decision: %d", rc)
	}
}

// The stable exit codes, each message naming a next step.
func TestApproveStableExitCodes(t *testing.T) {
	s, r := approvalFixture(t)
	run := func(args ...string) (int, string) {
		var out, errs bytes.Buffer
		rc := approveMain(context.Background(), args, false, s, &out, &errs)
		return rc, errs.String()
	}
	if rc, msg := run(r.ID, "--for", "2h"); rc != approveExitPolicy || !strings.Contains(msg, "Next: Allow it once or choose a shorter --for") {
		t.Fatalf("policy %d %q", rc, msg)
	}
	if rc, msg := run("r-00000000000000000000000000000000"); rc != approveExitUnknownID || !strings.Contains(msg, "Next: run `yolo approve` to list") {
		t.Fatalf("unknown %d %q", rc, msg)
	}
	if rc, msg := run("--revoke", "g-00000000000000000000000000000000"); rc != approveExitUnknownID || !strings.Contains(msg, "Next:") {
		t.Fatalf("unknown grant %d %q", rc, msg)
	}
	if rc, _ := run(r.ID, "--deny"); rc != 0 {
		t.Fatal(rc)
	}
	if rc, msg := run(r.ID); rc != approveExitAlreadyDecided || !strings.Contains(msg, "already denied") ||
		!strings.Contains(msg, "Next: run `yolo approve` to see its state") {
		t.Fatalf("decided %d %q", rc, msg)
	}
	if rc, _ := run("--nonsense"); rc != approveExitUsage {
		t.Fatal(rc)
	}
	// A request the host can no longer vouch for (its scope file is gone) is its own code,
	// and its next step is not to pick a shorter window.
	stale, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/w", Argv: []string{"pr", "merge", "2"}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}})
	if err != nil {
		t.Fatal(err)
	}
	s.Revalidate = func(brokerstore.Request) (brokerstore.Policy, error) {
		return brokerstore.Policy{}, errors.New("read scope file: no such file or directory")
	}
	if rc, msg := run(stale.ID); rc != approveExitNotConfirmed || strings.Contains(msg, "shorter --for") || !strings.Contains(msg, "Next: deny it") {
		t.Fatalf("not confirmed %d %q", rc, msg)
	}
	// The numbers are the contract a UI keys on.
	if approveExitFailed != 1 || approveExitUsage != 2 || approveExitAlreadyDecided != 3 || approveExitUnknownID != 4 || approveExitPolicy != 5 || approveExitNotConfirmed != 6 {
		t.Fatal("an exit code moved")
	}
}

// waitLines waits until buf holds at least n lines.
func waitLines(t *testing.T, buf *syncBuffer, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := buf.String()
		lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		if s != "" && len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d lines, have %q", n, s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fastWatch(t *testing.T) {
	old := approveWatchInterval
	approveWatchInterval = 5 * time.Millisecond
	t.Cleanup(func() { approveWatchInterval = old })
}

// `--watch --json` opens with the state, then one object per event, until interrupted.
func TestApproveWatchJSON(t *testing.T) {
	fastWatch(t)
	s, r := approvalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errs syncBuffer
	done := make(chan int)
	go func() { done <- approveMain(ctx, []string{"--watch", "--json"}, false, s, &out, &errs) }()
	waitLines(t, &out, 1)
	g, err := s.Decide(r.ID, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	waitLines(t, &out, 2)
	if err := s.Revoke(g.ID); err != nil {
		t.Fatal(err)
	}
	lines := waitLines(t, &out, 3)
	cancel()
	if rc := <-done; rc != 0 {
		t.Fatal(rc, errs.String())
	}
	var events []string
	for _, l := range lines {
		var v struct {
			Schema int    `json:"schema"`
			Event  string `json:"event"`
		}
		if err := json.Unmarshal([]byte(l), &v); err != nil || v.Schema != 1 {
			t.Fatalf("%v %q", err, l)
		}
		events = append(events, v.Event)
	}
	if strings.Join(events, ",") != "state,request-decided,grant-revoked" {
		t.Fatalf("events %v", events)
	}
	if !strings.Contains(lines[0], r.ID) {
		t.Fatalf("the opening state misses the pending request: %s", lines[0])
	}
}

// Plain `--watch` prints the list, then one readable line per event.
func TestApproveWatchText(t *testing.T) {
	fastWatch(t)
	s, r := approvalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errs syncBuffer
	done := make(chan int)
	go func() { done <- approveMain(ctx, []string{"--watch"}, false, s, &out, &errs) }()
	waitLines(t, &out, 2)
	if _, err := s.Decide(r.ID, "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	lines := waitLines(t, &out, 3)
	cancel()
	if rc := <-done; rc != 0 {
		t.Fatal(rc)
	}
	if !strings.Contains(lines[2], "request-decided") || !strings.Contains(lines[2], r.ID) ||
		!strings.HasSuffix(lines[2], ": denied") || strings.Contains(out.String(), "\x1b") ||
		!strings.Contains(lines[1], "action identity: command "+r.Digest) {
		t.Fatalf("%q", out.String())
	}
	if !strings.Contains(errs.String(), "Ctrl-C") {
		t.Fatalf("%q", errs.String())
	}
}

// approvalWatchOutput wakes a test when approveWatch writes, so the test can wait for its
// opening snapshot and each subsequent event without scheduling sleeps.
type approvalWatchOutput struct {
	mu               sync.Mutex
	b                bytes.Buffer
	changed          chan struct{}
	writeCalls       int
	bytesWritten     int
	coalescedWakeups int
}

func newApprovalWatchOutput() *approvalWatchOutput {
	return &approvalWatchOutput{changed: make(chan struct{}, 1)}
}

func (o *approvalWatchOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.b.Write(p)
	if n > 0 {
		o.writeCalls++
		o.bytesWritten += n
	}
	o.mu.Unlock()
	select {
	case o.changed <- struct{}{}:
	default:
		o.mu.Lock()
		o.coalescedWakeups++
		o.mu.Unlock()
	}
	return n, err
}

func (o *approvalWatchOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *approvalWatchOutput) writeStats() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return fmt.Sprintf("write-calls=%d bytes=%d coalesced-wakeups=%d", o.writeCalls, o.bytesWritten, o.coalescedWakeups)
}

func waitApprovalWatchOutput(t *testing.T, out *approvalWatchOutput, want func(string) bool) string {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if got := out.String(); want(got) {
			return got
		}
		select {
		case <-out.changed:
		case <-timer.C:
			t.Fatalf("timed out waiting for approval watch output: %q", out.String())
		}
	}
}

// waitProductionRequestFiled keeps the production request event assertion and its original guard,
// while identifying which boundary failed if the durable request is not written to the watch.
func waitProductionRequestFiled(t *testing.T, out *approvalWatchOutput, store *brokerstore.Store,
	before brokerstore.Snapshot, outputStart, stdinDigest string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if got := out.String(); len(got) >= len(outputStart) && strings.Contains(got[len(outputStart):], "request-filed") {
			return
		}
		select {
		case <-out.changed:
		case <-timer.C:
			type snapshotResult struct {
				snapshot brokerstore.Snapshot
				err      error
			}
			result := make(chan snapshotResult, 1)
			go func() {
				snapshot, err := store.Snapshot()
				result <- snapshotResult{snapshot: snapshot, err: err}
			}()
			var after brokerstore.Snapshot
			snapshotStatus := "available"
			select {
			case got := <-result:
				after = got.snapshot
				if got.err != nil {
					snapshotStatus = got.err.Error()
				}
			case <-time.After(2 * time.Second):
				snapshotStatus = "snapshot diagnostic did not return within 2s"
			}
			matching := "not present"
			for _, request := range after.Requests {
				if request.StdinDigest == stdinDigest {
					matching = request.ID + ":" + request.State
					break
				}
			}
			var diff []string
			for _, event := range brokerstore.Diff(before, after) {
				id, state := "", ""
				if event.Request != nil {
					id, state = event.Request.ID, event.Request.State
				}
				diff = append(diff, event.Kind+":"+id+":"+state)
			}
			t.Fatalf("timed out waiting for production request-filed event: store=%q expected_stdin_sha256=%s durable=%q snapshot=%s diff=%v output-stats={%s} output=%q", store.Dir, stdinDigest, matching, snapshotStatus, diff, out.writeStats(), out.String())
		}
	}
}

// Live text events identify the command and actual stdin bytes submitted through the real
// forwarder and production broker, without exposing either body.
func TestApproveWatchTextLiveProductionRequestsIncludeCompleteActionIdentity(t *testing.T) {
	fastWatch(t)
	fixture := newProductionGitHubFixture(t, "", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := newApprovalWatchOutput()
	done := make(chan int, 1)
	go func() {
		done <- approveAll(ctx, []string{"--watch"}, false, []*brokerstore.Store{fixture.store}, out, out)
	}()
	watchStopped := false
	defer func() {
		cancel()
		if !watchStopped {
			<-done
		}
	}()
	initial := waitApprovalWatchOutput(t, out, func(got string) bool {
		return strings.Contains(got, "Watching the store; one line per event.")
	})

	bodies := []string{"private production body one\n", "private production body two\n"}
	requests := make([]brokerstore.Request, 0, len(bodies))
	for _, body := range bodies {
		before, err := fixture.store.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan productionForwardResult, 1)
		go func(body string) {
			code, stdout, stderr := fixture.forwardInput(body, "pr", "comment", "41", "--repo", "o/r", "--body-file", "-")
			finished <- productionForwardResult{code: code, out: stdout, stderr: stderr}
		}(body)

		waitProductionRequestFiled(t, out, fixture.store, before, initial, stdinSHA256([]byte(body)))
		current, _, err := fixture.store.List()
		if err != nil {
			t.Fatal(err)
		}
		var request brokerstore.Request
		for _, q := range current {
			if q.State == "pending" && q.StdinDigest == stdinSHA256([]byte(body)) {
				request = q
				break
			}
		}
		if request.ID == "" {
			t.Fatalf("watch observed request-filed but no matching production request: %+v", current)
		}
		requests = append(requests, request)
		live := out.String()
		observed := strings.Contains(live[len(initial):], "request-filed") && strings.Contains(live[len(initial):], request.ID)
		after, err := fixture.store.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		var diff []string
		for _, event := range brokerstore.Diff(before, after) {
			id, state := "", ""
			if event.Request != nil {
				id, state = event.Request.ID, event.Request.State
			}
			diff = append(diff, event.Kind+":"+id+":"+state)
		}
		t.Logf("approval-watch observation: request=%s state=%s stdin_sha256=%s store=%q diff=%v output-event=%t output-stats={%s}", request.ID, request.State, request.StdinDigest, fixture.store.Dir, diff, observed, out.writeStats())
		if !observed {
			t.Fatalf("production request %s is durable but its request-filed output is missing: %q", request.ID, live[len(initial):])
		}
		if !strings.Contains(live[len(initial):], "command SHA-256 "+request.Digest) ||
			!strings.Contains(live[len(initial):], "stdin SHA-256 "+request.StdinDigest) ||
			strings.Contains(live[len(initial):], body) {
			t.Fatalf("new live text event omits exact identity or exposes the body: %q", live[len(initial):])
		}
		initial = live
		if rc := Main([]string{"yolo", "approve", request.ID, "--deny"}); rc != 0 {
			t.Fatalf("denying live-watch request returned %d", rc)
		}
		select {
		case got := <-finished:
			if got.code != ghbroker.ExitNoPerm {
				t.Fatalf("denied production request returned %d: %q", got.code, got.stderr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("denied production request did not drain")
		}
		if _, err := os.Stat(fixture.calls); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("live-watch denied request dispatched fake gh: %v", err)
		}
	}
	cancel()
	if rc := <-done; rc != 0 {
		t.Fatalf("host approval watch returned %d: %s", rc, out.String())
	}
	watchStopped = true
	if len(requests) != 2 || requests[0].StdinDigest == requests[1].StdinDigest {
		t.Fatalf("live watch did not distinguish both production bodies: %+v", requests)
	}
}

// This isolated-store case pins bounded long-command summary rendering; production caller
// coverage with actual stdin is in TestApproveWatchTextLiveProductionRequestsIncludeCompleteActionIdentity.
func TestApproveWatchTextLiveRequestsIncludeCompleteActionIdentity(t *testing.T) {
	fastWatch(t)
	s := &brokerstore.Store{Dir: t.TempDir(), Source: "github"}
	_, err := s.File(brokerstore.Request{
		Jail: "jail", Start: "baseline", Workspace: "/workspace", Set: "read-write",
		Argv: []string{"pr", "view", "41"}, Repo: "o/r", StdinDigest: "baseline",
		Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Minute, Uses: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out, errs syncBuffer
	done := make(chan int)
	go func() { done <- approveMain(ctx, []string{"--watch"}, false, s, &out, &errs) }()
	waitLines(t, &out, 2)

	requests := make([]brokerstore.Request, 2)
	for i, body := range []string{"private body one", "private body two"} {
		q, err := s.File(brokerstore.Request{
			Jail: "jail", Start: "start", Workspace: "/workspace", Set: "read-write",
			Argv: []string{"pr", "comment", "41", "--body", strings.Repeat("long-argument-", 200)},
			Repo: "o/r", StdinDigest: stdinSHA256([]byte(body)),
			Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Minute, Uses: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		requests[i] = q
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		output := out.String()
		if strings.Contains(output, requests[0].ID) && strings.Contains(output, requests[1].ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch did not emit both newly filed request events: %q", output)
		}
		time.Sleep(5 * time.Millisecond)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	cancel()
	if rc := <-done; rc != 0 {
		t.Fatal(rc, errs.String())
	}
	live := strings.Join(lines[1:], "\n")
	for _, q := range requests {
		if !strings.Contains(live, q.Digest) || !strings.Contains(live, q.StdinDigest) || !strings.Contains(live, "request-filed") {
			t.Fatalf("live text events omit complete action identity for request %s: %q", q.ID, live)
		}
	}
	if requests[0].StdinDigest == requests[1].StdinDigest || strings.Contains(live, "private body") {
		t.Fatalf("live summaries do not safely distinguish bodies: %q", live)
	}
}

func TestApproveWatchJSONOpensWithOneMergedState(t *testing.T) {
	fastWatch(t)
	gh, r := approvalFixture(t)
	gh.Source = "github"
	ws := workingStore(t.TempDir())
	e := addEntry(t, ws, "/home/u/code/app", "refs/heads/agent/x")
	stores := []*brokerstore.Store{gh, ws}
	ctx, cancel := context.WithCancel(context.Background())
	var out, errs syncBuffer
	done := make(chan int)
	go func() { done <- approveAll(ctx, []string{"--watch", "--json"}, false, stores, &out, &errs) }()
	waitLines(t, &out, 1)
	if _, err := gh.Decide(r.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	lines := waitLines(t, &out, 2)
	cancel()
	if rc := <-done; rc != 0 {
		t.Fatal(rc, errs.String())
	}
	var events []string
	for _, l := range lines {
		var v struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("%v %q", err, l)
		}
		events = append(events, v.Event)
	}
	if strings.Join(events, ",") != "state,request-decided" {
		t.Fatalf("events %v, want one state then the decision:\n%s", events, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], r.ID) || !strings.Contains(lines[0], e.ID) {
		t.Fatalf("the opening state lacks a store's records (want %s and %s): %s", r.ID, e.ID, lines[0])
	}
}
