package hostfloor

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// prelaunch_test.go pins the floor's half of a program's PRE-LAUNCH REFRESH (prelaunch.go): when
// it is due (its stamp, and the content of the files it watches), the one-at-a-time lock and its
// one bounded wait, the bound, the signals, and what it prints. The program is a /bin/sh stub that
// appends one line per run to a log, so nothing here runs pi or reaches a network. Its call site,
// `yolo host --`, is pinned in internal/cli's hostprelaunchrefresh_test.go.

// refreshWorld is one floor and the stub program its refreshes run.
type refreshWorld struct {
	f     *Floor
	out   *syncBuffer
	clock *clock
	home  string
	log   string
	stub  string
}

// newRefreshWorld is a floor under a fresh resolved temp root, with a settable clock, and a stub
// whose `update --extensions` runs body after logging its argv. body "" exits 0.
func newRefreshWorld(t *testing.T, body string) *refreshWorld {
	t.Helper()
	root := resolvedTemp(t)
	w := &refreshWorld{out: newSyncBuffer(), clock: &clock{t: time.Now()},
		home: filepath.Join(root, "home"), log: filepath.Join(root, "refresh.log"),
		stub: filepath.Join(root, "bin", "pi")}
	must(t, os.MkdirAll(w.home, 0o755))
	w.setStub(t, body)
	w.f = &Floor{Dir: filepath.Join(root, "floor"), Home: w.home, Now: w.clock.now, Out: w.out,
		Prefix: "yolo host: "}
	return w
}

func (w *refreshWorld) setStub(t *testing.T, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(w.stub), 0o755))
	script := "#!/bin/sh\necho \"$*|MARK=${MARK-unset}|AMBIENT=${PRELAUNCH_AMBIENT-unset}\" >> '" + w.log + "'\n" +
		body + "\nexit 0\n"
	must(t, os.WriteFile(w.stub, []byte(script), 0o755))
}

// runs is how many times the stub ran.
func (w *refreshWorld) runs(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(w.log)
	if os.IsNotExist(err) {
		return 0
	}
	must(t, err)
	return strings.Count(string(data), "\n")
}

func (w *refreshWorld) refresh(p Program) RefreshResult {
	return w.f.PrelaunchRefresh(p, w.stub, []string{"PATH=/usr/bin:/bin", "MARK=handed"})
}

// refreshProgram is pi as its pack declares it, watching due.
func refreshProgram(due ...string) Program {
	return Program{Pack: "pi", Install: packdecl.Install{Bin: "pi", Kind: "npm",
		Package: "@earendil-works/pi-coding-agent", Refresh: &packdecl.Refresh{
			Argv: []string{"update", "--extensions"}, Lock: ".pi-shared-npm/.yolo-update.lock",
			DueOnChange: due}}}
}

const settingsRel = ".pi/agent/settings.json"

func TestPrelaunchRefreshRunsOnceAnIntervalOnItsOwnStamp(t *testing.T) {
	w := newRefreshWorld(t, "")
	t.Setenv("PRELAUNCH_AMBIENT", "from-the-test-process")
	p := refreshProgram()
	if got := w.refresh(p); got.Outcome != RefreshRan {
		t.Fatalf("a first launch: outcome %v, want RefreshRan\n%s", got.Outcome, w.out)
	}
	if !strings.Contains(w.out.String(), "yolo host: Refreshing pi (update --extensions)...\n") {
		t.Errorf("the refresh did not say it was running, in the launcher's words:\n%s", w.out)
	}
	log, _ := os.ReadFile(w.log)
	// The argv is the pack's, after the program; the environment is the one handed in, whole.
	if string(log) != "update --extensions|MARK=handed|AMBIENT=unset\n" {
		t.Errorf("the stub saw %q, want the declared argv in exactly the environment handed in", log)
	}
	if got := w.refresh(p); got.Outcome != RefreshNone || w.runs(t) != 1 {
		t.Errorf("a second launch inside the interval: outcome %v, %d runs; want none", got.Outcome, w.runs(t))
	}
	w.clock.t = w.clock.t.Add(DefaultUpdateInterval - time.Second)
	if w.refresh(p); w.runs(t) != 1 {
		t.Errorf("a launch a second short of the interval ran the refresh again")
	}
	w.clock.t = w.clock.t.Add(2 * time.Second)
	if got := w.refresh(p); got.Outcome != RefreshRan || w.runs(t) != 2 {
		t.Errorf("a launch past the interval: outcome %v, %d runs; want a second run", got.Outcome, w.runs(t))
	}
	// The state is the floor's own, under its 0700 prefix: never a jail's stamp or its store.
	if st, err := os.Stat(w.f.Dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the prefix after a refresh: %v %v, want 0700", st, err)
	}
	if _, err := os.Stat(filepath.Join(w.f.Dir, "refresh", "pi.stamp")); err != nil {
		t.Errorf("no stamp under the prefix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".pi-shared-npm")); !os.IsNotExist(err) {
		t.Errorf("the host refresh touched the jails' store (the pack's lock parent): %v", err)
	}
}

func TestPrelaunchRefreshWatchedContentMakesItDueInsideTheInterval(t *testing.T) {
	w := newRefreshWorld(t, "")
	p := refreshProgram(settingsRel)
	w.refresh(p)
	if w.runs(t) != 1 {
		t.Fatalf("a first launch did not refresh:\n%s", w.out)
	}
	settings := filepath.Join(w.home, settingsRel)
	must(t, os.MkdirAll(filepath.Dir(settings), 0o755))
	must(t, os.WriteFile(settings, []byte(`{"packages":["npm:pi-a"]}`), 0o644))
	if got := w.refresh(p); got.Outcome != RefreshRan || w.runs(t) != 2 {
		t.Errorf("a settings change inside the interval: outcome %v, %d runs; want a run", got.Outcome, w.runs(t))
	}
	if w.refresh(p); w.runs(t) != 2 {
		t.Errorf("the same content again inside the interval ran the refresh again")
	}
	// Content, not mtime: yolo rewrites a composed file on every boot.
	future := w.clock.t.Add(time.Minute)
	must(t, os.Chtimes(settings, future, future))
	if w.refresh(p); w.runs(t) != 2 {
		t.Errorf("an mtime that moved with the content unchanged made the refresh due")
	}
	// Back to absent, which a refresh already succeeded for: not due.
	must(t, os.Remove(settings))
	if w.refresh(p); w.runs(t) != 2 {
		t.Errorf("content a refresh already succeeded with (absent) made the refresh due")
	}
}

func TestPrelaunchRefreshFailureIsStampedAndLeavesTheContentDue(t *testing.T) {
	w := newRefreshWorld(t, "echo 'registry said no' >&2; exit 3")
	p := refreshProgram(settingsRel)
	got := w.refresh(p)
	if got.Outcome != RefreshFailed || got.Status != 3 {
		t.Fatalf("a refresh exiting 3: %+v, want RefreshFailed status 3", got)
	}
	if !strings.Contains(w.out.String(),
		"yolo host: pi: the pre-launch refresh failed (status 3) — running what is installed\n") {
		t.Errorf("the failure line is not the launcher's:\n%s", w.out)
	}
	if !strings.Contains(w.out.String(), "    registry said no\n") {
		t.Errorf("the refresh's own output did not reach Out, nested under its line:\n%s", w.out)
	}
	if _, err := os.Stat(filepath.Join(w.f.Dir, "refresh", "pi.stamp")); err != nil {
		t.Errorf("a failed refresh was not stamped: %v", err)
	}
	if seen, _ := os.ReadDir(filepath.Join(w.f.Dir, "refresh", "pi.seen")); len(seen) != 0 {
		t.Errorf("a failed refresh recorded its content as seen: %v", seen)
	}
	// So the watched change stays due, and the next launch retries it under the lock.
	if w.refresh(p); w.runs(t) != 2 {
		t.Errorf("the next launch inside the interval did not retry the unseen content")
	}
	// A refresh watching nothing waits out the stamp instead.
	w2 := newRefreshWorld(t, "exit 3")
	w2.refresh(refreshProgram())
	if w2.refresh(refreshProgram()); w2.runs(t) != 1 {
		t.Errorf("a stamp-only refresh that failed retried inside the interval")
	}
}

func TestPrelaunchRefreshRunsNothingTheDeclarationOrPolicyHolds(t *testing.T) {
	w := newRefreshWorld(t, "")
	w.f.UpdatesAllowed = func(pack string) bool { return pack != "pi" }
	if got := w.refresh(refreshProgram(settingsRel)); got.Outcome != RefreshNone {
		t.Errorf("agent_updates holding the pack: outcome %v", got.Outcome)
	}
	w.f.UpdatesAllowed = nil
	none := refreshProgram()
	none.Install.Refresh = nil
	if got := w.refresh(none); got.Outcome != RefreshNone {
		t.Errorf("a program declaring no refresh: outcome %v", got.Outcome)
	}
	if w.runs(t) != 0 || w.out.String() != "" {
		t.Errorf("nothing was due, yet the stub ran %d times and the floor said %q", w.runs(t), w.out)
	}
	if _, err := os.Stat(w.f.Dir); !os.IsNotExist(err) {
		t.Errorf("a refresh that did not run made the prefix: %v", err)
	}
}

// holdRefreshLock takes pi's refresh lock as another launch would, from its own descriptor.
func holdRefreshLock(t *testing.T, w *refreshWorld) *pidlock.Lock {
	t.Helper()
	must(t, w.f.ensureDir("refresh"))
	lk, err := pidlock.Acquire(w.f.refreshLockPath("pi"), pidlock.NoWait, nil)
	must(t, err)
	return lk
}

func TestPrelaunchRefreshSkipsAHeldLockWithOneLine(t *testing.T) {
	w := newRefreshWorld(t, "")
	lk := holdRefreshLock(t, w)
	defer lk.Release()
	start := time.Now()
	if got := w.refresh(refreshProgram()); got.Outcome != RefreshHeld {
		t.Fatalf("a held lock: outcome %v, want RefreshHeld\n%s", got.Outcome, w.out)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("a held lock on seen content was waited on (%s)", time.Since(start))
	}
	want := "yolo host: pi: another refresh holds " + w.f.refreshLockPath("pi") + " — running what is installed\n"
	if w.out.String() != want {
		t.Errorf("a held lock printed %q, want the one line %q", w.out, want)
	}
	if w.runs(t) != 0 {
		t.Error("the refresh ran under another launch's lock")
	}
	// No stamp: the holder stamps when it finishes.
	if _, err := os.Stat(filepath.Join(w.f.Dir, "refresh", "pi.stamp")); !os.IsNotExist(err) {
		t.Errorf("a skipped refresh stamped: %v", err)
	}
}

func TestPrelaunchRefreshWaitsBoundedForAHeldLockOnNewContent(t *testing.T) {
	t.Run("the holder outlives the bound", func(t *testing.T) {
		w := newRefreshWorld(t, "")
		w.f.PollTimeout = 300 * time.Millisecond
		lk := holdRefreshLock(t, w)
		defer lk.Release()
		start := time.Now()
		got := w.refresh(refreshProgram(settingsRel))
		waited := time.Since(start)
		if got.Outcome != RefreshHeld || w.runs(t) != 0 {
			t.Fatalf("outcome %v, %d runs; want RefreshHeld and no run\n%s", got.Outcome, w.runs(t), w.out)
		}
		if waited < 300*time.Millisecond || waited > 5*time.Second {
			t.Errorf("waited %s for a lock bounded at 300ms", waited)
		}
		if !strings.Contains(w.out.String(), "this workspace's add-ons are new — waiting for it (up to 300ms)") {
			t.Errorf("the wait was not said before it began:\n%s", w.out)
		}
	})
	t.Run("the holder finishes other content", func(t *testing.T) {
		w := newRefreshWorld(t, "")
		w.f.PollTimeout = 5 * time.Second
		lk := holdRefreshLock(t, w)
		go func() { time.Sleep(150 * time.Millisecond); lk.Release() }()
		if got := w.refresh(refreshProgram(settingsRel)); got.Outcome != RefreshRan || w.runs(t) != 1 {
			t.Errorf("outcome %v, %d runs; want the refresh once the lock was free\n%s", got.Outcome, w.runs(t), w.out)
		}
	})
	t.Run("the holder refreshes this very content", func(t *testing.T) {
		w := newRefreshWorld(t, "")
		w.f.PollTimeout = 5 * time.Second
		lk := holdRefreshLock(t, w)
		key := refreshContentKey(w.home, []string{settingsRel})
		go func() {
			time.Sleep(150 * time.Millisecond)
			w.f.recordSeen("pi", key)
			lk.Release()
		}()
		if got := w.refresh(refreshProgram(settingsRel)); got.Outcome != RefreshNone || w.runs(t) != 0 {
			t.Errorf("outcome %v, %d runs; want nothing left to do\n%s", got.Outcome, w.runs(t), w.out)
		}
		if pidlock.Held(w.f.refreshLockPath("pi")) {
			t.Error("the lock taken after the wait was kept")
		}
	})
}

func TestPrelaunchRefreshIsBoundedAndKilled(t *testing.T) {
	w := newRefreshWorld(t, "exec sleep 30")
	w.f.PollTimeout = 300 * time.Millisecond
	start := time.Now()
	got := w.refresh(refreshProgram())
	if got.Outcome != RefreshTimedOut {
		t.Fatalf("a refresh outliving its bound: outcome %v\n%s", got.Outcome, w.out)
	}
	if took := time.Since(start); took > RefreshKillAfter+5*time.Second {
		t.Errorf("the bounded refresh took %s", took)
	}
	if !strings.Contains(w.out.String(),
		"yolo host: pi: the pre-launch refresh timed out after 300ms — running what is installed\n") {
		t.Errorf("the timeout line is not the launcher's:\n%s", w.out)
	}
	if pidlock.Held(w.f.refreshLockPath("pi")) {
		t.Error("the lock outlived the timed-out refresh")
	}
}

// The signals this process forwards to the refresh, sent by the refresh itself to its parent —
// which is this test process, because the floor execs the program directly — so they arrive while
// the forwarding is armed.
func TestPrelaunchRefreshCtrlCEndsTheRefreshAndATerminateEndsTheLaunch(t *testing.T) {
	t.Run("SIGINT", func(t *testing.T) {
		w := newRefreshWorld(t, "kill -INT $PPID; exec sleep 10")
		got := w.refresh(refreshProgram())
		if got.Outcome != RefreshInterrupted {
			t.Fatalf("a Ctrl-C during the refresh: %+v\n%s", got, w.out)
		}
		if !strings.Contains(w.out.String(),
			"yolo host: pi: the pre-launch refresh was interrupted (Ctrl-C) — running what is installed\n") {
			t.Errorf("the interrupt line is not the launcher's:\n%s", w.out)
		}
	})
	t.Run("SIGTERM", func(t *testing.T) {
		w := newRefreshWorld(t, "kill -TERM $PPID; exec sleep 10")
		got := w.refresh(refreshProgram())
		if got.Outcome != RefreshStopped || got.Signal != syscall.SIGTERM {
			t.Fatalf("a SIGTERM during the refresh: %+v, want RefreshStopped by SIGTERM\n%s", got, w.out)
		}
		if pidlock.Held(w.f.refreshLockPath("pi")) {
			t.Error("a SIGTERM left the refresh lock held")
		}
		if _, err := os.Stat(filepath.Join(w.f.Dir, "refresh", "pi.stamp")); err != nil {
			t.Errorf("a stopped refresh was not stamped: %v", err)
		}
	})
}

func TestRefreshContentKeyIsContentNotPresenceAlone(t *testing.T) {
	home := resolvedTemp(t)
	files := []string{settingsRel}
	if refreshContentKey(home, nil) != "" {
		t.Error("a refresh watching nothing has a key")
	}
	absent := refreshContentKey(home, files)
	path := filepath.Join(home, settingsRel)
	must(t, os.MkdirAll(path, 0o755))
	if refreshContentKey(home, files) != absent {
		t.Error("a directory at the watched path is not 'absent', as the launcher's [ -f ] reads it")
	}
	must(t, os.Remove(path))
	must(t, os.WriteFile(path, nil, 0o644))
	empty := refreshContentKey(home, files)
	if empty == absent {
		t.Error("an empty file and an absent one have one key")
	}
	must(t, os.WriteFile(path, []byte("{}"), 0o644))
	if k := refreshContentKey(home, files); k == empty || k == absent {
		t.Error("new content did not move the key")
	}
	if strings.ContainsAny(refreshContentKey(home, files), "/ ") {
		t.Error("the key is not a file name")
	}
}

// A prefix the refresh cannot write is "cannot take the lock", said with its step, never "another
// refresh holds it": the two ask the user to do different things. A file where the prefix should be
// fails as root too, which a mode would not.
func TestPrelaunchRefreshThatCannotTakeItsLockSaysSoAndRunsNothing(t *testing.T) {
	w := newRefreshWorld(t, "")
	must(t, os.WriteFile(w.f.Dir, []byte("not a directory"), 0o600))
	if got := w.refresh(refreshProgram(settingsRel)); got.Outcome != RefreshNoLock || w.runs(t) != 0 {
		t.Fatalf("an unwritable prefix: outcome %v, %d runs; want RefreshNoLock and no run\n%s",
			got.Outcome, w.runs(t), w.out)
	}
	out := w.out.String()
	if !strings.Contains(out, "yolo host: pi: cannot take the refresh lock ") ||
		!strings.Contains(out, "a later launch runs it") || strings.Contains(out, "another refresh holds") {
		t.Errorf("the line does not say the lock cannot be taken, with its step:\n%s", out)
	}
}
