package run

// keeper_test.go pins the keeper (keeper.go, keeperframe.go, keeperstate.go, keeperplan.go) and the
// fresh launch's side of it (keeperspawn.go): docs/design/jail-lifetime-last-session-wins.md §9,
// step 3 of its §7. The keeper's life is driven in-process against a fake main process (a shell
// that prints the boot's ready line and holds until the fake runtime's stop), since a test binary
// must never self-exec as the keeper; the integration suite runs the real one
// (integration/keeper_test.go).

import (
	"bufio"
	"bytes"
	"errors"
	"go/ast"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// inProcessKeeper is the package's keeper spawner under test (TestMain installs it): it runs the
// keeper on a goroutine of this process, with duplicates of the three descriptors a real spawn would
// hand a child, and with the launching Options' fakes, so a test of a whole launch drives a whole
// keeper with the same runtime.
//
// EACH DUPLICATE IS CLOSE-ON-EXEC, as KeeperMain's first act makes a real keeper's (JL-D29). dup(2)
// clears the flag, and Go's exec closes nothing it was not told to, so without it every process this
// keeper starts would inherit the progress pipe's write end: a real host daemon (a whole launch's
// keeper starts the brokers) outlives the keeper holding it, the launch's relay never reads the
// pipe's end, and the launch hangs (TestTheSealFixtureCrossesUnsealed did, for the package's whole
// timeout). The dup and the flag are one step under syscall.ForkLock, so no fork between them can
// take the descriptor either.
func inProcessKeeper(launch *Options, planPath string, progress, lifeline, lock *os.File,
	reserved []*os.File) (func() int, error) {
	dup := func(f *os.File) (*os.File, error) {
		if f == nil {
			return nil, nil
		}
		syscall.ForkLock.RLock()
		fd, err := syscall.Dup(int(f.Fd()))
		if err == nil {
			syscall.CloseOnExec(fd)
		}
		syscall.ForkLock.RUnlock()
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), f.Name()), nil
	}
	p, err := dup(progress)
	if err != nil {
		return nil, err
	}
	l, err := dup(lifeline)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	k, err := dup(lock)
	if err != nil {
		_ = p.Close()
		_ = l.Close()
		return nil, err
	}
	// The jail's reserved ports, each its own descriptor, as a spawned keeper's are: the launch
	// closes its copies once the spawn returns.
	var held []*os.File
	for _, f := range reserved {
		d, derr := dup(f)
		if derr != nil {
			err = derr
			break
		}
		held = append(held, d)
	}
	var plan *keeperPlan
	if err == nil {
		plan, err = readKeeperPlan(planPath)
	}
	if err != nil {
		_ = p.Close()
		_ = l.Close()
		if k != nil {
			_ = k.Close()
		}
		for _, f := range held {
			_ = f.Close()
		}
		return nil, err
	}
	done := make(chan int, 1)
	go func() {
		seams := KeeperSeams{}
		if c := launch.CaptureOnTerminate; c != nil {
			seams.CaptureOnTerminate = func(ws, rt string, _ func(string)) { c(ws, rt) }
		}
		rc := runKeeper(plan, seams, p, l, k, held, make(chan os.Signal), func(ko *Options) { adoptLaunchSeams(ko, launch) })
		_ = p.Close()
		done <- rc
	}()
	return func() int { return <-done }, nil
}

// adoptLaunchSeams hands a keeper the launching Options' fakes.
func adoptLaunchSeams(ko, launch *Options) {
	ko.Exec = launch.Exec
	ko.PIDAlive = launch.PIDAlive
	ko.PathExists = launch.PathExists
	ko.LookPath = launch.LookPath
	ko.StartDetached = launch.StartDetached
	ko.Getenv = launch.Getenv
	ko.IsMacOS, ko.IsLinux = launch.IsMacOS, launch.IsLinux
	ko.ServiceReadyTimeout, ko.ServiceTermGrace = launch.ServiceReadyTimeout, launch.ServiceTermGrace
	ko.HostCASProbe = launch.HostCASProbe
}

// fakeJail is a runtime for one keeper: a main process that is a shell, holding until the fake
// `podman stop` writes its stop file, and the probes a keeper makes of it. Its dir is a held directory
// (heldDir), so its main process never outlives the test, whether or not anything stopped it.
type fakeJail struct {
	mu      sync.Mutex
	dir     string
	cname   string
	stopped bool
	// stuck is a runtime whose stop ends nothing: the container runs on after it.
	stuck bool
	// pinned is a runtime whose stop ends the container but whose --rm removal fails, as podman's
	// does on an exec session it still counts as live and cannot take a handle on: the stopped
	// container stays, a plain rm fails on it, and a forced rm removes it and still exits 125
	// (MEASURED in nested podman, JL-D82).
	pinned bool
	// runningUnknown is a runtime that cannot answer whether the container runs: its running-only
	// listing fails, while its listing of every container still answers.
	runningUnknown bool
	stops          int
	calls          []string
	// execs, once reportExecs sets it, is how many exec sessions the runtime's inspect says the
	// container has, its main process a hold (jailSessionCount); 0 answers nothing, a count unknown.
	execs int
}

// reportExecs has the runtime answer that the container runs n exec sessions, its main process a
// hold, as podman's inspect does (jailSessionCount).
func (f *fakeJail) reportExecs(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = n
}

func newFakeJail(t *testing.T, cname string) *fakeJail {
	return &fakeJail{dir: heldDir(t), cname: cname}
}

// mainArgv is the main process: its boot, the ready line, and a hold its stop file ends. ready
// false leaves out the ready line, a boot that never finishes.
func (f *fakeJail) mainArgv(ready bool) []string {
	script := recordPID(f.dir) + `echo "a boot line" >&2; `
	if ready {
		script += `echo "` + entrypoint.BootReadyLine + `" >&2; `
	}
	script += holdUntil(filepath.Join(f.dir, "stop")) + `; exit 143`
	return []string{"sh", "-c", script}
}

func (f *fakeJail) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(argv, " ")
	f.calls = append(f.calls, joined)
	switch {
	case len(argv) > 1 && argv[1] == "stop":
		f.stops++
		if f.stuck {
			return ExecResult{Ran: true}
		}
		f.stopped = true
		_ = os.WriteFile(filepath.Join(f.dir, "stop"), nil, 0o644)
		return ExecResult{Ran: true}
	case len(argv) > 1 && argv[1] == "rm":
		if !f.pinned {
			return ExecResult{Ran: true}
		}
		if slices.Contains(argv, "--force") || slices.Contains(argv, "-f") {
			f.pinned = false
		}
		return ExecResult{Ran: true, RC: 125, Stderr: "Error: removing exec sessions: getting the PID handle: " +
			"openByHandleAt failed: operation not permitted"}
	case strings.Contains(joined, "ps -a -q"):
		if f.stopped && !f.pinned {
			return ExecResult{Ran: true}
		}
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	case strings.Contains(joined, "ps -q"):
		if f.runningUnknown {
			return ExecResult{Ran: true, RC: 125, Stderr: "Error: the runtime could not list its containers"}
		}
		if f.stopped {
			return ExecResult{Ran: true}
		}
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	case len(argv) > 1 && argv[1] == "wait":
		return ExecResult{} // the keeper's client exit is the observation here
	case f.execs > 0 && len(argv) > 1 && argv[1] == "inspect" && strings.Contains(joined, "ExecIDs"):
		return ExecResult{Ran: true, Stdout: strconv.Itoa(f.execs) + "\n"}
	case f.execs > 0 && len(argv) > 1 && argv[1] == "inspect" && strings.Contains(joined, ".Config.Env"):
		return ExecResult{Ran: true, Stdout: entrypoint.JailMainEnv + "=" + entrypoint.JailMainHold + "\n"}
	}
	return ExecResult{Ran: true}
}

func (f *fakeJail) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

// removals are the container removals the runtime was asked to run, forced or not.
func (f *fakeJail) removals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rm []string
	for _, c := range f.calls {
		if argv := strings.Fields(c); len(argv) > 1 && argv[1] == "rm" {
			rm = append(rm, c)
		}
	}
	return rm
}

// keeperFixture is one keeper, run in-process, and the launch's end of its descriptors.
type keeperFixture struct {
	t       *testing.T
	cname   string
	jail    *fakeJail
	plan    *keeperPlan
	signals chan os.Signal
	progR   *os.File
	lifeW   *os.File
	done    chan int

	out, errOut, jailOut, jailErr lockedBuffer
	started                       int
	sawRunning                    bool
}

// startKeeperFixture runs a keeper for a fresh plan. tune adjusts the plan before the spawn.
func startKeeperFixture(t *testing.T, ready bool, tune func(*keeperPlan)) *keeperFixture {
	t.Helper()
	return startKeeperFixtureWith(t, ready, tune, nil)
}

// startKeeperFixtureWith is startKeeperFixture whose keeper's Options tuneOpts adjusts last, after
// the fixture's own fakes.
func startKeeperFixtureWith(t *testing.T, ready bool, tune func(*keeperPlan), tuneOpts func(*Options)) *keeperFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	cname := "yolo-keeper-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	if len(cname) > 60 {
		cname = cname[:60]
	}
	f := &keeperFixture{t: t, cname: cname, jail: newFakeJail(t, cname), signals: make(chan os.Signal, 2),
		done: make(chan int, 1)}
	cfg, err := encodeConfig(jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	f.plan = &keeperPlan{Build: keeperBuildStamp(), Workspace: t.TempDir(), Cname: cname, Runtime: "podman",
		Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false),
		RunCmd: f.jail.mainArgv(ready), ImageRef: "the-image"}
	if tune != nil {
		tune(f.plan)
	}
	progR, progW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	lifeR, lifeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	f.progR, f.lifeW = progR, lifeW
	go func() {
		rc := runKeeper(f.plan, KeeperSeams{}, progW, lifeR, nil, nil, f.signals, func(o *Options) {
			o.Exec = f.jail.exec
			o.PIDAlive = func(int) bool { return false }
			o.LookPath = func(string) (string, bool) { return "", false }
			o.PathExists = func(string) bool { return false }
			o.StartDetached = func([]string, *os.File) error { return errTestBinarySelfExec }
			if tuneOpts != nil {
				tuneOpts(o)
			}
		})
		_ = progW.Close()
		f.done <- rc
	}()
	t.Cleanup(func() { _ = lifeW.Close() })
	return f
}

// relay reads the keeper's frames as the fresh launch does, until ready or the keeper's end.
func (f *keeperFixture) relay() bool {
	return relayKeeper(f.progR, &f.out, &f.errOut, &f.jailOut, &f.jailErr, keeperEvents{
		started: func(pid int) { f.started = pid },
		running: func() { f.sawRunning = true },
	})
}

// wait is the keeper's status, within a bound.
func (f *keeperFixture) wait() int {
	f.t.Helper()
	select {
	case rc := <-f.done:
		return rc
	case <-time.After(30 * time.Second):
		f.t.Fatal("the keeper did not end")
		return -1
	}
}

// keeperLog is the keeper's log.
func (f *keeperFixture) keeperLog() string {
	b, _ := os.ReadFile(keeperLogPath(f.cname))
	return string(b)
}

// TestTheKeeperDrainsOnTheLastSessionAndTearsDown is the keeper's normal life (§9.5 item 2): it
// relays the boot to the launch and swallows the ready line, holds the liveness lock and names
// itself in the owner-PID file and its start record, and once the last session's shared lock goes,
// takes the lock exclusively, records why, stops the container, runs the chain (the host-services
// dir goes) and frees everything it held.
func TestTheKeeperDrainsOnTheLastSessionAndTearsDown(t *testing.T) {
	var first *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		// The fresh launch counts its first session before the spawn (JL-D16).
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	if !strings.Contains(f.jailErr.String(), "a boot line") || strings.Contains(f.jailErr.String(), entrypoint.BootReadyLine) {
		t.Errorf("pid 1's boot was not relayed as it was, ready line swallowed: %q", f.jailErr.String())
	}
	if f.started != os.Getpid() || !f.sawRunning {
		t.Errorf("the keeper's moments: started=%d running=%v, want its pid and the container seen running", f.started, f.sawRunning)
	}
	if probeKeeper(f.cname) != keeperAlive {
		t.Error("the keeper does not hold the liveness lock")
	}
	if pid, ok := readOwnerPID(f.cname); !ok || pid != os.Getpid() {
		t.Errorf("the owner-PID file names %d (%v), want the keeper", pid, ok)
	}
	if rec, ok := readKeeperRecord(f.cname); !ok || rec.PID != os.Getpid() || rec.SocketsDir != f.plan.SocketsDir {
		t.Errorf("the start record is %+v (%v)", rec, ok)
	}
	if _, err := os.Stat(f.plan.SocketsDir); err != nil {
		t.Errorf("the keeper did not start the host services (their dir): %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if f.jail.stopCount() != 0 {
		t.Fatal("the keeper stopped the jail while its first session ran")
	}

	first.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the keeper stopped the jail %d times, want once", f.jail.stopCount())
	}
	if rec, _ := readJailStop(f.cname); rec.Reason != lastSessionLeftReason {
		t.Errorf("the stop recorded %q, want %q", rec.Reason, lastSessionLeftReason)
	}
	if probeKeeper(f.cname) != keeperGone {
		t.Error("the keeper's liveness lock is still held after its end")
	}
	if _, ok := readOwnerPID(f.cname); ok {
		t.Error("the keeper left its owner-PID file")
	}
	if _, ok := readKeeperRecord(f.cname); ok {
		t.Error("the keeper left its start record")
	}
	if _, err := os.Stat(f.plan.SocketsDir); !os.IsNotExist(err) {
		t.Errorf("the chain left the host-services dir: %v", err)
	}
	if held := sessionLockHeld(t, f.cname); held {
		t.Error("the keeper still holds the session lock after its end")
	}
	if !strings.Contains(f.keeperLog(), "the last session of "+f.cname+" left") {
		t.Errorf("the keeper's log does not say why it ended the jail:\n%s", f.keeperLog())
	}
}

// TestASignalledKeeperEndsTheJailInOrder: a SIGTERM ends the jail as a drain does, records its own
// reason, and the keeper exits 128+SIGTERM once the sessions its stop ended let their locks go
// (JL-D24, JL-D28 (2)). A SIGHUP, which only a kill can send a process with no terminal, is dropped.
func TestASignalledKeeperEndsTheJailInOrder(t *testing.T) {
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	f.signals <- syscall.SIGHUP
	time.Sleep(100 * time.Millisecond)
	if f.jail.stopCount() != 0 {
		t.Fatal("a SIGHUP ended the jail")
	}
	f.signals <- syscall.SIGTERM
	for f.jail.stopCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	session.release() // the stop ended the session; its launcher lets its lock go
	if rc := f.wait(); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("the keeper exited %d, want %d", rc, 128+int(syscall.SIGTERM))
	}
	if rec, _ := readJailStop(f.cname); rec.Reason != keeperSignalledReason(os.Getpid()) {
		t.Errorf("the stop recorded %q", rec.Reason)
	}
	if probeKeeper(f.cname) != keeperGone {
		t.Error("the signalled keeper left its liveness lock held")
	}
}

// TestAKeeperWhoseLaunchDiesBeforeReadyUnwinds is §9.5 item 1: the launch's lifeline closing before
// the boot is done ends the jail the keeper started, and its status is non-zero. It closes once the
// keeper has spawned the container's main process: a lifeline closed earlier starts no container
// at all (keeperearlysignal_test.go).
func TestAKeeperWhoseLaunchDiesBeforeReadyUnwinds(t *testing.T) {
	f := startKeeperFixture(t, false, nil)
	ready := relayKeeper(f.progR, &f.out, &f.errOut, &f.jailOut, &f.jailErr, keeperEvents{
		spawned: func() { _ = f.lifeW.Close() },
	})
	if rc := f.wait(); rc != 1 {
		t.Errorf("the keeper exited %d, want 1", rc)
	}
	if ready {
		t.Error("the relay saw ready from a boot that never finished")
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the keeper stopped the jail %d times, want once", f.jail.stopCount())
	}
	if rec, _ := readJailStop(f.cname); rec.Reason != launchGoneReason(os.Getpid()) {
		t.Errorf("the stop recorded %q", rec.Reason)
	}
	if probeKeeper(f.cname) != keeperGone {
		t.Error("the keeper left its liveness lock held")
	}
}

// TestAKeeperRefusesAPlanItCannotRunAsDisclosed is JL-D20: a plan of another build, and one whose
// config would start a host daemon the launch did not name, are refused before anything starts; the
// launch lock the launch handed over is released, and the keeper's own records go.
func TestAKeeperRefusesAPlanItCannotRunAsDisclosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		tune func(*keeperPlan)
		want string
	}{
		{"another build", func(p *keeperPlan) { p.Build = "0.0.0@elsewhere" }, "it was made by yolo 0.0.0@elsewhere"},
		{"an undisclosed daemon", func(p *keeperPlan) {
			cfg := jsonx.NewOrderedMap()
			lp := jsonx.NewOrderedMap()
			spec := jsonx.NewOrderedMap()
			spec.Set("command", []any{"true"})
			lp.Set("extra-daemon", spec)
			cfg.Set("loopholes", lp)
			raw, err := encodeConfig(cfg)
			if err != nil {
				panic(err)
			}
			p.Config = raw
		}, `it would start the host service "extra-daemon", which the launch did not disclose`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startKeeperFixture(t, true, tc.tune)
			if f.relay() {
				t.Fatal("a refused plan reached ready")
			}
			if rc := f.wait(); rc != 1 {
				t.Errorf("the keeper exited %d, want 1", rc)
			}
			if !strings.Contains(f.errOut.String(), "Refusing this launch's plan") || !strings.Contains(f.errOut.String(), tc.want) {
				t.Errorf("the refusal did not say why:\n%s", f.errOut.String())
			}
			if f.jail.stopCount() != 0 || strings.Contains(strings.Join(f.jail.calls, "\n"), "podman run") {
				t.Error("a refused plan started something")
			}
			if probeKeeper(f.cname) != keeperGone {
				t.Error("the refusing keeper left its liveness lock held")
			}
			if _, ok := readOwnerPID(f.cname); ok {
				t.Error("the refusing keeper left its owner-PID file")
			}
		})
	}
}

// TestTheKeepersPlanIsReadOnceFromAPrivateFile: 0600 in a 0700 directory of its own, never an argv,
// and gone once read (JL-D20).
func TestTheKeepersPlanIsReadOnceFromAPrivateFile(t *testing.T) {
	path, err := writeKeeperPlan(&keeperPlan{Workspace: "/ws", Cname: "yolo-x", Runtime: "podman", RunCmd: []string{"podman", "run"}})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the plan's mode is %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("the plan's directory mode is %v (%v), want 0700", di.Mode().Perm(), err)
	}
	p, err := readKeeperPlan(path)
	if err != nil || p.Cname != "yolo-x" {
		t.Fatalf("read %+v, %v", p, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("the plan and its directory outlived the read")
	}
	if _, err := readKeeperPlan(path); err == nil {
		t.Error("a plan was read twice")
	}
	if !strings.Contains(strings.Join(keeperArgv(path, true, 0), " "), "--plan "+path) {
		t.Error("the keeper's argv does not name the plan's file")
	}
}

// TestTheRelayRoutesEachStreamAndStopsAtReady: the keeper's own lines go to the launch's (teed)
// writers, pid 1's to the process's own streams, the moments to their callbacks, and nothing after
// ready is read; after ready the keeper writes its log and mirrors only its own lines.
func TestTheRelayRoutesEachStreamAndStopsAtReady(t *testing.T) {
	var pipe, logBuf, mirror bytes.Buffer
	s := &keeperSink{pipe: &pipe, log: &logBuf}
	keeperStream{s, frameStdout}.Write([]byte("keeper out\n"))
	keeperStream{s, frameStderr}.Write([]byte("keeper err\n"))
	keeperStream{s, frameJailStderr}.Write([]byte("boot li"))
	keeperStream{s, frameJailStderr}.Write([]byte("ne\n"))
	s.event(frameStarted, "42")
	s.sayReady(&mirror)
	keeperStream{s, frameStderr}.Write([]byte("after ready\n"))
	keeperStream{s, frameJailStderr}.Write([]byte("jail after ready\n"))

	var out, errOut, jailOut, jailErr bytes.Buffer
	pid := 0
	ready := relayKeeper(bufio.NewReader(&pipe), &out, &errOut, &jailOut, &jailErr, keeperEvents{started: func(n int) { pid = n }})
	if !ready || pid != 42 {
		t.Errorf("ready=%v pid=%d", ready, pid)
	}
	if out.String() != "keeper out\n" || errOut.String() != "keeper err\n" || jailErr.String() != "boot line\n" || jailOut.Len() != 0 {
		t.Errorf("routing: out=%q err=%q jailErr=%q jailOut=%q", out.String(), errOut.String(), jailErr.String(), jailOut.String())
	}
	if pipe.Len() != 0 {
		t.Errorf("the relay left %d bytes unread, or the sink wrote after ready", pipe.Len())
	}
	if !strings.Contains(mirror.String(), "after ready") || strings.Contains(mirror.String(), "jail after ready") {
		t.Errorf("the launch.log mirror holds %q; want the keeper's own lines after ready and never pid 1's", mirror.String())
	}
	for _, want := range []string{"keeper out", "keeper err", "boot line", "after ready", "jail after ready"} {
		if !strings.Contains(logBuf.String(), want) {
			t.Errorf("the keeper's log lacks %q:\n%s", want, logBuf.String())
		}
	}
}

// TestAWriteToAClosedPipeNeverEndsTheKeeper: the launch stops reading when its session ends; the
// keeper's next write fails and turns the pipe off, and its log still gets the line (JL-D29).
func TestAWriteToAClosedPipeNeverEndsTheKeeper(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	var logBuf bytes.Buffer
	s := &keeperSink{pipe: w, log: &logBuf}
	keeperStream{s, frameStderr}.Write([]byte("into a closed pipe\n"))
	if s.pipe != nil {
		t.Error("a failed write left the pipe on")
	}
	if !strings.Contains(logBuf.String(), "into a closed pipe") {
		t.Error("the line never reached the log")
	}
	_ = w.Close()
}

// TestOneKeeperHoldsTheLivenessLock: a second take fails while one is held, and the probe reads it.
func TestOneKeeperHoldsTheLivenessLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	live, err := holdLivenessLock("yolo-live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holdLivenessLock("yolo-live"); !errors.Is(err, errKeeperAlive) {
		t.Errorf("a second take got %v, want errKeeperAlive", err)
	}
	if probeKeeper("yolo-live") != keeperAlive {
		t.Error("the probe missed a held lock")
	}
	releaseLock(live)
	if probeKeeper("yolo-live") != keeperGone {
		t.Error("the probe missed a freed lock")
	}
}

// TestAQuittingSessionFindsWhatItLeft is JL-D57's probe: the keeper holding the session lock
// exclusively is the drain (last); another session's shared hold is others; a free liveness lock
// beside a start record is an unkept jail, whose last session gets both locks to reap with; and a
// jail with neither a keeper nor a record is an older yolo's.
func TestAQuittingSessionFindsWhatItLeft(t *testing.T) {
	const cname = "yolo-quit"
	withKeeper := func(t *testing.T) func() {
		live, err := holdLivenessLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		return func() { releaseLock(live) }
	}
	t.Run("the keeper drained", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		defer withKeeper(t)()
		ex, ok := tryExclusiveSessionLock(cname)
		if !ok {
			t.Fatal("could not stand in for the keeper's drain")
		}
		defer ex.release()
		o := goldenOptions("/ws", t.TempDir())
		if got, _ := o.probeAfterQuit(cname); got != quitLast {
			t.Errorf("got %v, want quitLast", got)
		}
	})
	t.Run("another session is in", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		defer withKeeper(t)()
		other, _, err := takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		defer other.release()
		o := goldenOptions("/ws", t.TempDir())
		if got, _ := o.probeAfterQuit(cname); got != quitOthers {
			t.Errorf("got %v, want quitOthers", got)
		}
	})
	t.Run("the keeper is about to drain", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		defer withKeeper(t)()
		// The keeper, blocked on the exclusive lock, takes it a moment after the quit.
		go func() {
			time.Sleep(50 * time.Millisecond)
			f, _ := openSessionLock(cname)
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
			time.Sleep(time.Second)
			_ = f.Close()
		}()
		o := goldenOptions("/ws", t.TempDir())
		if got, _ := o.probeAfterQuit(cname); got != quitLast {
			t.Errorf("got %v, want quitLast", got)
		}
	})
	t.Run("an unkept jail's last session", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := writeKeeperRecord(cname, keeperRecord{PID: 4242}); err != nil {
			t.Fatal(err)
		}
		o := goldenOptions("/ws", t.TempDir())
		got, locks := o.probeAfterQuit(cname)
		if got != quitUnkeptLast || locks.liveness == nil || locks.sessions == nil {
			t.Fatalf("got %v with %+v, want quitUnkeptLast holding both locks", got, locks)
		}
		if probeKeeper(cname) != keeperAlive {
			t.Error("the reaping session does not hold the liveness lock, so an arrival would not wait for it")
		}
		locks.release()
	})
	t.Run("an unkept jail with another session", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := writeKeeperRecord(cname, keeperRecord{PID: 4242}); err != nil {
			t.Fatal(err)
		}
		other, _, err := takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		defer other.release()
		o := goldenOptions("/ws", t.TempDir())
		if got, _ := o.probeAfterQuit(cname); got != quitUnkeptOthers {
			t.Errorf("got %v, want quitUnkeptOthers", got)
		}
		if probeKeeper(cname) != keeperGone {
			t.Error("the probe kept the liveness lock of a jail it does not reap")
		}
	})
	t.Run("an older yolo's jail", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		o := goldenOptions("/ws", t.TempDir())
		if got, _ := o.probeAfterQuit(cname); got != quitNoKeeper {
			t.Errorf("got %v, want quitNoKeeper", got)
		}
	})
}

// TestAnArrivalDuringADrainNeverWaitsHoldingTheLaunchLock is JL-D28: a session's count that meets the
// keeper holding the session lock returns at once, so its caller can let the launch lock go and wait
// for the keeper; a reaper's hold is still waited for.
func TestAnArrivalDuringADrainNeverWaitsHoldingTheLaunchLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-drain"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	ex, ok := tryExclusiveSessionLock(cname)
	if !ok {
		t.Fatal("could not stand in for the keeper's drain")
	}
	defer ex.release()
	start := time.Now()
	if _, contended, err := takeSessionLock(cname); !errors.Is(err, errKeeperDraining) || !contended {
		t.Errorf("got contended=%v err=%v, want errKeeperDraining", contended, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the count waited %s on a draining keeper", took)
	}
	if !keeperDraining(cname) {
		t.Error("keeperDraining missed the drain")
	}
	o := goldenOptions("/ws", t.TempDir())
	if !o.holdSessionLock(cname) || !o.keeperDrainSeen || o.sessionLock != nil {
		t.Errorf("holdSessionLock: drainSeen=%v lock=%v, want the drain seen and no count", o.keeperDrainSeen, o.sessionLock)
	}
}

// TestTheLastSessionWaitsForTheKeeperAndStreamsIt is JL-D11: a quit that finds the keeper draining
// prints the keeper's teardown lines as they land, and returns once the keeper is gone.
func TestTheLastSessionWaitsForTheKeeperAndStreamsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-stream"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	logF, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	from := keeperLogSize(cname)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = logF.WriteString("12:00:00.000 keeper: the last session of yolo-stream left; ending the jail\n")
		time.Sleep(100 * time.Millisecond)
		_, _ = logF.WriteString("12:00:00.100 keeper: done\n")
		_ = logF.Close()
		releaseLock(live)
	}()
	o := goldenOptions("/ws", t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.streamKeeperTeardown(cname, from)
	for _, want := range []string{"ending the jail", "keeper: done"} {
		if !strings.Contains(errBuf.String(), want) {
			t.Errorf("the stream lacks %q:\n%s", want, errBuf.String())
		}
	}
	if probeKeeper(cname) != keeperGone {
		t.Error("the stream returned before the keeper was gone")
	}
}

// TestAFirstSessionAStopEndedReturnsWhatItDid is JL-D59, keeping JL-D50's status: the fresh launch's
// own session, whose jail a recorded stop ended, returns 143 as it did when it was the container's
// main process, and prints why; an attach keeps its exec's status; and an end nothing recorded keeps
// the 137.
func TestAFirstSessionAStopEndedReturnsWhatItDid(t *testing.T) {
	fastStopRecordWait(t)
	for _, tc := range []struct {
		name     string
		recorded bool
		first    bool
		want     int
	}{
		{"the first session, a recorded stop", true, true, 143},
		{"an attach, a recorded stop", true, false, 137},
		{"the first session, nothing recorded", false, true, 137},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			o := goldenOptions("/ws", t.TempDir())
			var errBuf bytes.Buffer
			o.Stderr = &errBuf
			o.Now = time.Now
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				return ExecResult{Ran: true} // no container of the name is running
			}
			since := time.Now()
			if tc.recorded {
				writeJailStop("yolo-ended", jailStopRecord{At: since.Add(time.Millisecond), Reason: YoloStopReason(9)})
			}
			if got := o.endSession("yolo-ended", "podman", 137, since, 0, tc.first); got != tc.want {
				t.Errorf("status %d, want %d:\n%s", got, tc.want, errBuf.String())
			}
			if tc.recorded && !strings.Contains(errBuf.String(), "This session ended because its jail stopped: "+YoloStopReason(9)) {
				t.Errorf("the session did not say why:\n%s", errBuf.String())
			}
		})
	}
	// On Apple Container the first session's exec returns while `container ls` still lists the
	// ending jail (acListingBehind), and the 143 holds there too: run 37133569003 (2026-10-03,
	// container 1.1.0) recorded 137 after `yolo stop`.
	t.Run("the first session on Apple Container, a recorded stop its listing trails", func(t *testing.T) {
		fastJailGoneWait(t)
		t.Setenv("HOME", t.TempDir())
		o := goldenOptions("/ws", t.TempDir())
		var errBuf bytes.Buffer
		o.Stderr = &errBuf
		o.Now = time.Now
		asks := 0
		o.Exec = acListingBehind("yolo-ended", 2, 0, &asks)
		since := time.Now()
		writeJailStop("yolo-ended", jailStopRecord{At: since.Add(time.Millisecond), Reason: YoloStopReason(9)})
		if got := o.endSession("yolo-ended", "container", 137, since, 0, true); got != 143 {
			t.Errorf("status %d after %d asks, want 143:\n%s", got, asks, errBuf.String())
		}
		if !strings.Contains(errBuf.String(), "This session ended because its jail stopped: "+YoloStopReason(9)) {
			t.Errorf("the session did not say why:\n%s", errBuf.String())
		}
	})
}

// TestASessionThatLeavesOthersSaysSoAndReturns: §4.5's one line, with the count when the runtime
// can tell, and nothing waited on.
func TestASessionThatLeavesOthersSaysSoAndReturns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-others"
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
	o := goldenOptions("/ws", t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			if strings.Contains(strings.Join(argv, " "), "ExecIDs") {
				return ExecResult{Ran: true, Stdout: "1\n"}
			}
			return ExecResult{Ran: true, Stdout: entrypoint.JailMainEnv + "=" + entrypoint.JailMainHold + "\n"}
		}
		return ExecResult{Ran: true, Stdout: "abc123\n"}
	}
	start := time.Now()
	if rc := o.endSession(cname, "podman", 0, time.Now(), 0, true); rc != 0 {
		t.Errorf("rc %d", rc)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("a session that left others took %s to return", time.Since(start))
	}
	if !strings.Contains(errBuf.String(), "Jail yolo-others stays up for 1 other session") {
		t.Errorf("the line is missing:\n%s", errBuf.String())
	}
}

// TestTheReaperLeavesAJailWhoseKeeperIsAlive is JL-D7's liveness half: a dead owner PID is not an
// orphan while its keeper holds the liveness lock (a reused PID, or a keeper that is simply alive
// under another number), and once the lock is free, no session holds the jail and the owner is dead,
// the reap takes both locks, stops the jail and clears the dead owner's records.
func TestTheReaperLeavesAJailWhoseKeeperIsAlive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const name = "yolo-kept"
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPIDFile(name), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeKeeperRecord(name, keeperRecord{PID: 999999}); err != nil {
		t.Fatal(err)
	}
	stops := 0
	o := goldenOptions("/ws", t.TempDir())
	o.PIDAlive = func(int) bool { return false }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		j := strings.Join(argv, " ")
		switch {
		case strings.Contains(j, "--format {{.Names}} {{.State}}"):
			return ExecResult{Ran: true, Stdout: name + " running\n"}
		case len(argv) > 1 && argv[1] == "stop":
			stops++
		}
		return ExecResult{Ran: true}
	}
	live, err := holdLivenessLock(name)
	if err != nil {
		t.Fatal(err)
	}
	o.reapOrphanedJails("podman")
	if stops != 0 {
		t.Fatal("the reaper stopped a jail whose keeper holds its liveness lock")
	}
	releaseLock(live)
	o.reapOrphanedJails("podman")
	if stops != 1 {
		t.Fatalf("the reaper stopped a keeperless orphan %d times, want once", stops)
	}
	if _, ok := readOwnerPID(name); ok {
		t.Error("the reap left the dead owner's PID file")
	}
	if _, ok := readKeeperRecord(name); ok {
		t.Error("the reap left the dead keeper's start record")
	}
	// Apple Container: a keeper-era jail is reaped too; one with no start record is left.
	if err := os.WriteFile(ownerPIDFile(name), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "container" && argv[1] == "ls" {
			return ExecResult{Ran: true, Stdout: "ID IMAGE\n" + name + " img\n"}
		}
		if len(argv) > 1 && argv[1] == "stop" {
			stops++
		}
		return ExecResult{Ran: true}
	}
	o.reapOrphanedJails("container")
	if stops != 1 {
		t.Error("the reaper stopped an Apple Container jail no keeper started")
	}
	if err := writeKeeperRecord(name, keeperRecord{PID: 999999}); err != nil {
		t.Fatal(err)
	}
	o.reapOrphanedJails("container")
	if stops != 2 {
		t.Error("the reaper left an Apple Container jail whose keeper is gone")
	}
}

// TestTheLaunchLockSurvivesItsHandOff is JL-D31: the fresh launch closes its copy of the launch lock
// without the unlock that would release every copy, so the keeper's duplicate keeps it until the
// keeper lets it go.
func TestTheLaunchLockSurvivesItsHandOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.lock")
	lock, err := acquireWorkspaceLock(path, "/ws", lockNotices{})
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(lock.f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	child := os.NewFile(uintptr(fd), "child")
	lock.handOff()
	probe, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("the hand-off released the lock the keeper was handed")
	}
	releaseLock(child)
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("the keeper's release did not free it: %v", err)
	}
	if !lock.isClosed() {
		t.Error("the handed-off lock does not read as closed, so Run's deferred release would unlock it")
	}
}

// TestTheKeeperLineNamesWhatItHoldsAndItsLog is JL-D21's wording.
func TestTheKeeperLineNamesWhatItHoldsAndItsLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := goldenOptions("/ws", t.TempDir())
	line := o.keeperLine("yolo-x", []string{"claude-oauth-broker", "cgroup-delegate"}, 2, "podman")
	for _, want := range []string{"keeper: yolo internal daemon jail-keeper will hold",
		"the claude-oauth-broker service", "the cgroup-delegate service", "2 port forwards",
		"until its last session leaves", "log: " + keeperLogPath("yolo-x")} {
		if !strings.Contains(line, want) {
			t.Errorf("the keeper line lacks %q: %s", want, line)
		}
	}
}

// TestKeeperMainRefusesABadArgv: the verb's own argument errors are misuse, exit 2.
func TestKeeperMainRefusesABadArgv(t *testing.T) {
	for _, args := range [][]string{
		{"--progress-fd", "1", "--plan", "/x"},
		{"--lifeline-fd", "four", "--plan", "/x"},
		{"--reserved-fd", "2", "--plan", "/x"},
		{"--reserved-fd", "five", "--plan", "/x"},
		{"--progress-fd", "3"},
		{"--whatever"},
	} {
		if rc := KeeperMain(args, KeeperSeams{}); rc != 2 {
			t.Errorf("KeeperMain(%q) = %d, want 2", args, rc)
		}
	}
}

// TestAKeeperArgvWithoutAPlanLeavesItsDescriptorsAlone: an argv KeeperMain refuses must not have
// adopted the descriptors it names. It used to wrap each in an *os.File before seeing that --plan
// was missing, and the wrapper's finalizer closed the descriptor at a later garbage collection. In
// the test binary TestKeeperMainRefusesABadArgv's "--progress-fd 3" was then some other file's
// descriptor: on macOS, go test's own testlog, whose closing failed the package after every test
// had passed ("can't write .../testlog.txt: bad file descriptor").
func TestAKeeperArgvWithoutAPlanLeavesItsDescriptorsAlone(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if rc := KeeperMain([]string{"--progress-fd", strconv.Itoa(int(w.Fd()))}, KeeperSeams{}); rc != 2 {
		t.Fatalf("KeeperMain with no --plan = %d, want 2", rc)
	}
	for i := 0; i < 5; i++ {
		goruntime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("KeeperMain refused the argv, yet a descriptor it never took was closed behind it: %v", err)
	}
}

// TestTheKeepersChildrenEndWithIt pins JL-D32's call sites: a fronted daemon and a socat forward the
// keeper starts get the kernel's death signal, a launch's do not (keeperMode), and the forwards'
// socket dir is removed whole before anything is spawned into it.
func TestTheKeepersChildrenEndWithIt(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"loopholesruntime.go", "startExternalService"},
		{"network.go", "startPortForwards"},
	} {
		var gated bool
		ast.Inspect(funcDecl(t, tc.file, tc.fn), func(n ast.Node) bool {
			ifs, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}
			if sel, ok := ifs.Cond.(*ast.SelectorExpr); ok && sel.Sel.Name == "keeperMode" && callsIn(ifs.Body)["setChildDeathSignal"] {
				gated = true
			}
			return true
		})
		if !gated {
			t.Errorf("%s no longer gives a keeper's child the death signal", tc.fn)
		}
	}
	var removeAt, spawnAt int
	for i, st := range funcDecl(t, "network.go", "startPortForwards").Body.List {
		c := callsIn(st)
		if c["RemoveAll"] && removeAt == 0 {
			removeAt = i + 1
		}
		if c["Start"] && spawnAt == 0 {
			spawnAt = i + 1
		}
	}
	if removeAt == 0 || spawnAt == 0 || removeAt > spawnAt {
		t.Error("startPortForwards must remove its whole socket dir before it starts a forward")
	}
}

// TestAStaleForwardSocketIsGoneBeforeTheForwardsStart drives the removal: a socket a dead forward
// left, for a port the config no longer forwards, is gone once the forwards start.
func TestAStaleForwardSocketIsGoneBeforeTheForwardsStart(t *testing.T) {
	fakeSocatOnPath(t)
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "yolo-fwd-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "port-1111.sock")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Options{}
	fillDefaults(o)
	procs := o.startPortForwards([]PortForward{{LocalPort: 8080, HostPort: 8080}}, "x", dir)
	t.Cleanup(func() { cleanupPortForwarding(procs, dir) })
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a dropped forward's socket survived: %v", err)
	}
}
