package run

// hostdaemonsupervise_test.go pins the host-side supervisor of a jail's host daemons
// (hostdaemonsupervise.go; docs/design/host-daemon-ownership.md OQ-HD5, HD-D25, HD-D3) through its
// one call site, startExternalService: delete `go sup.run()` there and every restart, give-up and
// retired-exit test below fails.

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// fastHostDaemonRestarts runs HD-D25's policy on a millisecond scale for one test: the same shape
// (a first wait doubling to a ceiling, a bound on restarts in a row), so a crash loop takes
// milliseconds, not the production 31 seconds.
func fastHostDaemonRestarts(t *testing.T, initial, ceiling time.Duration, restarts int) {
	t.Helper()
	prev := hostDaemonRestarts
	hostDaemonRestarts = daemonRestartPolicy{initial: initial, ceiling: ceiling, restarts: restarts}
	t.Cleanup(func() { hostDaemonRestarts = prev })
}

// supervisedFixture is a test's view of one supervised daemon: its Options, the launch's terminal
// and launch.log halves, and the files its spawns record themselves in.
type supervisedFixture struct {
	t        *testing.T
	o        *Options
	terminal *syncBuffer
	launch   *syncBuffer
	pids     string
}

func newSupervisedFixture(t *testing.T) *supervisedFixture {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes and reads /proc")
	}
	t.Setenv("HOME", t.TempDir())
	f := &supervisedFixture{t: t, terminal: &syncBuffer{}, launch: &syncBuffer{},
		pids: filepath.Join(t.TempDir(), "pids")}
	f.o = &Options{}
	fillDefaults(f.o)
	// A LAUNCH'S WRITER, as attachLaunchLog installs it: the terminal, teed into launch.log.
	f.o.Stderr = teeLog{w: f.terminal, log: f.launch}
	return f
}

// spawnScript starts a socket-transport service whose argv is sh running script, with $1 the path
// the framework substituted and $PIDS the file each spawn appends its pid to.
func (f *supervisedFixture) spawnScript(name, script string, hd *loopholes.HostDaemon) loopholeDaemon {
	f.t.Helper()
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", script, name, "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("PIDS", f.pids)
	spec.Set("env", env)
	h, ok := f.o.startExternalService(name, spec, f.t.TempDir(), "", "", hd)
	if !ok {
		f.t.Fatalf("the service never came up:\n%s", f.terminal.String())
	}
	f.t.Cleanup(func() {
		h.stop()
		for _, pid := range f.spawned() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return h
}

// spawned is every pid a spawn of the service recorded, in order.
func (f *supervisedFixture) spawned() []int {
	raw, _ := os.ReadFile(f.pids)
	var pids []int
	for _, l := range strings.Fields(string(raw)) {
		if pid, err := strconv.Atoi(l); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// hostLog is the service's host log, logs/host-service-<name>.log.
func hostLog(name string) string {
	raw, _ := os.ReadFile(filepath.Join(paths.GlobalStorage(), "logs", "host-service-"+name+".log"))
	return string(raw)
}

// ended reports whether the handle's end closed within d.
func ended(h loopholeDaemon, d time.Duration) bool {
	select {
	case <-h.end.done:
		return true
	case <-time.After(d):
		return false
	}
}

// THE POLICY IS THE JAIL SIDE'S (HD-D25 (1)): one policy for both supervisors, from the same two
// constants, and the bound is the five restarts that backoff takes before its ceiling.
func TestTheHostDaemonRestartPolicyIsTheJailSidesBackoffBounded(t *testing.T) {
	want := daemonRestartPolicy{initial: time.Second, ceiling: 30 * time.Second, restarts: 5}
	if hostDaemonRestarts != want {
		t.Errorf("the host daemon restart policy is %+v, want %+v", hostDaemonRestarts, want)
	}
	if supervisor.RestartBackoffInitial != 1.0 || supervisor.RestartBackoffMax != 30.0 {
		t.Errorf("the jail side's backoff moved (%v, %v): HD-D25 takes it for both sides",
			supervisor.RestartBackoffInitial, supervisor.RestartBackoffMax)
	}
	var waits []time.Duration
	for d, i := want.initial, 0; i < want.restarts; i++ {
		waits = append(waits, d)
		d = min(d*2, want.ceiling)
	}
	if got := waits[len(waits)-1]; got != 16*time.Second {
		t.Errorf("the fifth restart waits %s, want 16s, the last step before the 30s ceiling", got)
	}
}

// A KILLED DAEMON IS RESTARTED, and the jail's front, published before the death, reaches the new
// one from its next request: the front dials the daemon's socket per connection. The death and the
// restart are lines in the daemon's host log and in launch.log, and not on the terminal.
func TestAKilledHostDaemonIsRestartedAndItsFrontServesAgain(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 10*time.Millisecond, 10*time.Second, 5)
	socketsDir := t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(t.TempDir(), "release")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "lingers", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("YJ_FRONT_CHILD_PID", pidFile)
	env.Set("YJ_FRONT_CHILD_RELEASE", release)
	spec.Set("env", env)
	h, ok := f.o.startExternalService("phoenix", spec, socketsDir, loopholes.TransportLoopbackTLS,
		"127.0.0.1", &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket, RequestEnd: loopholes.RequestEndFramed})
	if !ok {
		t.Fatalf("the fronted daemon never came up:\n%s", f.terminal.String())
	}
	defer h.stop()
	readPid := func() int {
		raw, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		return pid
	}
	first := readPid()
	if first == 0 {
		t.Fatal("the daemon never recorded its pid")
	}
	ping := func() string {
		conn, err := svcendpoint.DialLocal(h.hostPath, 5*time.Second)
		if err != nil {
			return "dial: " + err.Error()
		}
		defer conn.Close()
		boundConn(t, conn)
		if _, err := conn.Write([]byte("ping\n")); err != nil {
			return "write: " + err.Error()
		}
		reply, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return "read: " + err.Error()
		}
		return strings.TrimSpace(reply)
	}
	if got := ping(); got != "pong" {
		t.Fatalf("before the death the front answered %q, want pong", got)
	}

	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return strings.Contains(hostLog("phoenix"), "restarted (pid") }) {
		t.Fatalf("the killed daemon was never restarted; its host log:\n%s", hostLog("phoenix"))
	}
	if second := readPid(); second == first || !processLive(second) {
		t.Fatalf("after the restart the daemon's pid is %d (was %d): no new process serves", second, first)
	}
	if got := ping(); got != "pong" {
		t.Errorf("the front published before the death answered %q after the restart, want pong", got)
	}
	select {
	case <-h.end.done:
		t.Error("the handle's end closed for a death the supervisor restarted: a restarted daemon is not down")
	default:
	}

	for _, want := range []string{"host service 'phoenix' died mid-session (its process ended (signal: killed))",
		"restart 1 of 5", "host service 'phoenix' restarted (pid"} {
		if !strings.Contains(hostLog("phoenix"), want) {
			t.Errorf("the daemon's host log lacks %q:\n%s", want, hostLog("phoenix"))
		}
		if !strings.Contains(f.launch.String(), want) {
			t.Errorf("launch.log lacks %q:\n%s", want, f.launch.String())
		}
	}
	if strings.Contains(f.terminal.String(), "died mid-session") || strings.Contains(f.terminal.String(), "restarted (pid") {
		t.Errorf("a mid-session death reached the terminal, where the agent's interface runs:\n%s", f.terminal.String())
	}
}

// FIVE RESTARTS, THEN A DEATH THAT STANDS (HD-D25 (2), (3)): a daemon that dies straight after
// each start is restarted five times in a row and then given up on, the give-up naming the daemon
// and its log, and the handle's end closes so the keeper records it down.
func TestAHostDaemonThatKeepsDyingIsGivenUpOnAfterFiveRestarts(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	h := f.spawnScript("crashloop", `echo $$ >> "$PIDS"; : > "$1"; sleep 0.05; exit 3`, nil)
	if !ended(h, 20*time.Second) {
		t.Fatalf("supervision never gave up on a daemon that dies at every start; its host log:\n%s", hostLog("crashloop"))
	}
	if n := len(f.spawned()); n != 6 {
		t.Errorf("the daemon was started %d times, want 6: the first spawn and five restarts", n)
	}
	log := hostLog("crashloop")
	if n := strings.Count(log, "died mid-session"); n != 5 {
		t.Errorf("the host log has %d restart lines, want 5:\n%s", n, log)
	}
	giveUp := "host service 'crashloop' died (its process ended (exit status 3)) after 5 restarts in a row; giving up"
	for _, out := range []struct{ what, text string }{{"host log", log}, {"launch.log", f.launch.String()}} {
		if !strings.Contains(out.text, giveUp) || !strings.Contains(out.text, "host-service-crashloop.log") {
			t.Errorf("the %s does not give up naming the daemon and its log:\n%s", out.what, out.text)
		}
	}
	if how := h.end.how(); !strings.Contains(how, "exit status 3") || !strings.Contains(how, "gave up after 5 restarts") {
		t.Errorf("the end's account is %q, want the death and the give-up", how)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.spawned()); n != 6 {
		t.Errorf("a daemon given up on was started again (%d spawns)", n)
	}
}

// UP PAST THE CEILING RESETS THE COUNT (HD-D25 (2)): a daemon that dies only after living longer
// than the backoff's ceiling is not a crash loop, and is restarted past the bound. The control is
// the test above: the same death inside the ceiling gives up.
func TestAHostDaemonUpPastTheCeilingResetsTheRestartCount(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 100*time.Millisecond, 2)
	h := f.spawnScript("longlived", `echo $$ >> "$PIDS"; : > "$1"; sleep 0.3; exit 3`, nil)
	if !waitFor(15*time.Second, func() bool { return len(f.spawned()) >= 5 }) {
		t.Fatalf("the daemon was started %d times; its host log:\n%s", len(f.spawned()), hostLog("longlived"))
	}
	select {
	case <-h.end.done:
		t.Fatalf("supervision gave up on a daemon that lived past the ceiling each time:\n%s", hostLog("longlived"))
	default:
	}
	if strings.Contains(hostLog("longlived"), "giving up") {
		t.Errorf("the host log gave up past the bound of 2:\n%s", hostLog("longlived"))
	}
	if strings.Contains(hostLog("longlived"), "restart 2 of 2") {
		t.Errorf("the count did not reset after a life past the ceiling:\n%s", hostLog("longlived"))
	}
}

// A TEARDOWN'S STOP IS NEVER A DEATH (HD-D25 (5)), by both teardowns: the process-group kill, and
// detach, the SIGTERM a credential daemon finishes its work after.
func TestATeardownStopIsNeverRestarted(t *testing.T) {
	for _, tc := range []struct {
		name string
		hd   *loopholes.HostDaemon
	}{
		{"kill", nil},
		{"detach", &loopholes.HostDaemon{Detach: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisedFixture(t)
			fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
			f.o.ServiceTermGrace = 2 * time.Second
			name := "torn-" + tc.name
			h := f.spawnScript(name, `echo $$ >> "$PIDS"; : > "$1"; exec sleep 30`, tc.hd)
			pid := f.spawned()[0]
			h.stop()
			if !waitFor(5*time.Second, func() bool { return !processLive(pid) }) {
				t.Fatalf("the daemon (pid %d) outlived its stop", pid)
			}
			if !ended(h, 5*time.Second) {
				t.Error("supervision did not end with the teardown's stop")
			}
			time.Sleep(200 * time.Millisecond)
			if n := len(f.spawned()); n != 1 {
				t.Errorf("a daemon its teardown stopped was started again (%d spawns)", n)
			}
			if log := hostLog(name); strings.Contains(log, "died") || strings.Contains(log, "restart") {
				t.Errorf("the teardown's stop was reported as a death:\n%s", log)
			}
		})
	}
}

// A TEARDOWN DURING A RESTART LEAKS NOTHING: the stop marks the supervisor first, so a restart in
// its readiness wait is the process the teardown stops, and nothing is spawned after it.
func TestATeardownDuringARestartStopsTheRestartedProcess(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	f.o.ServiceTermGrace = time.Second
	// The first spawn serves and dies; every later one never becomes ready, so the restart sits in
	// its readiness wait.
	h := f.spawnScript("midrestart", `echo $$ >> "$PIDS"; if [ "$(wc -l < "$PIDS")" -eq 1 ]; then : > "$1"; sleep 0.1; exit 3; fi; exec sleep 30`, nil)
	if !waitFor(10*time.Second, func() bool { return len(f.spawned()) == 2 }) {
		t.Fatalf("the restart never spawned; host log:\n%s", hostLog("midrestart"))
	}
	restarted := f.spawned()[1]
	h.stop()
	if !waitFor(5*time.Second, func() bool { return !processLive(restarted) }) {
		t.Fatalf("the restart in flight (pid %d) outlived the teardown: it leaked", restarted)
	}
	if !ended(h, 5*time.Second) {
		t.Error("supervision did not end with the teardown's stop")
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(f.spawned()); n != 2 {
		t.Errorf("a process was spawned after the teardown (%d spawns)", n)
	}
}

// A RETIRED STATE DIRECTORY IS FINAL (HD-D3): a daemon that exits with
// hostservice.ExitStateDirRetired is reported once and never restarted, since a restart would
// recreate the directory a launch archived; the handle's end closes, so the keeper records it down.
func TestARetiredStateDirExitIsNotRestarted(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	h := f.spawnScript("retiree", `echo $$ >> "$PIDS"; : > "$1"; sleep 0.1; exit `+
		shquote.Quote(strconv.Itoa(hostservice.ExitStateDirRetired)), nil)
	if !ended(h, 10*time.Second) {
		t.Fatalf("supervision did not end for a retired state directory; host log:\n%s", hostLog("retiree"))
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(f.spawned()); n != 1 {
		t.Errorf("a daemon whose state directory was retired was started again (%d spawns)", n)
	}
	want := "host service 'retiree' stopped: its state directory was retired; not restarted"
	for _, out := range []struct{ what, text string }{{"host log", hostLog("retiree")}, {"launch.log", f.launch.String()}} {
		if strings.Count(out.text, want) != 1 {
			t.Errorf("the %s does not say the retired exit once:\n%s", out.what, out.text)
		}
		if strings.Contains(out.text, "died") {
			t.Errorf("the %s reports the deliberate exit as a death:\n%s", out.what, out.text)
		}
	}
	if how := h.end.how(); !strings.Contains(how, "state directory was retired") {
		t.Errorf("the end's account is %q, want the retirement", how)
	}
}

// A SERVICE STARTED UNDER THE PROGRESS LINE still reports supervisor and teardown lines through
// the launch's underlying stderr tee. The progress line is temporary; a daemon can outlive the
// step, and its supervisor and the stop closure must not keep writing to the finished line.
func TestSupervisorAndTeardownOutputSurviveProgressLine(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 5)
	f.o.Stdout = io.Discard
	f.o.Stderr = teeLog{w: f.terminal, log: f.launch}
	f.o.ServiceTermGrace = 25 * time.Millisecond

	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", `echo $$ >> "$PIDS"; : > "$1"; if [ "$(wc -l < "$PIDS")" -eq 1 ]; then sleep 0.2; exit 3; fi; kill -STOP $$; while :; do sleep 1; done`, "progress", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("PIDS", f.pids)
	spec.Set("env", env)
	var h loopholeDaemon
	if !f.o.withStderrProgress("Starting host services", func() bool {
		var ok bool
		h, ok = f.o.startExternalService("progress", spec, t.TempDir(), "", "", nil)
		return ok
	}) {
		t.Fatalf("the service never came up:\n%s", f.terminal.String())
	}
	t.Cleanup(func() { h.stop() })
	if !waitFor(10*time.Second, func() bool { return strings.Contains(f.launch.String(), "restart 1 of 5") }) {
		t.Fatalf("the supervisor line did not reach launch.log after progress ended:\n%s", f.launch.String())
	}
	if !waitFor(5*time.Second, func() bool {
		pids := f.spawned()
		if len(pids) < 2 {
			return false
		}
		state, alive := procState(pids[len(pids)-1])
		return alive && state == "T"
	}) {
		t.Fatal("the restarted service did not stop itself before teardown")
	}
	h.stop()
	if !strings.Contains(f.launch.String(), "ignored SIGTERM for 25ms") {
		t.Errorf("the teardown warning did not reach launch.log after progress ended:\n%s", f.launch.String())
	}
}

// launchLogOnly is the launch.log half of each writer a launch hands its services, and nothing of
// the terminal's.
func TestLaunchLogOnlyTakesTheLogHalfAlone(t *testing.T) {
	var term, outer, inner syncBuffer
	if w := launchLogOnly(&term); w != nil {
		t.Errorf("a bare terminal has a log half: %T", w)
	}
	w := launchLogOnly(teeLog{w: teeLog{w: &term, log: &inner}, log: &outer})
	if w == nil {
		t.Fatal("a stack of tees has no log half")
	}
	_, _ = w.Write([]byte("line\n"))
	if term.String() != "" || outer.String() != "line\n" || inner.String() != "line\n" {
		t.Errorf("terminal %q, outer log %q, inner log %q", term.String(), outer.String(), inner.String())
	}
	k := keeperStream{&keeperSink{}, frameStdout}
	if got, ok := launchLogOnly(k).(keeperStream); !ok || got != k {
		t.Errorf("a keeper's stream was not kept as its own routing: %T", launchLogOnly(k))
	}
}

// A RESTART THAT FAILS ITS READINESS WAIT IS A DEATH (HD-D28 (3)): it is abandoned (its group
// killed) and counted, so a daemon whose restarts never come up reaches the give-up, and no
// abandoned process is left running.
func TestARestartThatNeverBecomesReadyCountsAsADeath(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 2)
	f.o.ServiceReadyTimeout = 150 * time.Millisecond
	// The first spawn serves and dies; every later one lives on without ever serving.
	h := f.spawnScript("neverready", `echo $$ >> "$PIDS"; if [ "$(wc -l < "$PIDS")" -eq 1 ]; then : > "$1"; sleep 0.1; rm -f "$1"; exit 3; fi; exec sleep 30`, nil)
	if !ended(h, 20*time.Second) {
		t.Fatalf("supervision never gave up on restarts that never became ready; host log:\n%s", hostLog("neverready"))
	}
	if n := len(f.spawned()); n != 3 {
		t.Errorf("the daemon was started %d times, want 3: the first spawn and two restarts", n)
	}
	for _, pid := range f.spawned()[1:] {
		if !waitFor(5*time.Second, func() bool { return !processLive(pid) }) {
			t.Errorf("the abandoned restart (pid %d) is still running", pid)
		}
	}
	log := hostLog("neverready")
	if !strings.Contains(log, "(its restart did not become reachable within 150ms) after 2 restarts in a row; giving up") {
		t.Errorf("the host log does not give up on the failed readiness wait as the death:\n%s", log)
	}
}

// EXIT 85 DURING A RESTART'S READINESS WAIT IS AN ORDINARY FAILED RESTART (HD-D27 (3)): only a
// daemon that served and then exited 85 is a retired state directory; one that exits 85 before
// it serves is counted toward the give-up like any other failed restart.
func TestARestartThatExits85BeforeReadyIsAnOrdinaryFailedRestart(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 5*time.Millisecond, 10*time.Second, 2)
	h := f.spawnScript("early85", `echo $$ >> "$PIDS"; if [ "$(wc -l < "$PIDS")" -eq 1 ]; then : > "$1"; sleep 0.1; rm -f "$1"; exit 3; fi; exit `+
		shquote.Quote(strconv.Itoa(hostservice.ExitStateDirRetired)), nil)
	if !ended(h, 20*time.Second) {
		t.Fatalf("supervision never ended; host log:\n%s", hostLog("early85"))
	}
	if n := len(f.spawned()); n != 3 {
		t.Errorf("the daemon was started %d times, want 3: an exit 85 before ready was not retried", n)
	}
	log := hostLog("early85")
	if strings.Contains(log, "state directory was retired") || strings.Contains(h.end.how(), "retired") {
		t.Errorf("an exit 85 before the restart served was read as a retired state directory:\n%s", log)
	}
	if !strings.Contains(log, "(its restart exited at startup (exit status 85)) after 2 restarts in a row; giving up") {
		t.Errorf("the host log does not count the exit 85 as a failed restart up to the give-up:\n%s", log)
	}
}

// THE KILL PATH'S TEARDOWN REACHES THE GROUP EVEN WHEN ITS LEADER IS REAPED: a daemonizing wrapper
// exits 0 once its service is reachable, and a crashed leader may have forked a child before it
// died; either way the process the supervisor holds is gone and a child is left in its group. The
// group signal is what ends that child with the jail (a group whose members are all gone answers
// ESRCH, so no pid-reuse risk arises on this path). During a backoff the teardown also spawns
// nothing.
func TestATeardownReachesTheGroupOfAReapedLeader(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		backoff      time.Duration
	}{
		// The daemonizing wrapper: its child serves; it exits 0, and supervision ends there.
		{"wrapper", `echo $$ >> "$PIDS"; sleep 30 & echo $! >> "$PIDS"; : > "$1"; exit 0`, 5 * time.Millisecond},
		// A crashed leader with a forked child, the teardown landing in the backoff wait.
		{"backoff", `echo $$ >> "$PIDS"; sleep 30 & echo $! >> "$PIDS"; : > "$1"; sleep 0.1; exit 3`, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisedFixture(t)
			fastHostDaemonRestarts(t, tc.backoff, 30*time.Second, 5)
			f.o.ServiceTermGrace = time.Second
			name := "reaped-" + tc.name
			h := f.spawnScript(name, tc.script, nil)
			if !waitFor(5*time.Second, func() bool { return len(f.spawned()) == 2 && !processLive(f.spawned()[0]) }) {
				t.Fatalf("the leader never exited; spawns %v, host log:\n%s", f.spawned(), hostLog(name))
			}
			if tc.name == "backoff" && !waitFor(5*time.Second, func() bool { return strings.Contains(hostLog(name), "restarting it in 10s") }) {
				t.Fatalf("the death was never reported; host log:\n%s", hostLog(name))
			}
			child := f.spawned()[1]
			if !processLive(child) {
				t.Fatalf("the child (pid %d) is not running before the teardown", child)
			}
			h.stop()
			if !waitFor(5*time.Second, func() bool { return !processLive(child) }) {
				t.Errorf("the child in the reaped leader's group (pid %d) outlived its jail's teardown", child)
			}
			if n := len(f.spawned()); n != 2 {
				t.Errorf("a process was spawned after the teardown (%d pids recorded)", n)
			}
		})
	}
}

// THE DETACH PATH SIGNALS NO PROCESS ALREADY REAPED: detach signals a bare pid, and once the
// child's Wait has returned (during a backoff wait, or after a give-up) that pid may be another
// process's. A stand-in process plays the reused pid: it must not be signaled.
func TestADetachTeardownSignalsNoProcessAlreadyReaped(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	o := &Options{}
	fillDefaults(o)
	var out syncBuffer
	o.Stderr = &out
	stranger := exec.Command("sleep", "30")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	strangerExited := make(chan struct{})
	go func() { _ = stranger.Wait(); close(strangerExited) }()
	t.Cleanup(func() { _ = stranger.Process.Kill() })
	reaped := make(chan struct{})
	close(reaped)
	hd := &loopholes.HostDaemon{Detach: true}
	o.stopService(o.pr(o.Stderr), "detached", hd, stranger, reaped)
	select {
	case <-strangerExited:
		t.Fatal("the detach teardown signaled the pid of a process already reaped")
	case <-time.After(200 * time.Millisecond):
	}
	// The control: while the child's exit is still unseen, detach signals it.
	o.stopService(o.pr(o.Stderr), "detached", hd, stranger, make(chan struct{}))
	select {
	case <-strangerExited:
	case <-time.After(5 * time.Second):
		t.Error("the detach teardown did not signal a process still running")
	}
}

// verbStoppable starts a fronted daemon at a jail's front socket (in a private FrontSocketDir),
// whose argv names that socket after `--socket` as yolo's own daemons do, so `yolo host-daemon
// stop` finds it by pgrep. before runs with the socket's path ahead of the first spawn. It returns
// the handle and a reader of the pid the daemon last recorded.
func (f *supervisedFixture) verbStoppable(name string, before func(socket string)) (loopholeDaemon, func() int) {
	f.t.Helper()
	saved := paths.FrontSocketDir
	paths.FrontSocketDir = shortSocketDir(f.t)
	f.t.Cleanup(func() { paths.FrontSocketDir = saved })
	socketsDir := f.t.TempDir()
	if err := os.Chmod(socketsDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if before != nil {
		before(frontSocketFile(frontShortHash(socketsDir), name))
	}
	release := filepath.Join(f.t.TempDir(), "release")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		f.t.Fatal(err)
	}
	pidFile := filepath.Join(f.t.TempDir(), "pid")
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "lingers", "{socket}", "--socket", "{socket}"})
	env := jsonx.NewOrderedMap()
	env.Set("YJ_FRONT_CHILD_PID", pidFile)
	env.Set("YJ_FRONT_CHILD_RELEASE", release)
	spec.Set("env", env)
	h, ok := f.o.startExternalService(name, spec, socketsDir, loopholes.TransportLoopbackTLS,
		"127.0.0.1", &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket, RequestEnd: loopholes.RequestEndFramed})
	if !ok {
		f.t.Fatalf("the fronted daemon never came up:\n%s", f.terminal.String())
	}
	f.t.Cleanup(h.stop)
	return h, func() int {
		raw, _ := os.ReadFile(pidFile)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		return pid
	}
}

// STOP MEANS STOP (HD-D28): `yolo host-daemon stop` (and the `yolo broker stop` alias) ends a
// jail's copy, and the jail's supervisor does not read that death as a crash: it is not restarted,
// supervision ends with the stop as its account, so the keeper records the service down, and the
// stop is a line in the daemon's host log and in launch.log, never "died mid-session".
func TestAStopByTheVerbIsNotRestartedByTheJailsSupervisor(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 10*time.Millisecond, 10*time.Second, 5)
	h, pid := f.verbStoppable("verbstop", nil)
	first := pid()
	var out strings.Builder
	deps := broker.CLIRealDeps()
	deps.Out, deps.Err, deps.Color = &out, &out, false
	broker.Stop(deps, "verbstop")
	if !strings.Contains(out.String(), "Stopped verbstop for jail") {
		t.Fatalf("the verb did not stop the jail's copy:\n%s", out.String())
	}
	if !ended(h, 5*time.Second) {
		t.Fatalf("supervision did not end with the verb's stop; host log:\n%s", hostLog("verbstop"))
	}
	if how := h.end.how(); !strings.Contains(how, "stopped by `yolo host-daemon stop`") {
		t.Errorf("the end's account is %q, want the verb's stop", how)
	}
	time.Sleep(200 * time.Millisecond)
	if again := pid(); again != first || processLive(first) {
		t.Errorf("the copy the verb stopped was started again (pid %d, was %d)", again, first)
	}
	want := "host service 'verbstop' was stopped by `yolo host-daemon stop`; not restarted"
	for _, o := range []struct{ what, text string }{{"host log", hostLog("verbstop")}, {"launch.log", f.launch.String()}} {
		if !strings.Contains(o.text, want) {
			t.Errorf("the %s lacks %q:\n%s", o.what, want, o.text)
		}
		if strings.Contains(o.text, "died mid-session") || strings.Contains(o.text, "restarted (pid") {
			t.Errorf("the %s reports the verb's stop as a death:\n%s", o.what, o.text)
		}
	}
	if _, err := os.Lstat(hostservice.StopMarkerPath(h.upstream)); !os.IsNotExist(err) {
		t.Errorf("the stop marker outlived its purpose (%v)", err)
	}
}

// A STALE STOP MARKER STOPS NOTHING: one an earlier copy at the same socket left is removed by the
// next first spawn there, and one naming another pid never stands for this copy's death, so a
// genuine crash is still restarted.
func TestAStaleStopMarkerDoesNotSuppressALaterCrashsRestart(t *testing.T) {
	f := newSupervisedFixture(t)
	fastHostDaemonRestarts(t, 10*time.Millisecond, 10*time.Second, 5)
	h, pid := f.verbStoppable("stale", func(socket string) {
		// What an earlier launch of this jail left: a marker for a copy no supervisor watched.
		if err := hostservice.WriteStopMarker(socket, []int{1}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Lstat(hostservice.StopMarkerPath(h.upstream)); !os.IsNotExist(err) {
		t.Errorf("the first spawn left an earlier copy's stop marker in place (%v)", err)
	}
	first := pid()
	// One written for another pid at this socket.
	if err := hostservice.WriteStopMarker(h.upstream, []int{first + 1}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return strings.Contains(hostLog("stale"), "restarted (pid") }) {
		t.Fatalf("a crash beside a stale stop marker was not restarted; host log:\n%s", hostLog("stale"))
	}
	if strings.Contains(hostLog("stale"), "host-daemon stop") {
		t.Errorf("a stale marker was read as the verb's stop:\n%s", hostLog("stale"))
	}
	if _, err := os.Lstat(hostservice.StopMarkerPath(h.upstream)); !os.IsNotExist(err) {
		t.Errorf("the restart left the stale marker in place (%v)", err)
	}
}

// A STOP BY THE VERB DURING A RESTART'S READINESS WAIT IS STILL A STOP: the restarted process,
// stopped by `yolo host-daemon stop` before its wait passed, fails that wait, and the supervisor
// reads the marker for its pid rather than counting a failed restart and spawning another.
// TestRestartInputExitWitnessWaitsForCurrentProcess proves done is not an
// exit witness during restart readiness, even after the original process died.
func TestDetachedWrapperStoppedAfterExitRetainsPrivateInput(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	<-exited
	s := newDaemonSupervisor("wrapper-input-fixture", "", cmd, exited)
	s.wrapperExit = func(*exec.Cmd) bool { return true }
	observed, unknown := s.inputExitObserved()
	s.stop(func(*exec.Cmd, <-chan struct{}) {})
	s.run()
	select {
	case <-unknown:
	case <-time.After(time.Second):
		t.Fatal("detached wrapper did not retain unobserved private input")
	}
	select {
	case <-observed:
		t.Fatal("wrapper exit was mistaken for daemon exit")
	default:
	}
}

func TestRestartInputExitWitnessWaitsForCurrentProcess(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	spawn := func() (*exec.Cmd, <-chan struct{}, error) {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		return cmd, exited, nil
	}
	first, firstExited, err := spawn()
	if err != nil {
		t.Fatal(err)
	}
	s := newDaemonSupervisor("input-exit-fixture", "", first, firstExited)
	s.policy = daemonRestartPolicy{initial: 5 * time.Millisecond, ceiling: time.Second, restarts: 1}
	s.say = func(string) {}
	s.wrapperExit = func(*exec.Cmd) bool { return false }
	restarted := make(chan *exec.Cmd, 1)
	s.start = func() (*exec.Cmd, <-chan struct{}, error) {
		cmd, exited, err := spawn()
		if err == nil {
			restarted <- cmd
		}
		return cmd, exited, err
	}
	s.ready = func(*exec.Cmd, <-chan struct{}) string {
		<-s.stopCh // teardown interrupts readiness without waiting for this process
		return "stopped during readiness"
	}
	s.abandon = func(*exec.Cmd) {}
	observed, _ := s.inputExitObserved()
	go s.run()
	t.Cleanup(func() {
		s.stop(func(c *exec.Cmd, _ <-chan struct{}) { _ = c.Process.Kill() })
		_ = first.Process.Kill()
	})
	_ = first.Process.Kill()
	var second *exec.Cmd
	select {
	case second = <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restarted process")
	}
	s.stop(func(*exec.Cmd, <-chan struct{}) {}) // detached process stays live
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("teardown did not close supervision")
	}
	select {
	case <-observed:
		t.Fatal("supervisor done was mistaken for current process exit")
	default:
	}
	if err := second.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("restarted process exit was never observed")
	}
}

func TestAStopByTheVerbDuringARestartsReadinessWaitIsNotRestarted(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	socket := filepath.Join(t.TempDir(), "d.sock")
	spawn := func() (*exec.Cmd, <-chan struct{}, error) {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		return cmd, exited, nil
	}
	first, firstExited, err := spawn()
	if err != nil {
		t.Fatal(err)
	}
	var lines syncBuffer
	s := newDaemonSupervisor("midready", "", first, firstExited)
	s.policy = daemonRestartPolicy{initial: 5 * time.Millisecond, ceiling: 10 * time.Second, restarts: 5}
	s.say = func(l string) { _, _ = lines.Write([]byte(l + "\n")) }
	s.wrapperExit = func(*exec.Cmd) bool { return false }
	s.stoppedByVerb = func(pid int) bool { return hostservice.TakeStopMarker(socket, pid) }
	restarts := 0
	s.start = func() (*exec.Cmd, <-chan struct{}, error) { restarts++; return spawn() }
	// The verb's stop, as it lands mid-wait: the marker for the restarted pid, then its signal.
	s.ready = func(cmd *exec.Cmd, exited <-chan struct{}) string {
		if err := hostservice.WriteStopMarker(socket, []int{cmd.Process.Pid}); err != nil {
			t.Error(err)
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-exited
		return "exited at startup (signal: terminated)"
	}
	s.abandon = func(*exec.Cmd) {}
	go s.run()
	t.Cleanup(func() { s.stop(func(c *exec.Cmd, _ <-chan struct{}) { _ = c.Process.Kill() }) })
	_ = first.Process.Kill()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("supervision did not end with the verb's stop mid-restart; lines:\n%s", lines.String())
	}
	if how := s.end().how(); how != stoppedByVerbAccount {
		t.Errorf("the end's account is %q, want %q", how, stoppedByVerbAccount)
	}
	if restarts != 1 {
		t.Errorf("the supervisor spawned %d restarts, want 1: the stopped one was restarted", restarts)
	}
	if !strings.Contains(lines.String(), "was stopped by `yolo host-daemon stop`; not restarted") {
		t.Errorf("the stop was not said:\n%s", lines.String())
	}
}
