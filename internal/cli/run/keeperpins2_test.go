package run

// keeperpins2_test.go drives keeper call sites no other unit test reached, each of which could be
// deleted with the unit gate green: the scope move (JL-D61), a quit's print of what the keeper
// recorded (JL-D19), the unkept reap's stop of the dead keeper's scope (JL-D32), the arrival's word
// on what it waits for (JL-D12) and the lifeline path's release of the launch lock (JL-D31)
// (docs/design/jail-lifetime-last-session-wins.md).

import (
	"bytes"
	"io"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// TestAKeeperMovesIntoAScopeOfItsOwnFirst is JL-D61 at keeper.run's call site. On Linux, where there
// is a busctl, the keeper's first runtime act is StartTransientUnit naming its own pid, and the scope
// it moved into is in its start record, which the reap of an unkept jail stops (JL-D32). Off Linux
// there is no systemd (JL-D5 puts the keeper in a scope "where systemd is present"; keeper_other.go),
// so even with a busctl on PATH the keeper makes no scope move and records no scope. On both, its log
// says what became of the move: a line only that call site writes, so deleting the call fails here.
func TestAKeeperMovesIntoAScopeOfItsOwnFirst(t *testing.T) {
	var session *sessionLock
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	}, func(o *Options) {
		o.LookPath = func(name string) (string, bool) {
			if name == "busctl" {
				return "/usr/bin/busctl", true
			}
			return "", false
		}
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	pid := strconv.Itoa(os.Getpid())
	unit := "yolo-jail-keeper-" + f.cname + "-" + pid + ".scope"
	f.jail.mu.Lock()
	calls := append([]string(nil), f.jail.calls...)
	f.jail.mu.Unlock()
	wantScope, wantLine := unit, "keeper: moved into the systemd user scope "+unit
	if goruntime.GOOS == "linux" {
		if len(calls) == 0 || !strings.HasPrefix(calls[0], "/usr/bin/busctl --user call org.freedesktop.systemd1") ||
			!strings.Contains(calls[0], "StartTransientUnit") || !strings.Contains(calls[0], unit) ||
			!strings.Contains(calls[0], "PIDs au 1 "+pid) {
			t.Errorf("the keeper's first runtime act is not its scope move naming its pid: %q", calls)
		}
	} else {
		wantScope = ""
		wantLine = "keeper: no systemd on this platform, so it runs in a session of its own and no scope of its own"
		for _, c := range calls {
			if strings.Contains(c, "busctl") || strings.Contains(c, "StartTransientUnit") {
				t.Errorf("the keeper tried a systemd scope move on %s, which has no systemd: %q", goruntime.GOOS, c)
			}
		}
	}
	if rec, ok := readKeeperRecord(f.cname); !ok || rec.Scope != wantScope {
		t.Errorf("the start record names the scope %q (%v), want %q", rec.Scope, ok, wantScope)
	}
	if log := f.keeperLog(); !strings.Contains(log, wantLine) {
		t.Errorf("the keeper's log does not say what became of its scope move (want %q):\n%s", wantLine, log)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

// TestAQuitPrintsWhatTheKeeperRecordedWhileItWasIn is JL-D19 at endSession's call site: a session
// that leaves others is shown the keeper's lines logged since it began, and none from before.
func TestAQuitPrintsWhatTheKeeperRecordedWhileItWasIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-records"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	other, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer other.release()
	logF, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = logF.WriteString("09:00:00.000 keeper: a line from before this session\n")
	from := keeperLogSize(cname)
	_, _ = logF.WriteString("09:00:01.000 keeper: a line from while it was in\n")
	_ = logF.Close()
	o := goldenOptions("/ws", t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, Stdout: entrypoint.JailMainEnv + "=" + entrypoint.JailMainHold + "\n"}
		}
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	}
	if rc := o.endSession(cname, "podman", 0, time.Now(), from, false); rc != 0 {
		t.Errorf("rc %d", rc)
	}
	if !strings.Contains(errBuf.String(), "a line from while it was in") {
		t.Errorf("the quit did not print what the keeper recorded while the session was in:\n%s", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "a line from before this session") {
		t.Errorf("the quit printed a line the keeper logged before the session began:\n%s", errBuf.String())
	}
}

// TestTheReapOfAnUnkeptJailStopsItsKeepersScope is JL-D32's scope half at reapUnkept's call site: a
// dead keeper's start record names the systemd scope it moved into, and the reap stops that scope,
// which ends whatever the keeper started that outlived it.
func TestTheReapOfAnUnkeptJailStopsItsKeepersScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	const cname = "yolo-unkept-scope"
	const scope = "yolo-jail-keeper-" + cname + "-999999.scope"
	if err := writeKeeperRecord(cname, keeperRecord{PID: 999999, Scope: scope}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &bytes.Buffer{}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		calls = append(calls, strings.Join(argv, " "))
		return ExecResult{Ran: true}
	}
	o.reapUnkept(cname, "podman", true)
	want := "systemctl --user stop " + scope
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the reap did not stop the dead keeper's scope (%q): %q", want, calls)
	}
}

// TestAnArrivalSaysWhyItWaitsForThePreviousKeeper is JL-D12's "the arrival says why it waits", at
// awaitPreviousKeeper: a launch that waits on a keeper still ending the workspace's previous jail
// names that jail and that keeper, with the launch lock let go, and goes on once the keeper is gone.
func TestAnArrivalSaysWhyItWaitsForThePreviousKeeper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-arrival-waits"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeKeeperRecord(cname, keeperRecord{PID: 4242}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		releaseLock(live)
	}()
	o := goldenOptions("/ws", t.TempDir())
	var out bytes.Buffer
	o.Stdout = &out
	o.holdLaunchLock(cname)
	if !o.awaitPreviousKeeper(cname) {
		t.Fatal("the arrival gave up on a keeper that ended")
	}
	if !o.launchLock.isClosed() {
		t.Error("the arrival waited holding the launch lock")
	}
	if !strings.Contains(out.String(), "The previous jail of this workspace ("+cname+") is still shutting down") ||
		!strings.Contains(out.String(), "its keeper (pid 4242)") {
		t.Errorf("the arrival did not say what it waits for:\n%s", out.String())
	}
}

// TestAKeeperWhoseLaunchDiedBeforeReadyLetsTheLaunchLockGoFirst is JL-D31 on the lifeline's path:
// the keeper releases the launch lock it was handed before its unwind, whose guarded cleanups take
// that lock non-blocking and would otherwise back off and leave the jail's records behind.
func TestAKeeperWhoseLaunchDiedBeforeReadyLetsTheLaunchLockGoFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	jail := newFakeJail(t, "yolo-lifeline-lock")
	// The runtime never shows the container running, so the running wait, which would release the
	// lock too, is still polling when the launch dies: the release on the lifeline's path is the one.
	// The same runtime holds the keeper's stop for the whole of its wait for the container to come up
	// (awaitStartSettled), so that wait is cut short.
	jail.stopped = true
	saved := keeperStartSettleWait
	keeperStartSettleWait = 200 * time.Millisecond
	t.Cleanup(func() { keeperStartSettleWait = saved })
	o := goldenOptions("/ws", t.TempDir())
	o.holdLaunchLock(jail.cname)
	lockPath := launchLockPath(jail.cname)
	handed, err := syscall.Dup(int(o.launchLock.f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	o.launchLock.handOff()
	cfg, err := encodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: t.TempDir(), Cname: jail.cname, Runtime: "podman",
		Config: cfg, SocketsDir: hostServiceSocketsDir(jail.cname, false), RunCmd: jail.mainArgv(false), ImageRef: "img"}
	progR, progW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	lifeR, lifeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- runKeeper(plan, KeeperSeams{}, progW, lifeR, os.NewFile(uintptr(handed), "lock"), nil, make(chan os.Signal), func(ko *Options) {
			ko.Exec = jail.exec
			ko.PIDAlive = func(int) bool { return false }
			ko.LookPath = func(string) (string, bool) { return "", false }
			ko.PathExists = func(string) bool { return false }
			ko.StartDetached = func([]string, *os.File) error { return errTestBinarySelfExec }
		})
		_ = progW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, progR) }()
	// Once the keeper is up (its start record), the launch dies: its lifeline closes.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := readKeeperRecord(jail.cname); ok || time.Now().After(deadline) {
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	_ = lifeW.Close()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the keeper did not end")
	}
	probe, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Error("the keeper ended its unwind still holding the launch lock it was handed")
	}
}
