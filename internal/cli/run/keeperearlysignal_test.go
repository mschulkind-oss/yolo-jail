package run

// keeperearlysignal_test.go pins a fresh launch interrupted in the moment after its keeper said it
// started (docs/design/jail-lifetime-last-session-wins.md §9.5 item 1): a Ctrl-C right after
// "keeper: started" must end the jail it was starting, and never leave its container running with
// nothing that will end it.
//
// What a nested jail showed, 3 of 6 tries: the launch's signal arm closed the lifeline while the
// keeper was still starting the jail's host services. The keeper went on to spawn the container's
// main process, saw the lifeline gone, and stopped the jail at once, before the runtime client had
// made the container. The stop found nothing, the existence probe said no container, the chain ran,
// the keeper killed the client and exited, and the container the runtime finished starting a moment
// later ran on with no keeper, no owner-PID file and no start record.
//
// The fake runtime here (lateJail) is that runtime: its container comes into being a while after
// its client starts, and runs on whatever becomes of the client, as conmon, not `podman run`, holds
// a podman container.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// lateJailLag is how long after its client starts a lateJail's container comes up: longer than the
// keeper's stop and existence probe take against a fake, so a stop sent at the client's spawn comes
// before the container exists.
const lateJailLag = 400 * time.Millisecond

// lateJail is a runtime whose container exists only some time after its main process's client
// starts, and then runs until a stop ends it, whatever becomes of that client. A stop finds only a
// container that runs, and a stopped one is gone (--rm).
type lateJail struct {
	mu     sync.Mutex
	dir    string
	stops  int // stops that ended the container
	missed int // stops that found no container to end
	// stopsUnderAClient counts the stops sent while the client still ran, which could still bring
	// a container up after the stop.
	stopsUnderAClient int
	lag               time.Duration
}

// newLateJail is a lateJail whose container comes up lag after its client starts; a negative lag is
// a client that never brings it up.
func newLateJail(t *testing.T, lag time.Duration) *lateJail {
	j := &lateJail{dir: t.TempDir(), lag: lag}
	t.Cleanup(func() {
		// The client holds until a stop; and the process bringing the container up writes into
		// the directory, so it must be done before t.TempDir's removal runs.
		_ = os.WriteFile(j.marker("stopped"), nil, 0o644)
		if lag >= 0 && j.has("spawned") {
			deadline := time.Now().Add(lag + 2*time.Second)
			for !j.has("created") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
	return j
}

func (j *lateJail) marker(name string) string { return filepath.Join(j.dir, name) }

func (j *lateJail) has(name string) bool {
	_, err := os.Stat(j.marker(name))
	return err == nil
}

// running is whether the container runs: it came up, and no stop has ended it since.
func (j *lateJail) running() bool { return j.has("created") && !j.has("stopped") }

// clientRuns is whether the main process's client is still running.
func (j *lateJail) clientRuns() bool {
	raw, err := os.ReadFile(j.marker("spawned"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return err == nil && pid > 0 && syscall.Kill(pid, 0) == nil
}

// mainArgv is the main process's client: it records its pid, has the container come up after the
// lag in a process of its own that outlives the client, prints a boot line and holds until a stop,
// its boot never done.
func (j *lateJail) mainArgv() []string {
	script := "echo $$ > " + shquote.Quote(j.marker("spawned")) + "; "
	if j.lag >= 0 {
		script += fmt.Sprintf("( sleep %.3f; touch %s ) </dev/null >/dev/null 2>&1 & ", j.lag.Seconds(),
			shquote.Quote(j.marker("created")))
	}
	script += `echo "a boot line" >&2; while [ ! -e ` + shquote.Quote(j.marker("stopped")) +
		` ]; do sleep 0.02; done; exit 143`
	return []string{"sh", "-c", script}
}

func (j *lateJail) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case len(argv) > 1 && argv[1] == "stop":
		if j.clientRuns() {
			j.stopsUnderAClient++
		}
		if !j.running() {
			j.missed++
			return ExecResult{Ran: true, RC: 125, Stderr: "Error: no container with name or ID found"}
		}
		j.stops++
		_ = os.WriteFile(j.marker("stopped"), nil, 0o644)
		return ExecResult{Ran: true}
	case len(argv) > 1 && argv[1] == "ps":
		if j.running() {
			return ExecResult{Ran: true, Stdout: "abc123\n"}
		}
		return ExecResult{Ran: true}
	case len(argv) > 1 && argv[1] == "wait":
		return ExecResult{} // the client's exit is the observation here, as in fakeJail
	}
	return ExecResult{Ran: true}
}

// stopsSeen is how many stops ended the container and how many found none.
func (j *lateJail) stopsSeen() (int, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stops, j.missed
}

// leftRunning waits out the lag, so a container still on its way up has come up, and reports
// whether one runs.
func (j *lateJail) leftRunning() bool {
	if j.lag >= 0 && j.has("spawned") {
		deadline := time.Now().Add(j.lag + 2*time.Second)
		for !j.has("created") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return j.running()
}

// requireNothingLeft is the defect's whole assertion, after the keeper's end: no container runs, and
// none of the keeper's records is left, which is what a jail with a keeper nobody can prove dead
// would need.
func requireNothingLeft(t *testing.T, j *lateJail, cname string) {
	t.Helper()
	if j.leftRunning() {
		stops, missed := j.stopsSeen()
		t.Errorf("the container is running after its keeper ended (%d stops ended it, %d found nothing): "+
			"a jail with nothing that will end it", stops, missed)
	}
	if probeKeeper(cname) != keeperGone {
		t.Error("the keeper's liveness lock is still held after its end")
	}
	if _, ok := readOwnerPID(cname); ok {
		t.Error("the keeper left its owner-PID file")
	}
	if _, ok := readKeeperRecord(cname); ok {
		t.Error("the keeper left its start record")
	}
}

// TestAKeeperEndedBeforeItsContainerStartsNeverStartsIt: the launch's lifeline closing, which is
// what a Ctrl-C to the launch does, or a signal that ends the jail, received while the keeper was
// still starting the jail's host services, ends the launch there. The container's main process is
// never spawned, so no runtime can be left making a container nobody stops; the host services go,
// and the keeper's records with them.
func TestAKeeperEndedBeforeItsContainerStartsNeverStartsIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		// end ends the launch before the keeper's life begins; settle is how long the keeper then
		// waits to begin, so the lifeline's reader has seen its end.
		end    func(f *keeperFixture)
		settle time.Duration
		rc     int
		says   func(cname string) string
	}{
		{"the launch's lifeline closes", func(f *keeperFixture) { _ = f.lifeW.Close() }, 200 * time.Millisecond, 1,
			func(cname string) string {
				return "the launch that started " + cname + " is gone before its container started"
			}},
		{"the keeper is sent SIGINT", func(f *keeperFixture) {
			f.signals <- syscall.SIGHUP // dropped, as at every other turn
			f.signals <- syscall.SIGINT
		}, 0, 128 + int(syscall.SIGINT), func(cname string) string { return "before " + cname + "'s container started" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jail := newLateJail(t, lateJailLag)
			queued := make(chan struct{})
			f := startKeeperFixtureWith(t, false, func(p *keeperPlan) { p.RunCmd = jail.mainArgv() },
				func(o *Options) {
					o.Exec = jail.exec
					<-queued // the keeper's life begins once the launch has ended
					time.Sleep(tc.settle)
				})
			tc.end(f)
			close(queued)
			if f.relay() {
				t.Fatal("a keeper whose launch ended before its container started reached ready")
			}
			if rc := f.wait(); rc != tc.rc {
				t.Errorf("the keeper exited %d, want %d", rc, tc.rc)
			}
			if jail.has("spawned") {
				t.Error("the keeper spawned the container's main process after its launch had ended")
			}
			if !strings.Contains(f.errOut.String(), tc.says(f.cname)) {
				t.Errorf("the keeper did not say why it started no container:\n%s", f.errOut.String())
			}
			if _, err := os.Stat(f.plan.SocketsDir); !os.IsNotExist(err) {
				t.Errorf("the keeper left the host-services dir of a jail it never started: %v", err)
			}
			requireNothingLeft(t, jail, f.cname)
		})
	}
}

// TestAKeeperEndedWhileItsContainerStartsStopsItOnceItExists: the launch's lifeline closing, or a
// signal to the keeper, after the container's main process was spawned and before the runtime has
// made the container. The keeper's stop waits for the container to come up, so it ends it, rather
// than finding nothing and leaving the container the runtime finishes starting a moment later
// running with no keeper.
func TestAKeeperEndedWhileItsContainerStartsStopsItOnceItExists(t *testing.T) {
	for _, tc := range []struct {
		name   string
		end    func(f *keeperFixture)
		rc     int
		reason func(pid int) string
	}{
		{"the launch's lifeline closes", func(f *keeperFixture) { _ = f.lifeW.Close() }, 1, launchGoneReason},
		{"the keeper is sent SIGINT", func(f *keeperFixture) { f.signals <- syscall.SIGINT },
			128 + int(syscall.SIGINT), keeperSignalledReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jail := newLateJail(t, lateJailLag)
			f := startKeeperFixtureWith(t, false, func(p *keeperPlan) { p.RunCmd = jail.mainArgv() },
				func(o *Options) { o.Exec = jail.exec })
			ready := relayKeeper(f.progR, &f.out, &f.errOut, &f.jailOut, &f.jailErr, keeperEvents{
				spawned: func() { tc.end(f) },
			})
			if ready {
				t.Fatal("the relay saw ready from a boot that never finished")
			}
			if rc := f.wait(); rc != tc.rc {
				t.Errorf("the keeper exited %d, want %d", rc, tc.rc)
			}
			if stops, _ := jail.stopsSeen(); stops != 1 {
				t.Errorf("%d stops ended the container, want 1: the keeper stopped before the runtime had made it", stops)
			}
			if rec, _ := readJailStop(f.cname); rec.Reason != tc.reason(os.Getpid()) {
				t.Errorf("the stop recorded %q, want %q", rec.Reason, tc.reason(os.Getpid()))
			}
			requireNothingLeft(t, jail, f.cname)
		})
	}
}

// TestAKeeperWhoseContainerNeverComesUpEndsWithinItsBound: a runtime client that never brings its
// container up, and never exits, holds the keeper's stop no longer than keeperStartSettleWait. Then
// the keeper ends the client itself, after which the runtime makes nothing more, says so, and ends.
func TestAKeeperWhoseContainerNeverComesUpEndsWithinItsBound(t *testing.T) {
	saved := keeperStartSettleWait
	keeperStartSettleWait = 300 * time.Millisecond
	t.Cleanup(func() { keeperStartSettleWait = saved })
	jail := newLateJail(t, -1)
	f := startKeeperFixtureWith(t, false, func(p *keeperPlan) { p.RunCmd = jail.mainArgv() },
		func(o *Options) { o.Exec = jail.exec })
	ready := relayKeeper(f.progR, &f.out, &f.errOut, &f.jailOut, &f.jailErr, keeperEvents{
		spawned: func() { _ = f.lifeW.Close() },
	})
	if ready {
		t.Fatal("the relay saw ready from a boot that never finished")
	}
	if rc := f.wait(); rc != 1 {
		t.Errorf("the keeper exited %d, want 1", rc)
	}
	if !strings.Contains(f.keeperLog(), "did not come up within "+keeperStartSettleWait.String()) {
		t.Errorf("the keeper did not say it gave up waiting for the container:\n%s", f.keeperLog())
	}
	jail.mu.Lock()
	underAClient := jail.stopsUnderAClient
	jail.mu.Unlock()
	if underAClient != 0 {
		t.Errorf("%d stops were sent while the runtime client still ran with no container up: it could "+
			"still have brought one up after the stop and the existence probe", underAClient)
	}
	requireNothingLeft(t, jail, f.cname)
}

// TestASIGINTToTheLaunchRightAfterItsKeeperStartedLeavesNoContainerRunning is the defect from the
// launch's side, through every piece a fresh launch runs between its keeper's spawn and ready:
// startKeeper with TestMain's in-process keeper, which the launch's own signal never reaches (a real
// keeper runs in a session of its own), the launch's one signal arm around keeperPreReadyTeardown,
// and a real SIGINT to this process at each moment of the window: when the keeper says it started,
// which is what the terminal shows, and when it says it spawned the container's main process.
func TestASIGINTToTheLaunchRightAfterItsKeeperStartedLeavesNoContainerRunning(t *testing.T) {
	for _, moment := range []string{"started", "spawned"} {
		t.Run(moment, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			emptyLoopholeDirs(t)
			cname := "yolo-early-sigint-" + moment
			jail := newLateJail(t, lateJailLag)
			o := goldenOptions(t.TempDir(), t.TempDir())
			o.Exec = jail.exec
			o.PIDAlive = func(int) bool { return false }
			o.RestoreTerminal = func() {}
			cfg, err := encodeConfig(jsonx.NewOrderedMap())
			if err != nil {
				t.Fatal(err)
			}
			plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: "podman",
				Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false), RunCmd: jail.mainArgv(),
				ImageRef: "the-image"}
			kp, err := o.startKeeper(plan)
			if err != nil {
				t.Fatal(err)
			}
			codes := make(chan int, 1)
			arm := armLaunchSignalsWith(o.keeperPreReadyTeardown(kp, cname, "podman"), func(code int) { codes <- code })
			// The arm that fired never disarms (its exit would have ended the process), so its
			// handler goes here instead: a later signal to the test binary reaches no dead arm.
			t.Cleanup(func() { popLaunchArm(arm) })
			interrupt := func() {
				if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
					t.Error(err)
				}
			}
			events := keeperEvents{}
			if moment == "started" {
				events.started = func(int) { interrupt() }
			} else {
				events.spawned = interrupt
			}
			var out, errOut, jailOut, jailErr lockedBuffer
			if relayKeeper(kp.progress, &out, &errOut, &jailOut, &jailErr, events) {
				t.Fatal("the relay saw ready from a boot that never finished")
			}
			select {
			case code := <-codes:
				if code != 128+int(syscall.SIGINT) {
					t.Errorf("the launch exited %d, want %d", code, 128+int(syscall.SIGINT))
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the launch's arm never exited")
			}
			select {
			case <-kp.exited:
			default:
				t.Error("the launch's arm exited before its keeper had ended the jail")
			}
			requireNothingLeft(t, jail, cname)
		})
	}
}

// launchLockFree reports whether another process could take the launch lock at path now: what an
// arrival queued on it would do. A take that succeeds is let go at once.
func launchLockFree(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Errorf("opening the launch lock: %v", err)
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true
}

// TestAKeeperEndingBeforeReadyHoldsTheLaunchLockUntilItsContainerIsGone: a keeper whose launch ends
// while the runtime is still making the container keeps the launch lock it was handed until its stop
// has ended that container, and lets it go before its teardown chain. A second launch of the
// workspace, queued on that lock, then finds no container and waits for the keeper (JL-D28), rather
// than finding a container that runs, attaching to it, and having its first session fail as the stop
// lands, which a nested jail showed whenever the lock went before the wait for the container.
func TestAKeeperEndingBeforeReadyHoldsTheLaunchLockUntilItsContainerIsGone(t *testing.T) {
	for _, tc := range []struct {
		name, cname string
		end         func(lifeW *os.File, signals chan os.Signal)
	}{
		{"the launch's lifeline closes", "yolo-early-lock-lifeline",
			func(lifeW *os.File, _ chan os.Signal) { _ = lifeW.Close() }},
		{"the keeper is sent SIGINT", "yolo-early-lock-sigint",
			func(_ *os.File, signals chan os.Signal) { signals <- syscall.SIGINT }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			emptyLoopholeDirs(t)
			jail := newLateJail(t, lateJailLag)
			cname := tc.cname
			o := goldenOptions("/ws", t.TempDir())
			o.holdLaunchLock(cname)
			lockPath := launchLockPath(cname)
			handed, err := syscall.Dup(int(o.launchLock.f.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			o.launchLock.handOff()
			// What an arrival queued on the lock would see at each of the runtime's answers: a
			// container running while the lock is free is one it attaches to.
			var mu sync.Mutex
			var runningUnlocked, stopUnlocked bool
			exec := func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
				res := jail.exec(argv, dir, env, timeout)
				if len(argv) > 1 {
					free := launchLockFree(t, lockPath)
					mu.Lock()
					switch {
					case argv[1] == "ps" && strings.TrimSpace(res.Stdout) != "" && free:
						runningUnlocked = true
					case argv[1] == "stop" && free:
						stopUnlocked = true
					}
					mu.Unlock()
				}
				return res
			}
			cfg, err := encodeConfig(nil)
			if err != nil {
				t.Fatal(err)
			}
			plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: t.TempDir(), Cname: cname, Runtime: "podman",
				Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false), RunCmd: jail.mainArgv(), ImageRef: "img"}
			progR, progW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			lifeR, lifeW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lifeW.Close() })
			signals := make(chan os.Signal, 2)
			done := make(chan int, 1)
			go func() {
				done <- runKeeper(plan, KeeperSeams{}, progW, lifeR, os.NewFile(uintptr(handed), "lock"), nil, signals,
					func(ko *Options) {
						ko.Exec = exec
						ko.PIDAlive = func(int) bool { return false }
						ko.LookPath = func(string) (string, bool) { return "", false }
						ko.PathExists = func(string) bool { return false }
						ko.StartDetached = func([]string, *os.File) error { return errTestBinarySelfExec }
					})
				_ = progW.Close()
			}()
			var out, errOut, jailOut, jailErr lockedBuffer
			if relayKeeper(progR, &out, &errOut, &jailOut, &jailErr, keeperEvents{
				spawned: func() { tc.end(lifeW, signals) },
			}) {
				t.Fatal("the relay saw ready from a boot that never finished")
			}
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("the keeper did not end")
			}
			if stops, _ := jail.stopsSeen(); stops != 1 {
				t.Errorf("%d stops ended the container, want 1", stops)
			}
			mu.Lock()
			defer mu.Unlock()
			if runningUnlocked {
				t.Error("the container was running while the launch lock was free and the keeper was ending the " +
					"jail: an arrival queued on the lock would have attached to a jail about to be stopped")
			}
			if stopUnlocked {
				t.Error("the keeper's stop came after it let the launch lock go")
			}
			if !launchLockFree(t, lockPath) {
				t.Error("the keeper ended still holding the launch lock it was handed")
			}
			requireNothingLeft(t, jail, cname)
		})
	}
}
