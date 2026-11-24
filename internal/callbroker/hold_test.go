package callbroker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// holdGuard is a failure guard that turns a hang into a red test; no test passes on it.
const holdGuard = 3 * time.Minute

// fakeHold replaces the hold's clocks. Nothing waits on wall time: the test says when the
// hold budget has passed (expire), when the store should be read again (tick), and when a
// phase used up its whole budget (expireBudget).
type fakeHold struct {
	mu      sync.Mutex
	expires []chan time.Time
	ticks   []chan time.Time
	cancels []context.CancelFunc
	budgets []time.Duration
	started chan int      // the hold began: the request was filed first
	ended   chan struct{} // a hold returned
}

func newFakeHold() *fakeHold {
	return &fakeHold{started: make(chan int, 16), ended: make(chan struct{}, 16)}
}

func (f *fakeHold) timers() holdTimers {
	return holdTimers{
		Expire: func(time.Duration) (<-chan time.Time, func()) {
			c := make(chan time.Time, 1)
			f.mu.Lock()
			f.expires = append(f.expires, c)
			f.mu.Unlock()
			return c, func() {}
		},
		Poll: func() (<-chan time.Time, func()) {
			c := make(chan time.Time, 1)
			f.mu.Lock()
			f.ticks = append(f.ticks, c)
			n := len(f.ticks) - 1
			f.mu.Unlock()
			f.started <- n
			return c, func() { f.ended <- struct{}{} }
		},
		Context: func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(parent)
			f.mu.Lock()
			f.budgets = append(f.budgets, d)
			f.cancels = append(f.cancels, cancel)
			f.mu.Unlock()
			return ctx, cancel
		},
	}
}

func (f *fakeHold) fire(list *[]chan time.Time, n int) {
	f.mu.Lock()
	c := (*list)[n]
	f.mu.Unlock()
	c <- time.Time{}
}

func (f *fakeHold) tick(n int)   { f.fire(&f.ticks, n) }
func (f *fakeHold) expire(n int) { f.fire(&f.expires, n) }

// expireBudget ends the n-th phase context as if its whole budget had passed.
func (f *fakeHold) expireBudget(n int) {
	f.mu.Lock()
	cancel := f.cancels[n]
	f.mu.Unlock()
	cancel()
}

func (f *fakeHold) seenBudgets() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.budgets...)
}

func (f *fakeHold) waitStarted(t *testing.T) int {
	t.Helper()
	select {
	case n := <-f.started:
		return n
	case <-time.After(holdGuard):
		t.Fatal("no call began holding for an answer")
		return -1
	}
}

func (f *fakeHold) waitEnded(t *testing.T) {
	t.Helper()
	select {
	case <-f.ended:
	case <-time.After(holdGuard):
		t.Fatal("the hold never ended")
	}
}

// frontResult is one call's exit status and what it printed.
type frontResult struct {
	rc  int
	out string
}

// selectedCLIFront serves handler on a hostservice socket behind a real endpoint front, so a
// call reaches Handle with a host-asserted jail id and a live connection. open writes one
// request frame and returns the connection; call also reads the reply.
func selectedCLIFront(t *testing.T, handler hostservice.Handler) (open func(map[string]any) net.Conn, call func(map[string]any) frontResult) {
	t.Helper()
	root := shortCallBrokerTempDir(t)
	socket := filepath.Join(root, "broker.sock")
	stop := make(chan struct{})
	upstream := make(chan error, 1)
	go func() { upstream <- hostservice.ServeFrontedUnix(handler, socket, stop) }()
	t.Cleanup(func() {
		close(stop)
		select {
		case err := <-upstream:
			if err != nil {
				t.Errorf("upstream: %v", err)
			}
		case <-time.After(holdGuard):
			t.Error("upstream failed to stop")
		}
	})
	return connectFront(t, root, socket)
}

// connectFront puts a real endpoint front, named "launch" as a launch's is, before the upstream
// socket and returns a client for it. The front stops with the test.
func connectFront(t *testing.T, root, socket string) (open func(map[string]any) net.Conn, call func(map[string]any) frontResult) {
	t.Helper()
	endpointDir := filepath.Join(root, "launch")
	if err := os.Mkdir(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(endpointDir, "broker.endpoint")
	stop := make(chan struct{})
	front := make(chan error, 1)
	t.Cleanup(func() {
		close(stop)
		select {
		case err := <-front:
			if err != nil {
				t.Errorf("front: %v", err)
			}
		case <-time.After(holdGuard):
			t.Error("front failed to stop")
		}
	})
	deadline := time.Now().Add(holdGuard)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upstream did not bind")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { front <- svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, stop) }()
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("front did not bind")
		}
		time.Sleep(time.Millisecond)
	}
	open = func(frame map[string]any) net.Conn {
		t.Helper()
		conn, err := svcendpoint.Dial(endpoint, holdGuard)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := frameproto.WriteRequest(conn, payload); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	call = func(frame map[string]any) frontResult {
		t.Helper()
		conn := open(frame)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(holdGuard))
		var out strings.Builder
		for {
			f, err := frameproto.ReadFrame(conn)
			if err != nil {
				t.Errorf("reading the reply: %v", err)
				return frontResult{rc: -1, out: out.String()}
			}
			if f.StreamID == frameproto.StreamExit {
				rc, err := frameproto.ExitCode(f.Payload)
				if err != nil {
					t.Error(err)
				}
				return frontResult{rc: rc, out: out.String()}
			}
			out.Write(f.Payload)
		}
	}
	return open, call
}

// async runs call in a goroutine and returns the channel its result arrives on.
func async(call func(map[string]any) frontResult, frame map[string]any) <-chan frontResult {
	done := make(chan frontResult, 1)
	go func() { done <- call(frame) }()
	return done
}

func waitResult(t *testing.T, done <-chan frontResult) frontResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(holdGuard):
		t.Fatal("the call never returned")
		return frontResult{}
	}
}

func lifetimeOwner(f *onceFixture) brokercore.LifetimeOwner {
	return brokercore.LifetimeOwner{Name: "test-broker", Start: f.h.start, Store: f.store, Log: io.Discard}
}

func actFrame(argv ...string) map[string]any {
	return map[string]any{"op": "act", "argv": argv}
}

func onlyPending(t *testing.T, f *onceFixture) brokerstore.Request {
	t.Helper()
	pending := f.pending(t)
	if len(pending) != 1 {
		t.Fatalf("want one request: %+v", pending)
	}
	return pending[0]
}

func TestHoldBudgetsAreOneContract(t *testing.T) {
	if decideBudget != 30*time.Second || holdBudget != 30*time.Second || executeBudget != 30*time.Second {
		t.Fatalf("budgets: decide %v hold %v execute %v", decideBudget, holdBudget, executeBudget)
	}
	margin := connectionMargin
	if connectionBudget != 100*time.Second || connectionBudget-margin != decideBudget+holdBudget+executeBudget {
		t.Fatalf("a front connection must outlive the three budgets by the margin: %v", connectionBudget)
	}
	if forwardDialBudget != 5*time.Second {
		t.Fatalf("dial %v", forwardDialBudget)
	}
	now := time.Unix(1000, 0)
	if got := connectionDeadline(now); !got.Equal(now.Add(100 * time.Second)) {
		t.Fatalf("connection deadline %v", got)
	}
}

// D9, D13 and the hold rules of design 6: an act that asks continues in place when answered,
// whatever second the answer lands at, and its execute budget is its own.
func TestGenericHoldContinuesInPlace(t *testing.T) {
	t.Run("answered at second 29 keeps a full execute budget", func(t *testing.T) {
		f := selectedOnceFixture(t)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", "a1"))
		n := fake.waitStarted(t)
		// The request is filed before the hold begins; the decide budget is now spent.
		fake.expireBudget(0)
		request := onlyPending(t, f)
		if _, err := f.store.Decide(request.ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		fake.tick(n)
		if r := waitResult(t, done); r.rc != 0 || f.effects(t) != 1 {
			t.Fatalf("an answered hold did not continue: %+v effects=%d", r, f.effects(t))
		}
		if got := fake.seenBudgets(); len(got) != 2 || got[0] != decideBudget || got[1] != executeBudget {
			t.Fatalf("the resumed call needs its own execute budget after the decide budget: %v", got)
		}
		snap, err := f.store.Snapshot()
		if err != nil || len(snap.Grants) != 1 || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 || len(snap.Requests) != 1 {
			t.Fatalf("one answer, one spend, no second request: %+v %v", snap, err)
		}
	})
	t.Run("an answer recorded as the budget passes is still taken", func(t *testing.T) {
		f := selectedOnceFixture(t)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", "a1"))
		n := fake.waitStarted(t)
		if _, err := f.store.Decide(onlyPending(t, f).ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		fake.expire(n)
		if r := waitResult(t, done); r.rc != 0 || f.effects(t) != 1 {
			t.Fatalf("the budget bounds the wait, not the read: %+v effects=%d", r, f.effects(t))
		}
	})
	t.Run("no answer gives pending 75 with the id", func(t *testing.T) {
		f := selectedOnceFixture(t)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", "a1"))
		n := fake.waitStarted(t)
		fake.expire(n)
		r := waitResult(t, done)
		request := onlyPending(t, f)
		if r.rc != 75 || !strings.Contains(r.out, request.ID) || request.State != "pending" || f.effects(t) != 0 {
			t.Fatalf("an unanswered hold: %+v request=%+v effects=%d", r, request, f.effects(t))
		}
	})
	t.Run("denied is refused with the note", func(t *testing.T) {
		f := selectedOnceFixture(t)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", "a1"))
		n := fake.waitStarted(t)
		if _, err := f.store.Decide(onlyPending(t, f).ID, "pending", brokerstore.Decision{Deny: true, DenialNote: "not today"}); err != nil {
			t.Fatal(err)
		}
		fake.tick(n)
		if r := waitResult(t, done); r.rc != 64 || !strings.Contains(r.out, "not today") || f.effects(t) != 0 {
			t.Fatalf("a denied hold: %+v effects=%d", r, f.effects(t))
		}
	})
	t.Run("a second call progresses during the hold", func(t *testing.T) {
		f := selectedOnceFixtureWithStanding(t, false)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		held := async(call, actFrame("update", "a1"))
		n := fake.waitStarted(t)
		// The first call is holding; the source lock is free, so a read completes meanwhile.
		if r := waitResult(t, async(call, actFrame("read"))); r.rc != 0 || f.reads(t) != 1 {
			t.Fatalf("a read could not progress while another call held: %+v reads=%d", r, f.reads(t))
		}
		select {
		case r := <-held:
			t.Fatalf("the holding call returned early: %+v", r)
		default:
		}
		fake.expire(n)
		if r := waitResult(t, held); r.rc != 75 {
			t.Fatalf("held call: %+v", r)
		}
	})
}

// A client that disconnects leaves its request pending and runs nothing, even if the host
// answers afterwards.
func TestGenericHoldDisconnectLeavesRequestPending(t *testing.T) {
	f := selectedOnceFixture(t)
	fake := newFakeHold()
	f.h.timers = fake.timers()
	open, _ := selectedCLIFront(t, f.h.Handle)
	conn := open(actFrame("update", "a1"))
	fake.waitStarted(t)
	request := onlyPending(t, f)
	_ = conn.Close()
	fake.waitEnded(t)
	if _, err := f.store.Decide(request.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	snap, err := f.store.Snapshot()
	if err != nil || f.effects(t) != 0 || len(snap.Grants) != 1 || snap.Grants[0].Uses != 1 {
		t.Fatalf("a disconnected call ran or spent: effects=%d grants=%+v %v", f.effects(t), snap.Grants, err)
	}
}

// A stopping broker ends the hold with "closed"; Store.End then closes the request itself.
func TestGenericHoldShutdownClosesTheRequest(t *testing.T) {
	f := selectedOnceFixture(t)
	fake := newFakeHold()
	f.h.timers = fake.timers()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	f.h.shutdown = ctx
	_, call := selectedCLIFront(t, f.h.Handle)
	done := async(call, actFrame("update", "a1"))
	fake.waitStarted(t)
	request := onlyPending(t, f)
	stop()
	if r := waitResult(t, done); r.rc != 64 || !strings.Contains(r.out, request.ID) || f.effects(t) != 0 {
		t.Fatalf("a stopped hold: %+v effects=%d", r, f.effects(t))
	}
	f.h.lifetimes.EndAll(lifetimeOwner(f))
	closed, err := f.store.Status(request.Jail, request.Start, request.ID)
	if err != nil || closed.State != "refused" || closed.Reason != brokerstore.ReasonBrokerStopped {
		t.Fatalf("Store.End did not close the request: %+v %v", closed, err)
	}
}

// Two identical calls join one request; one exact answer runs one and the other learns it was
// used (72), without filing a second request.
func TestGenericHoldJoinedCallsRaceForOneAnswer(t *testing.T) {
	f := selectedOnceFixture(t)
	fake := newFakeHold()
	f.h.timers = fake.timers()
	_, call := selectedCLIFront(t, f.h.Handle)
	first := async(call, actFrame("update", "a1"))
	n1 := fake.waitStarted(t)
	second := async(call, actFrame("update", "a1"))
	n2 := fake.waitStarted(t)
	request := onlyPending(t, f)
	if _, err := f.store.Decide(request.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	fake.tick(n1)
	fake.tick(n2)
	a, b := waitResult(t, first), waitResult(t, second)
	codes := map[int]int{a.rc: 1}
	codes[b.rc]++
	if codes[0] != 1 || codes[72] != 1 || f.effects(t) != 1 {
		t.Fatalf("joined calls: %+v %+v effects=%d", a, b, f.effects(t))
	}
	if snap, err := f.store.Snapshot(); err != nil || len(snap.Requests) != 1 {
		t.Fatalf("the loser filed a request: %+v %v", snap.Requests, err)
	}
}

// watchDisconnect reports the peer's close and releases the connection on stop.
func TestWatchDisconnect(t *testing.T) {
	t.Run("peer close", func(t *testing.T) {
		server, client := net.Pipe()
		gone, stop := watchDisconnect(server)
		defer stop()
		_ = client.Close()
		select {
		case <-gone:
		case <-time.After(holdGuard):
			t.Fatal("a closed peer was not reported")
		}
	})
	t.Run("stop leaves the connection usable", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		gone, stop := watchDisconnect(server)
		stop()
		select {
		case <-gone:
			t.Fatal("stopping a watch reported a disconnect")
		default:
		}
		go func() { _, _ = client.Write([]byte("x")) }()
		buf := make([]byte, 1)
		if n, err := server.Read(buf); err != nil || n != 1 {
			t.Fatalf("the watch swallowed or broke the connection: %d %v", n, err)
		}
	})
}

// The confined forwarder gives its connection the fixed 100 s, from the clock it was handed.
func TestForwardCLIConnectionDeadlineIsTheGenericBudget(t *testing.T) {
	server, client := net.Pipe()
	recorded := make(chan time.Time, 1)
	restoreDial, restoreNow := dialForward, forwardNow
	t.Cleanup(func() { dialForward, forwardNow = restoreDial, restoreNow })
	fixed := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	forwardNow = func() time.Time { return fixed }
	dialForward = func(string, time.Duration) (net.Conn, error) {
		return &deadlineRecorder{Conn: client, recorded: recorded}, nil
	}
	go func() {
		defer server.Close()
		if _, err := frameproto.ReadRequestBytes(server); err != nil {
			return
		}
		_, _ = frameproto.WriteExit(server, 0)
	}()
	getenv := func(string) string { return "endpoint" }
	if rc := ForwardCLI("tasks", "act", []string{"read"}, nil, &strings.Builder{}, &strings.Builder{}, getenv); rc != 0 {
		t.Fatalf("forward rc=%d", rc)
	}
	select {
	case got := <-recorded:
		if !got.Equal(fixed.Add(100 * time.Second)) {
			t.Fatalf("connection deadline %v, want the injected clock plus 100s", got)
		}
	default:
		t.Fatal("the forwarder set no connection deadline")
	}
}

type deadlineRecorder struct {
	net.Conn
	recorded chan time.Time
}

func (d *deadlineRecorder) SetDeadline(t time.Time) error {
	select {
	case d.recorded <- t:
	default:
	}
	return d.Conn.SetDeadline(t)
}

// The HTTP front's request budget is the same fixed connection budget, unless a test narrows it.
func TestHTTPFrontRequestTimeoutIsTheGenericBudget(t *testing.T) {
	if got := (HTTPFront{}).requestTimeout(); got != connectionBudget {
		t.Fatalf("default request timeout %v", got)
	}
	if got := (HTTPFront{Timeout: time.Second}).requestTimeout(); got != time.Second {
		t.Fatalf("a configured timeout was ignored: %v", got)
	}
}

// The selected HTTP front holds the same way: filed once, answered while it holds, then the
// original body is classified again and sent exactly once.
func TestHTTPHoldContinuesInPlace(t *testing.T) {
	const original = `{"operationName":"Rename"}`
	var writes int32
	var mu sync.Mutex
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			mu.Lock()
			writes++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	fake := newFakeHold()
	h.timers = fake.timers()
	forward := selectedHTTPHostFrame(t, h)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	type reply struct {
		rc  int
		msg string
	}
	done := make(chan reply, 1)
	go func() {
		rc, msg := forward("act", call)
		done <- reply{rc, msg}
	}()
	n := fake.waitStarted(t)
	fake.expireBudget(0)
	request := onlyPending(t, f)
	if _, err := f.store.Decide(request.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	fake.tick(n)
	select {
	case r := <-done:
		mu.Lock()
		got := writes
		mu.Unlock()
		if r.rc != 0 || got != 1 {
			t.Fatalf("an answered HTTP hold did not continue: %+v writes=%d", r, got)
		}
	case <-time.After(holdGuard):
		t.Fatal("the HTTP call never returned")
	}
	if got := fake.seenBudgets(); len(got) != 2 || got[0] != decideBudget || got[1] != executeBudget {
		t.Fatalf("budgets %v", got)
	}
	if snap, err := f.store.Snapshot(); err != nil || len(snap.Requests) != 1 || len(snap.Grants) != 1 || snap.Grants[0].Uses != 0 {
		t.Fatalf("one request, one spend: %+v %v", snap, err)
	}
}
