package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/fakeyolo"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/hub"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
)

func TestMain(m *testing.M) {
	if fakeyolo.MaybeRun() {
		return
	}
	os.Exit(m.Run())
}

// guard is the generous failure timeout every wait uses; it only turns a hang into a red test.
const guard = 60 * time.Second

var base = time.Date(2026, 11, 10, 3, 50, 0, 0, time.UTC) // before every recorded event

func td(name string) string {
	p, _ := filepath.Abs(filepath.Join("..", "testdata", name))
	return p
}

func newHub() *hub.Hub {
	return hub.New(model.Options{Reconcile: 15 * time.Second}, func() time.Time { return base })
}

func ctxGuard(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	t.Cleanup(cancel)
	return ctx
}

func lines(t *testing.T, name string, n int) string {
	t.Helper()
	b, err := os.ReadFile(td(name))
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.SplitAfter(string(b), "\n")
	return strings.Join(ls[:n], "")
}

func modeCount(t *testing.T, rec, mode string) int {
	n := 0
	for _, c := range fakeyolo.Calls(t, rec) {
		if c.Mode == mode {
			n++
		}
	}
	return n
}

func runSup(t *testing.T, cfg Config, h *hub.Hub) (stop func()) {
	t.Helper()
	cfg.Now = func() time.Time { return base }
	s := New(cfg, h.Apply)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(guard):
			t.Fatal("Run did not return after cancel: a child outlived the context")
		}
	}
}

func TestSnapshotParsesTheRecordedPopulatedOutputWithArgvOnly(t *testing.T) {
	t.Setenv("YOLO_REQUEST_ID", "r-should-not-reach-the-child")
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec, JSON: []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}}})
	s := New(Config{Yolo: fake, Now: func() time.Time { return base }}, func(model.Input) {})
	res := s.Snapshot(ctxGuard(t))
	ok, isOK := res.Input.(model.SnapshotOK)
	if !isOK || len(ok.State.Requests) != 6 || !ok.FetchStart.Equal(base) || ok.FromWatch {
		t.Fatalf("%+v", res)
	}
	calls := fakeyolo.Calls(t, rec)
	if len(calls) != 1 || strings.Join(calls[0].Args, " ") != "approve --json" {
		t.Fatalf("argv = %v", calls)
	}
	for _, kv := range calls[0].Env {
		if strings.HasPrefix(kv, "YOLO_REQUEST_ID") {
			t.Fatalf("child environment kept %s", kv)
		}
	}
}

func TestSnapshotFailures(t *testing.T) {
	long := model.FailKind("")
	_ = long
	cases := []struct {
		name     string
		run      fakeyolo.Run
		kind     model.FailKind
		contains string
		exit     int
	}{
		{"exit 1 with a message", fakeyolo.Run{Exit: 1, Stderr: "yolo-jail unknown | linux/x86_64 | host\ncould not read the store\n"}, model.FailExit, "exited 1: could not read the store", 1},
		{"in-jail refusal fixture", fakeyolo.Run{Exit: 2, Stderr: mustRead(t, td("injail-approve-refusal.stderr.txt"))}, model.FailExit, "host-only", 2},
		{"schema 2", fakeyolo.Run{StdoutText: `{"schema":2,"requests":[],"grants":[],"working":[]}`}, model.FailUnsupported, "schema 2", 0},
		{"invalid json", fakeyolo.Run{StdoutText: `{not json`}, model.FailUnsupported, "not a schema 1 snapshot", 0},
		{"empty output", fakeyolo.Run{}, model.FailUnsupported, "not a schema 1 snapshot", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := fakeyolo.Install(t, fakeyolo.Script{JSON: []fakeyolo.Run{c.run}})
			res := New(Config{Yolo: fake}, func(model.Input) {}).Snapshot(ctxGuard(t))
			f, ok := res.Input.(model.SnapshotFail)
			if !ok || f.Kind != c.kind || !strings.Contains(f.Message, c.contains) || res.Exit != c.exit {
				t.Fatalf("%+v", res)
			}
			if strings.Contains(f.Message, "yolo-jail unknown |") {
				t.Fatalf("banner not stripped: %q", f.Message)
			}
		})
	}
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotTimeoutAndMissingBinaryAndOversize(t *testing.T) {
	fake := fakeyolo.Install(t, fakeyolo.Script{JSON: []fakeyolo.Run{{Hang: true}}})
	res := New(Config{Yolo: fake, SnapshotTimeout: 200 * time.Millisecond}, func(model.Input) {}).Snapshot(ctxGuard(t))
	if f, ok := res.Input.(model.SnapshotFail); !ok || f.Kind != model.FailTimeout {
		t.Fatalf("timeout: %+v", res)
	}
	res = New(Config{Yolo: filepath.Join(t.TempDir(), "no-such-yolo")}, func(model.Input) {}).Snapshot(ctxGuard(t))
	if !res.NotFound || !strings.Contains(res.Message, "yolo approve --json") {
		t.Fatalf("missing binary: %+v", res)
	}
	fake = fakeyolo.Install(t, fakeyolo.Script{JSON: []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}}})
	res = New(Config{Yolo: fake, MaxLine: 1000}, func(model.Input) {}).Snapshot(ctxGuard(t))
	if f, ok := res.Input.(model.SnapshotFail); !ok || f.Kind != model.FailUnsupported || !strings.Contains(f.Message, "more than 1000 bytes") {
		t.Fatalf("oversize: %+v", res)
	}
}

func TestWatchTranscriptDrivesTheModelAndShutdownKillsTheChild(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{{Stdout: td("watch-1.jsonl"), Block: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true}, h)
	err := h.WaitFor(ctxGuard(t), func(m model.Model) bool {
		return m.Watch == model.WatchRunning && len(m.State.Requests) == 0 && statusOf(m, "r-c31635034bbbaff00f3cb41aaa26cb88") != ""
	})
	if err != nil {
		t.Fatalf("model never converged: %v", err)
	}
	m := h.Model()
	if statusOf(m, "r-5d3e4fef29bc86e3c245192f5792e1db") != model.StExpired {
		t.Fatalf("expired request history = %q", statusOf(m, "r-5d3e4fef29bc86e3c245192f5792e1db"))
	}
	if modeCount(t, rec, "watch") != 1 {
		t.Fatalf("watch started %d times", modeCount(t, rec, "watch"))
	}
	stop()
}

func statusOf(m model.Model, key string) string {
	for _, e := range m.History {
		if e.Key == key {
			return e.Status
		}
	}
	return ""
}

// The recorded gap: the first watch dies after one decision, the successor opens on a state
// that shows none of what happened in between.
func TestRestartAfterAGapReconcilesAndSaysOutcomeNotObserved(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "watch-1-short.jsonl")
	if err := os.WriteFile(short, []byte(lines(t, "watch-1.jsonl", 2)), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON: []fakeyolo.Run{{Stdout: td("approve-json-populated.json")}, {Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{
			{Stdout: short},
			{Stdout: td("watch-2-after-gap.jsonl"), Block: true},
		}})
	h := newHub()
	// The caller's own first snapshot, as the real main takes it.
	first := New(Config{Yolo: fake, Now: func() time.Time { return base }}, h.Apply).Snapshot(ctxGuard(t))
	h.Apply(first.Input)
	if len(h.Model().State.Requests) != 6 {
		t.Fatalf("first snapshot: %d pending", len(h.Model().State.Requests))
	}
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true,
		BackoffInit: 5 * time.Millisecond, BackoffMax: 10 * time.Millisecond}, h)
	err := h.WaitFor(ctxGuard(t), func(m model.Model) bool {
		return m.Watch == model.WatchRunning && len(m.State.Requests) == 0 && len(m.Gaps) >= 1
	})
	if err != nil {
		t.Fatalf("no convergence: %v", err)
	}
	m := h.Model()
	notObserved := 0
	for _, e := range m.History {
		if e.Status == model.StNotObserved {
			notObserved++
		}
	}
	// One decision was seen (or its snapshot raced it); every other request vanished unseen.
	if notObserved < 5 {
		t.Fatalf("not-observed entries = %d, want at least 5; history %+v", notObserved, m.History)
	}
	if len(m.State.Working) != 5 {
		t.Fatalf("model did not take the new snapshot's working set: %d", len(m.State.Working))
	}
	pub := model.Publish(m, base.Add(time.Second), model.Options{Reconcile: 15 * time.Second})
	found := false
	for _, g := range pub.History.Gaps {
		found = found || strings.Contains(g, "history may be missing events")
	}
	if !found {
		t.Fatalf("gap note: %v", pub.History.Gaps)
	}
	// The restart itself triggered a second snapshot.
	if err := h.WaitFor(ctxGuard(t), func(model.Model) bool { return modeCount(t, rec, "json") >= 2 }); err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestSnapshotTriggersCoalesceIntoOneFollowUp(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "gate")
	rec := filepath.Join(dir, "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json"), WaitFile: gate}},
		Watch: []fakeyolo.Run{{Block: true}}})
	h := newHub()
	cfg := Config{Yolo: fake, Reconcile: time.Hour, Now: func() time.Time { return base }}
	s := New(cfg, h.Apply)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	if err := h.WaitFor(ctxGuard(t), func(model.Model) bool { return modeCount(t, rec, "json") == 1 }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		s.Trigger()
	}
	if n := modeCount(t, rec, "json"); n != 1 {
		t.Fatalf("a second --json started while one was in flight: %d", n)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := func(m model.Model) bool { return m.Loaded }
	if err := h.WaitFor(ctxGuard(t), loaded); err != nil {
		t.Fatal(err)
	}
	if err := h.WaitFor(ctxGuard(t), func(model.Model) bool { return modeCount(t, rec, "json") >= 2 }); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if n := modeCount(t, rec, "json"); n != 2 {
		t.Fatalf("ten triggers during one run made %d runs, want 2 (one in flight plus one follow-up)", n)
	}
}

func TestMalformedAndOversizeWatchLinesAreCountedAndTriggerASnapshot(t *testing.T) {
	big := `{"schema":1,"event":"request-filed","summary":"` + strings.Repeat("x", 5000) + `"}`
	good := `{"schema":1,"event":"state","requests":[],"grants":[],"working":[]}`
	out := "{not json\n" + big + "\n\n" + good + "\n"
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{{StdoutText: out, Block: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true, MaxLine: 1024}, h)
	err := h.WaitFor(ctxGuard(t), func(m model.Model) bool {
		return m.Malformed == 2 && m.Watch == model.WatchRunning && modeCount(t, rec, "json") >= 1
	})
	if err != nil {
		t.Fatalf("%v; model %+v", err, h.Model().Malformed)
	}
	stop()
}

func TestSchemaTwoWatchLineMakesTheModelStaleNamingTheSchema(t *testing.T) {
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{StdoutText: `{"schema":2,"requests":[],"grants":[],"working":[]}`}},
		Watch: []fakeyolo.Run{{StdoutText: `{"schema":2,"event":"state","requests":[],"grants":[],"working":[]}` + "\n", Block: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour}, h)
	err := h.WaitFor(ctxGuard(t), func(m model.Model) bool {
		return m.SnapshotErrKind == model.FailUnsupported && strings.Contains(m.SnapshotErr, "schema 2")
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Model().Loaded {
		t.Fatal("schema 2 must not load")
	}
	stop()
}

func TestWatchStderrStoreReadFailureMakesTheModelStale(t *testing.T) {
	ev := `{"schema":1,"event":"grant-ended","source":"git","time":"2026-11-10T03:54:00Z","grant":{"schema":1,"id":"g-1","request":"r-1"}}` + "\n"
	fake := fakeyolo.Install(t, fakeyolo.Script{
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{{StdoutText: ev, StderrAfter: "yolo: reading the store failed: boom\n", Block: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true}, h)
	err := h.WaitFor(ctxGuard(t), func(m model.Model) bool { return strings.Contains(m.WatchWarn, "reading the store failed") })
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestWatchChildIsRestartedWithBackoffAndEachRestartTriggersASnapshot(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{{Exit: 1, Stderr: "boom\n"}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true,
		BackoffInit: 2 * time.Millisecond, BackoffMax: 8 * time.Millisecond}, h)
	err := h.WaitFor(ctxGuard(t), func(model.Model) bool {
		return modeCount(t, rec, "watch") >= 4 && modeCount(t, rec, "json") >= 3
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := h.Model().Watch; w != model.WatchRestarting && w != model.WatchStarting {
		t.Fatalf("watch = %s", w)
	}
	stop()
}

func TestDefaultsMatchTheSpec(t *testing.T) {
	c := Config{}
	c.defaults()
	if c.BackoffInit != time.Second || c.BackoffMax != 30*time.Second || c.BackoffReset != 60*time.Second ||
		c.SnapshotTimeout != 60*time.Second || c.MaxLine != 16<<20 || c.Reconcile != 15*time.Second {
		t.Fatalf("defaults drifted from the spec: %+v", c)
	}
}

// The real watchLoop, with an injected sleep and monotonic clock: the delay between restarts
// is 1, 2, 4, 8, 16, 30, 30 seconds, and a child that ran 60 s resets it to 1.
func TestRestartScheduleDoublesCapsAndResetsAfterALongRun(t *testing.T) {
	ran := []time.Duration{0, 0, 0, 0, 0, 0, 0, 61 * time.Second, 0, 0, 59 * time.Second, 0}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 1, 2, 4, 8, 16}
	for i := range want {
		want[i] *= time.Second
	}
	fake := fakeyolo.Install(t, fakeyolo.Script{Watch: []fakeyolo.Run{{Exit: 1}}, JSON: []fakeyolo.Run{{Stdout: td("approve-json-final.json")}}})

	var mu sync.Mutex
	var slept []time.Duration
	calls := 0
	mono := func() time.Time { // two reads per child: its start, then its end after ran[k]
		mu.Lock()
		defer mu.Unlock()
		k := calls / 2
		calls++
		t0 := time.Unix(1000, 0)
		if calls%2 == 1 || k >= len(ran) {
			return t0
		}
		return t0.Add(ran[k])
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		slept = append(slept, d)
		n := len(slept)
		mu.Unlock()
		if n >= len(want) {
			cancel()
			return false
		}
		return true
	}
	s := New(Config{Yolo: fake, Mono: mono, Sleep: sleep, Now: func() time.Time { return base }}, func(model.Input) {})
	done := make(chan struct{})
	go func() { s.watchLoop(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(guard):
		t.Fatal("watchLoop did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(slept) != len(want) {
		t.Fatalf("slept %v", slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("delay %d = %v, want %v (all: %v)", i, slept[i], want[i], slept)
		}
	}
}

func gone(pid int) bool {
	err := syscall.Kill(pid, 0)
	return errors.Is(err, syscall.ESRCH)
}

// Shutdown kills the watch child's whole process group, not only the child.
func TestShutdownKillsTheWatchChildAndItsGroup_M1(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Stdout: td("approve-json-final.json")}},
		Watch: []fakeyolo.Run{{Stdout: td("empty-home-watch.jsonl"), Block: true, Grandchild: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour, NoInitialSnapshot: true}, h)
	if err := h.WaitFor(ctxGuard(t), func(m model.Model) bool { return m.Watch == model.WatchRunning }); err != nil {
		t.Fatal(err)
	}
	var child, grand int
	for _, c := range fakeyolo.Calls(t, rec) {
		if c.Mode == "watch" {
			child, grand = c.Pid, c.GrandPid
		}
	}
	if child == 0 || grand == 0 || gone(child) || gone(grand) {
		t.Fatalf("setup: child %d grandchild %d should be alive", child, grand)
	}
	stop()
	if !gone(child) {
		t.Fatalf("watch child %d outlived shutdown", child)
	}
	// The grandchild is reparented and reaped by init, which may take a moment; its death is
	// the event, so wait for it.
	for deadline := time.Now().Add(guard); !gone(grand); {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d outlived shutdown: the process group was not killed", grand)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A snapshot child that is still running when the queue shuts down is killed too.
func TestShutdownKillsARunningSnapshotChild_M1(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	fake := fakeyolo.Install(t, fakeyolo.Script{Record: rec,
		JSON:  []fakeyolo.Run{{Hang: true}},
		Watch: []fakeyolo.Run{{Block: true}}})
	h := newHub()
	stop := runSup(t, Config{Yolo: fake, Reconcile: time.Hour}, h)
	if err := h.WaitFor(ctxGuard(t), func(model.Model) bool { return modeCount(t, rec, "json") == 1 }); err != nil {
		t.Fatal(err)
	}
	var pid int
	for _, c := range fakeyolo.Calls(t, rec) {
		if c.Mode == "json" {
			pid = c.Pid
		}
	}
	stop()
	if pid == 0 || !gone(pid) {
		t.Fatalf("snapshot child %d outlived shutdown", pid)
	}
}
