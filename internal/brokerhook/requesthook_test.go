package brokerhook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// alive reports whether pid still runs: a zombie, which a container's init may never reap,
// counts as gone.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no /proc to ask (macOS); the signal answered
	}
	if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] != 'Z'
	}
	return true
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitFor(t, "the hook's pid file", func() bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pid > 0
	})
	return pid
}

// A hook is killed with its whole process group, child included, when its request leaves
// pending (decided with `yolo approve`, refused because its jail stopped, or expired) or its
// run times out: both while its shell still waits, and after its shell has exited leaving a
// backgrounded child, such as a notifier, still asking.
func TestAHookIsKilledWhenItsRequestLeavesPending(t *testing.T) {
	for _, c := range []struct {
		name    string
		timeout time.Duration
		leave   func(f *fixture, q brokerstore.Request) error
		logged  string
	}{
		{"decided", 0, func(f *fixture, q brokerstore.Request) error {
			_, err := f.store.Decide(q.ID, "pending", brokerstore.Decision{Deny: true})
			return err
		}, "stopped because its request is no longer pending"},
		{"jail stopped", 0, func(f *fixture, q brokerstore.Request) error {
			err := f.store.End(q.Jail, q.Start)
			return err
		}, "stopped because its request is no longer pending"},
		{"expired", 0, func(f *fixture, q brokerstore.Request) error {
			f.skew.Store(int64(brokerstore.PendingLife + time.Minute))
			return nil
		}, "stopped because its request is no longer pending"},
		// The run's Timeout runs out only once the hook's child is seen (limitClock), so it
		// never races the hook's start-up, however slowly a busy machine starts it.
		{"timed out", time.Minute, func(*fixture, brokerstore.Request) error { return nil },
			"stopped because it ran into its timeout"},
	} {
		for _, shape := range []struct{ name, tail string }{
			{"shell waiting", "wait\n"},
			{"shell exited", "exit 0\n"},
		} {
			t.Run(c.name+"/"+shape.name, func(t *testing.T) {
				f := newFixture(t)
				var log syncBuffer
				child := filepath.Join(f.dir, "child")
				hook := f.script(t, "hook", "cat >/dev/null\nsleep 300 >/dev/null 2>&1 &\necho $! >'"+child+"'\n"+shape.tail)
				clock := &limitClock{}
				stop := f.runFor(t, hook, &log, c.timeout, 5*time.Millisecond, clock)
				defer stop()
				q, err := f.store.File(f.filing)
				if err != nil {
					t.Fatal(err)
				}
				pid := readPid(t, child)
				if err := c.leave(f, q); err != nil {
					t.Fatal(err)
				}
				if c.timeout > 0 {
					// The Timeout, shorter than the request's life, is the run's limit.
					if got := clock.runOut(t); got[0] != c.timeout {
						t.Fatalf("the run's limit is %s, want its Timeout %s", got[0], c.timeout)
					}
				}
				waitFor(t, "the hook to be stopped", func() bool { return strings.Contains(log.String(), c.logged) })
				waitFor(t, "the hook's child to die", func() bool { return !alive(pid) })
				if got := f.state(t, q.ID); got == "allowed" {
					t.Fatalf("state %s after the hook was stopped", got)
				}
				// Killed because its request left pending is no failure; a timeout is. The runner
				// logs the stop before it records the failure, so a timeout's is waited for.
				if c.timeout > 0 && shape.name == "shell waiting" {
					waitFor(t, "the timeout to be recorded", func() bool {
						return strings.HasPrefix(f.request(t, q.ID).HookFailure, "hook timed out after")
					})
				}
				failure := f.request(t, q.ID).HookFailure
				if c.timeout == 0 && failure != "" {
					t.Fatalf("a hook stopped because its request left pending recorded %q", failure)
				}
			})
		}
	}
}

// A hook still running when its request expires was waiting for a click, which is its job:
// it is stopped because its request stopped waiting, and records no failure. That holds
// whichever comes first, the store's expiry event or the runner's own clock on the request's
// expiry, and when the runner's Timeout runs past the expiry, as its default does.
func TestAHookStillRunningAtExpiryRecordsNoFailure(t *testing.T) {
	// How long the request has left when the runner starts. The runner's clock on it runs out
	// only once the hook's child is seen (limitClock), so no real clock races the hook's start.
	const left = 10 * time.Minute
	for _, c := range []struct {
		name              string
		timeout, interval time.Duration
	}{
		// No poll before the expiry: only the runner's clock can stop the hook.
		{"by the runner's clock", 0, time.Minute},
		// A timeout past the expiry, as the default one-hour timeout is for any hook that
		// started after its request was filed.
		{"timeout past the expiry", left + time.Second, time.Minute},
		// The store's expiry event and the runner's clock race.
		{"event and clock race", 0, 5 * time.Millisecond},
	} {
		for _, shape := range []struct{ name, tail string }{
			{"shell waiting", "wait\n"},
			{"shell exited", "exit 0\n"},
		} {
			t.Run(c.name+"/"+shape.name, func(t *testing.T) {
				f := newFixture(t)
				var log syncBuffer
				child := filepath.Join(f.dir, "child")
				hook := f.script(t, "hook", "cat >/dev/null\nsleep 300 >/dev/null 2>&1 &\necho $! >'"+child+"'\n"+shape.tail)
				// Filed long enough ago that it expires `left` from now; the runner, started
				// after, finds it pending in its first read.
				f.skew.Store(int64(left - brokerstore.PendingLife))
				q, err := f.store.File(f.filing)
				if err != nil {
					t.Fatal(err)
				}
				f.skew.Store(0)
				clock := &limitClock{}
				stop := f.runFor(t, hook, &log, c.timeout, c.interval, clock)
				defer stop()
				pid := readPid(t, child)
				if c.interval < time.Minute {
					// The store's clock passes the expiry, so its next poll's expiry event races
					// the runner's own clock, run out just below.
					f.skew.Store(int64(left + time.Minute))
				}
				if got := clock.runOut(t); got[0] > left {
					t.Fatalf("the run's limit is %s, past its request's expiry %s from now", got[0], left)
				}
				waitFor(t, "the hook to be stopped", func() bool { return strings.Contains(log.String(), "was stopped because") })
				waitFor(t, "the hook's child to die", func() bool { return !alive(pid) })
				stop() // waits for the runner to finish with the hook, failure record included
				if got := f.request(t, q.ID).HookFailure; got != "" {
					t.Fatalf("a hook stopped at its request's expiry recorded %q", got)
				}
				if l := log.String(); strings.Contains(l, "timeout") || strings.Contains(l, "failed") {
					t.Fatalf("the log calls an expiry a failure: %q", l)
				}
			})
		}
	}
}

// A hook is stopped with SIGINT to its process group first, so a trap in it can close the
// notification it showed, and only then, after StopGrace, with SIGKILL. The grace is the
// test's to end and it does not, so the trap runs however slowly a loaded machine runs it;
// had the group been SIGKILLed first, or without waiting on the grace, the trap would never
// run.
func TestAStoppedHookGetsSIGINTFirst(t *testing.T) {
	f := newFixture(t)
	f.grace = newGraceClock()
	var log syncBuffer
	closed := filepath.Join(f.dir, "closed")
	started := filepath.Join(f.dir, "started")
	hook := f.script(t, "hook", "trap \x27echo closed >\""+closed+"\"; exit 0\x27 INT\ncat >/dev/null\ntouch \x27"+started+"\x27\nwhile :; do sleep 1; done\n")
	stop := f.run(t, hook, &log)
	defer stop()
	defer f.grace.end() // runs before stop, so a failed test still lets the runner finish
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to start", func() bool { _, err := os.Stat(started); return err == nil })
	if _, err := f.store.Decide(q.ID, "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	if d := f.grace.await(t); d != StopGrace {
		t.Fatalf("a stopped hook's grace is %s, want StopGrace (%s)", d, StopGrace)
	}
	waitFor(t, "the hook\x27s INT trap to run", func() bool { b, _ := os.ReadFile(closed); return string(b) == "closed\n" })
	stop()
	if got := f.request(t, q.ID).HookFailure; got != "" {
		t.Fatalf("a hook stopped by its request\x27s decision recorded %q", got)
	}
}

// The documented one-liner cleans up after itself: its `a=$(notify-send -A ...)` child is in
// the hook\x27s process group, so the group\x27s SIGINT reaches it while the non-interactive shell
// waits on the command substitution. The fake notify-send records the SIGINT as notify-send
// 0.8 does by closing its notification.
func TestTheOneLinerNotifierGetsTheSIGINT(t *testing.T) {
	f := newFixture(t)
	bin := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(f.dir, "closed")
	started := filepath.Join(f.dir, "started")
	fake := "#!/bin/sh\ntrap \x27echo closed >\"" + closed + "\"; exit 0\x27 INT\ntouch \x27" + started + "\x27\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(filepath.Join(bin, "notify-send"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var log syncBuffer
	stop := f.run(t, `a=$(notify-send -u critical -A once="Allow once" -A deny=Deny "yolo: $YOLO_REQUEST_HEADLINE" "$YOLO_REQUEST_DESCRIPTION_MARKUP"); case "$a" in once) yolo approve "$YOLO_REQUEST_ID";; deny) yolo approve "$YOLO_REQUEST_ID" --deny;; esac`, &log)
	defer stop()
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "notify-send to start", func() bool { _, err := os.Stat(started); return err == nil })
	if _, err := f.store.Decide(q.ID, "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "notify-send to get the SIGINT", func() bool { b, _ := os.ReadFile(closed); return string(b) == "closed\n" })
	stop()
	if got := f.request(t, q.ID).HookFailure; got != "" {
		t.Fatalf("a hook stopped by its request\x27s decision recorded %q", got)
	}
}

// A hook that ignores SIGINT is still killed, everything it started included, once
// StopGrace has passed: "kill everything it started" holds, and the stop is bounded. The
// backgrounded child also ignores SIGINT, as a non-interactive shell starts every
// background job. The grace is the test's to end: the child outlives the SIGINT while the
// grace runs, and dies once it ends.
func TestAHookIgnoringSIGINTIsKilledAfterTheGrace(t *testing.T) {
	f := newFixture(t)
	f.grace = newGraceClock()
	var log syncBuffer
	child := filepath.Join(f.dir, "child")
	hook := f.script(t, "hook", "trap \x27\x27 INT\ncat >/dev/null\nsleep 300 >/dev/null 2>&1 &\necho $! >\x27"+child+"\x27\nwait\n")
	stop := f.run(t, hook, &log)
	defer stop()
	defer f.grace.end() // runs before stop, so a failed test still lets the runner finish
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, child)
	if _, err := f.store.Decide(q.ID, "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	if d := f.grace.await(t); d != StopGrace {
		t.Fatalf("a stopped hook's grace is %s, want StopGrace (%s)", d, StopGrace)
	}
	// The SIGINT has been sent and ignored; nothing more is sent until the grace ends.
	if !alive(pid) {
		t.Fatal("the hook\x27s child was killed before its grace ended")
	}
	f.grace.end()
	waitFor(t, "the hook\x27s child to die", func() bool { return !alive(pid) })
}

// A request filed and decided between two polls has no question left to ask: its filing
// event carries its current state (brokerstore.Diff, pinned there), and the hook never
// starts for it. The event is handed over alone, with no decision after it to stop a hook
// that started anyway.
func TestARequestDecidedBeforeItsFilingIsSeenRunsNoHook(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.dir, "ran")
	r := &Runner{Store: f.store, Start: "start", Command: f.script(t, "hook", "touch '"+marker+"'\n")}
	q := f.filing
	// Filed now: a request already past its expiry has no question left to ask either.
	q.ID, q.State, q.Filed = "r-decided", "pending", time.Now()
	r.event(context.Background(), brokerstore.Event{Kind: brokerstore.EventRequestFiled, Request: &q, Current: "denied"})
	r.wg.Wait()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the hook ran for a request already decided")
	}
	// The same filing, still pending, does run it: the test above is not vacuous.
	r.event(context.Background(), brokerstore.Event{Kind: brokerstore.EventRequestFiled, Request: &q, Current: "pending"})
	r.wg.Wait()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the hook did not run for a pending filing")
	}
}

// The hook is a request hook: a decision, a grant's use and its end fire nothing.
func TestOnlyAFiledRequestRunsTheHook(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.dir, "runs")
	stop := f.run(t, f.script(t, "hook", "cat >/dev/null\necho \"$YOLO_EVENT\" >>'"+out+"'\n"), &syncBuffer{})
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to run", func() bool { b, _ := os.ReadFile(out); return len(b) > 0 })
	if _, err := f.store.Decide(q.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.store.Consume(q.Workspace, q.Jail, q.Set, q.Digest, f.store.Revalidate); err != nil || !ok {
		t.Fatal(ok, err)
	}
	time.Sleep(100 * time.Millisecond) // twenty ticks
	stop()
	if b, _ := os.ReadFile(out); string(b) != "request-filed\n" {
		t.Fatalf("the hook ran for %q, want only the filing", b)
	}
}

// A store that cannot be read when the broker starts is tried again rather than switching
// the hook off, its failure is logged once rather than once per tick, and a request filed
// meanwhile still runs the hook.
func TestAnUnreadableStoreIsRetriedAndLoggedOnce(t *testing.T) {
	f := newFixture(t)
	blocker := filepath.Join(f.dir, "blocked")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := f.store
	bad := *good
	bad.Dir = filepath.Join(blocker, "github") // under a regular file: no read can succeed
	f.store = &bad
	marker := filepath.Join(f.dir, "ran")
	var log syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: f.script(t, "hook", "cat >/dev/null\ntouch '"+marker+"'\n"),
		Interval: 5 * time.Millisecond, Log: &log}
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the failure to be logged", func() bool { return strings.Contains(log.String(), "reading the store failed") })
	time.Sleep(50 * time.Millisecond) // ten more failed reads
	if n := strings.Count(log.String(), "reading the store failed"); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, log.String())
	}
	// The store becomes readable, with a request filed while it was not.
	if _, err := good.File(f.filing); err != nil {
		t.Fatal(err)
	}
	// Swap the link in with one rename: a remove-then-symlink leaves a window in which the
	// runner's next poll creates the store directory where the blocker was.
	link := blocker + ".link"
	if err := os.Symlink(filepath.Dir(good.Dir), link); err != nil { // bad.Dir now names good.Dir
		t.Fatal(err)
	}
	if err := os.Rename(link, blocker); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to run once the store reads", func() bool { _, err := os.Stat(marker); return err == nil })
}

// The hook only notifies: nothing it prints, an old `approve` answer included, decides its
// request, and the store records no decision.
func TestTheHookDecidesNothingByItself(t *testing.T) {
	for _, body := range []string{"echo approve\n", "echo deny\n", "echo 'approve please'\n", "echo approve\nexit 3\n"} {
		f := newFixture(t)
		marker := filepath.Join(f.dir, "ran")
		stop := f.run(t, f.script(t, "hook", "cat >/dev/null\ntouch '"+marker+"'\n"+body), &syncBuffer{})
		q, err := f.store.File(f.filing)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the hook to run", func() bool { _, err := os.Stat(marker); return err == nil })
		time.Sleep(50 * time.Millisecond) // ten ticks
		stop()
		if got := f.state(t, q.ID); got != "pending" {
			t.Fatalf("%q left the request %s", body, got)
		}
		f.mu.Lock()
		kinds := strings.Join(f.kinds, ",")
		f.mu.Unlock()
		if strings.Contains(kinds, "decision") {
			t.Fatalf("%q was audited as a decision: %s", body, kinds)
		}
	}
}

// Plain `yolo` works in the hook (Q43): a private directory holding exactly one entry, a
// link named yolo to Bin, leads its PATH, so `yolo approve` resolves even when the broker's
// own PATH has no yolo. YOLO_BIN names the same binary, and the directory goes when the
// runner stops.
func TestTheHookRunsPlainYolo(t *testing.T) {
	f := newFixture(t)
	binDir := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(f.dir, "calls")
	listing := filepath.Join(f.dir, "listing")
	bin := filepath.Join(binDir, "yolo")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >>'"+calls+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	hook := f.script(t, "hook", "cat >/dev/null\n[ \"$YOLO_BIN\" = '"+bin+"' ] || exit 9\n"+
		"d=$(dirname \"$(command -v yolo)\")\n[ \"$d\" != '"+binDir+"' ] || exit 8\n"+
		"{ echo \"$d\"; ls -A \"$d\"; readlink \"$d/yolo\"; } >'"+listing+"'\n"+
		"yolo approve \"$YOLO_REQUEST_ID\"\n")
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: hook, Bin: bin, Interval: 5 * time.Millisecond}
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to call yolo", func() bool { b, _ := os.ReadFile(calls); return len(b) > 0 })
	if b, _ := os.ReadFile(calls); string(b) != "approve "+q.ID+"\n" {
		t.Fatalf("yolo was called with %q", b)
	}
	b, _ := os.ReadFile(listing)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[1] != "yolo" || lines[2] != bin || filepath.Dir(lines[0]) != f.store.Dir {
		t.Fatalf("the hook's yolo directory: %q", b)
	}
	cancel()
	<-done
	if _, err := os.Lstat(lines[0]); !os.IsNotExist(err) {
		t.Fatalf("the yolo directory outlived its runner: %v", err)
	}
}

// A failing hook's exit and the last of its output go on its request's record, where every
// listing shows them; the broker's shared log names the request and the exit, never the
// output. It still decides nothing.
func TestAFailedHookIsRecordedOnItsRequest(t *testing.T) {
	f := newFixture(t)
	var log syncBuffer
	stop := f.run(t, f.script(t, "hook", "cat >/dev/null\necho 'some noise'\necho 'sh: notify-send: not found' >&2\nexit 127\n"), &log)
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure to be recorded", func() bool { return f.request(t, q.ID).HookFailure != "" })
	stop()
	got := f.request(t, q.ID)
	if got.HookFailure != "hook exited 127: sh: notify-send: not found" || got.HookOutput != "some noise\nsh: notify-send: not found\n" {
		t.Fatalf("recorded %q / %q", got.HookFailure, got.HookOutput)
	}
	if got.State != "pending" {
		t.Fatalf("a failed hook left its request %s", got.State)
	}
	if l := log.String(); !strings.Contains(l, q.ID) || !strings.Contains(l, "exited 127") || strings.Contains(l, "notify-send") {
		t.Fatalf("the shared log: %q", l)
	}
}

// The kept output is the last OutputMax bytes, and the one line is bounded.
func TestAFailedHooksOutputIsBounded(t *testing.T) {
	var tb tailBuffer
	for range 1000 {
		tb.Write([]byte("0123456789"))
	}
	tb.Write([]byte("END"))
	if s := tb.String(); len(s) != OutputMax || !strings.HasSuffix(s, "END") {
		t.Fatalf("kept %d bytes ending %q", len(s), s[len(s)-5:])
	}
	if l := failureLine("hook exited 1", strings.Repeat("é", 500)); len(l) > FailureLineMax || !strings.HasSuffix(l, "…") {
		t.Fatalf("line of %d bytes: %q", len(l), l)
	}
}

// The hook starts in the given directory, not wherever the broker runs.
func TestTheHookRunsInItsDirectory(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	out := filepath.Join(f.dir, "cwd")
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Store: f.store, Start: "start", Command: f.script(t, "hook", "cat >/dev/null\npwd >'"+out+"'\n"),
		Dir: home, Interval: 5 * time.Millisecond}
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond)
	if _, err := f.store.File(f.filing); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to run", func() bool { b, _ := os.ReadFile(out); return len(b) > 0 })
	b, _ := os.ReadFile(out)
	want, _ := filepath.EvalSymlinks(home)
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(b))); got != want {
		t.Fatalf("the hook ran in %s, want %s", got, want)
	}
}

// A hook that started is recorded on its request, so the jail-readable record can say the user
// was asked (docs/design/jail-history.md JH-D11); one that exits 0 records no failure.
func TestAHookThatRanIsRecordedOnItsRequest(t *testing.T) {
	f := newFixture(t)
	var log syncBuffer
	stop := f.run(t, f.script(t, "hook", "cat >/dev/null\n"), &log)
	defer stop()
	q, err := f.store.File(f.filing)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook's run to be recorded", func() bool { return f.request(t, q.ID).HookRan })
	stop()
	if got := f.request(t, q.ID); got.HookFailure != "" || got.State != "pending" {
		t.Fatalf("a hook that exited 0 left %+v", got)
	}
}
