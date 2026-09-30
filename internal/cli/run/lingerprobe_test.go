//go:build linux

package run

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

const lingerCtrID = "4f2a9c1be0d34f2a9c1be0d34f2a9c1be0d34f2a9c1be0d34f2a9c1be0d3aaaa"

// windowAEventsFixture is a --rm container's events, stamped relative to end,
// the moment Window A is measured to (windowAEnd: the child.exited mark, or the
// terminate arm's signal). The container died `ago` before end, podman's teardown
// took `teardown`, and an attached exec session died just before the container did.
//
// ANCHORED TO THE MARK, NEVER TO time.Now() AT QUERY TIME. Every duration the
// launch records is end minus one of these stamps, and the whole teardown chain
// (and stopLingerProbe) runs between the mark and the query. A now-relative stamp
// shortened every recorded duration by that work, tens of milliseconds under CPU
// load, and "podman stayed 1.6s" (then 1.558s, 8ms above the rounding edge)
// printed 1.5s.
func windowAEventsFixture(end time.Time, ago, teardown time.Duration) string {
	die := end.Add(-ago)
	return fmt.Sprintf("%d start\n%d exec_died\n%d died\n%d cleanup\n%d remove\n",
		die.Add(-time.Hour).UnixNano(), die.Add(-20*time.Millisecond).UnixNano(), die.UnixNano(),
		die.Add(teardown/2).UnixNano(), die.Add(teardown).UnixNano())
}

func perfFile(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// THE SPLIT, through the production call site (teardownAfterExit → recordWindowA)
// on a quiet launch: the total is still recorded under its old name, and the two
// halves beside it, and every podman event lands as a note with its offset from
// the death.
func TestWindowASplitsPodmanTeardownFromTheClientsExit(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	var errb bytes.Buffer
	o.Stderr = &errb
	o.Perf.Mark("child.exited")
	end := markAt(t, o, "child.exited")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, 2*time.Second, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}

	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)

	total, ok := o.Perf.LastEvent("shutdown.window_a")
	if !ok || total.Dur != 2*time.Second {
		t.Fatalf("total = %v (%v), want exactly died→child.exited = 2s under the OLD name so old logs still compare",
			total.Dur, ok)
	}
	td, ok := o.Perf.LastEvent("shutdown.window_a.podman_teardown")
	if !ok || td.Dur != 42*time.Millisecond {
		t.Errorf("podman_teardown = %v (%v), want exactly died→remove = 42ms", td.Dur, ok)
	}
	ce, ok := o.Perf.LastEvent("shutdown.window_a.client_exit")
	if !ok || ce.Dur+td.Dur != total.Dur {
		t.Errorf("client_exit = %v (%v); the halves must sum to the total %v", ce.Dur, ok, total.Dur)
	}
	file := perfFile(t, ws)
	for _, want := range []string{
		"end    shutdown.window_a.podman_teardown  dur=0.042s",
		"end    shutdown.window_a.client_exit  dur=",
		"note   shutdown.window_a.event  start -3600.000s",
		"note   shutdown.window_a.event  exec_died -0.020s",
		"note   shutdown.window_a.event  died +0.000s",
		"note   shutdown.window_a.event  cleanup +0.021s",
		"note   shutdown.window_a.event  remove +0.042s",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("host-perf.log missing %q:\n%s", want, file)
		}
	}
	// The client stayed ~2s after the removal on a launch that armed no probe
	// (onStarted never ran here): the line still prints, and says why it has no state.
	if !strings.Contains(errb.String(), "yolo: podman stayed 2.0s after its container was removed (not sampled)") {
		t.Errorf("no lingering-client line on a slow client_exit:\n%s", errb.String())
	}
	if strings.Contains(errb.String(), "client_exit took") {
		t.Errorf("the bare slow-span notice for client_exit printed beside the line that explains it:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "shutdown.window_a took") {
		t.Errorf("the total's own notice must still print:\n%s", errb.String())
	}
}

// No teardown event: the total alone, and a token saying why it is not split.
// A teardown stamped after the client was reaped cannot bound the client's wait.
func TestWindowAUnsplitSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		events      func(end time.Time) string
	}{
		{"no teardown event", "no_teardown", func(end time.Time) string {
			return fmt.Sprintf("%d died\n", end.Add(-1500*time.Millisecond).UnixNano())
		}},
		{"teardown after exit", "teardown_after_exit", func(end time.Time) string {
			return fmt.Sprintf("%d died\n%d remove\n", end.Add(-1500*time.Millisecond).UnixNano(),
				end.Add(time.Hour).UnixNano())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := quietRecordingOptions(t, ws, home)
			o.Stderr = &bytes.Buffer{}
			o.Perf.Mark("child.exited")
			end := markAt(t, o, "child.exited")
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if len(argv) > 1 && argv[1] == "events" {
					return ExecResult{Ran: true, Stdout: tc.events(end)}
				}
				return ExecResult{Ran: true}
			}
			o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
			if _, ok := o.Perf.LastEvent("shutdown.window_a"); !ok {
				t.Fatal("the total must still be recorded")
			}
			if _, ok := o.Perf.LastEvent("shutdown.window_a.client_exit"); ok {
				t.Error("split recorded with no usable teardown event")
			}
			if !strings.Contains(perfFile(t, ws), "mark   shutdown.window_a_unsplit."+tc.token) {
				t.Errorf("no %s token in the file", tc.token)
			}
		})
	}
}

// ---- the probe through the launch's own seams ---------------------------------

func openRunTestPty(t *testing.T) (master, slave *os.File, name string) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	name = "/dev/pts/" + strconv.Itoa(n)
	s, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	return m, s, name
}

// A stand-in for the lingering `podman run` client: `dd` on a pty slave, which
// sits in read(0) exactly as the stdin hypothesis says podman does. (Not `cat`:
// coreutils cat blocks in splice(0, …).)
func lingeringClient(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	master, slave, ptsName := openRunTestPty(t)
	c := exec.Command("dd", "bs=1", "count=1", "of=/dev/null")
	c.Stdin, c.Stdout = slave, slave
	if err := c.Start(); err != nil {
		t.Skipf("dd: %v", err)
	}
	slave.Close()
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait(); master.Close() })
	waitBlockedInRead(t, c.Process.Pid)
	return c, ptsName
}

// waitBlockedInRead returns once pid is blocked in read(2) on fd 0, the state every caller's
// "names the read" assertion is about. Start returns when dd has been exec'd, not when it
// reaches its read: on a loaded runner a sample taken at once caught dd still loading its
// locale ("open: /usr/lib/locale/…", CI run 36500602808), and the assertion failed on a
// correct probe. /proc/<pid>/syscall's first field is the syscall number (unix.SYS_READ is
// this arch's), the second its first argument.
func waitBlockedInRead(t *testing.T, pid int) {
	t.Helper()
	want := strconv.Itoa(unix.SYS_READ)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/syscall", pid))
		if err != nil {
			continue
		}
		if f := strings.Fields(string(b)); len(f) > 1 && f[0] == want && f[1] == "0x0" {
			return
		}
	}
	t.Fatalf("pid %d never blocked in read(0): the lingering client did not reach its read", pid)
}

func waitForFile(t *testing.T, ws, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName)); err == nil &&
			strings.Contains(string(b), want) {
			return
		}
	}
	t.Fatalf("host-perf.log never showed %q:\n%s", want, perfFile(t, ws))
}

// THE WHOLE CHAIN on a quiet launch: startLingerProbe (onStarted's call) arms on
// the container id, the exit file fires it, the samples reach host-perf.log AS
// THEY ARE TAKEN (a SIGKILL later loses none of them), and at the teardown the
// stderr line names the blocked call — then nothing more is written.
func TestLingeringClientIsSampledAndNamedAtAQuietQuit(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	var errb bytes.Buffer
	o.Stderr = &errb
	exits := t.TempDir()
	o.linger.exitDir = exits
	o.linger.delay, o.linger.interval = 30*time.Millisecond, 30*time.Millisecond

	client, ptsName := lingeringClient(t)
	o.startLingerProbe("podman", "yolo-ws-test0000", lingerCtrID[:12], client.Process)
	if o.linger.off == "start_failed" {
		t.Skip("linger probe failed to start (inotify watches exhausted / ENOSPC)")
	}
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(perfFile(t, ws), "window_a.sample") {
		t.Fatal("sampled before the container died — a normal session must cost nothing")
	}

	if err := os.WriteFile(filepath.Join(exits, lingerCtrID), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "read(0 → " + ptsName + ")"
	waitForFile(t, ws, want)
	file := perfFile(t, ws)
	for _, w := range []string{"mark   shutdown.window_a.exit_file_seen", "note   shutdown.window_a.host  podman_procs="} {
		if !strings.Contains(file, w) {
			t.Errorf("missing %q:\n%s", w, file)
		}
	}

	o.Perf.Mark("child.exited")
	end := markAt(t, o, "child.exited")
	// died 1.690s before the exit, removed 42ms after the death: the client stayed
	// exactly 1.648s. The two round apart (1.7 vs 1.6), so the line below can only
	// pass if it prints the client's stay rather than the whole window.
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, 1690*time.Millisecond, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
	o.emitTimingReport(0, "yolo-ws-test0000", "podman")

	file = perfFile(t, ws)
	for _, w := range []string{
		"note   shutdown.window_a.input_to_exit  no input forwarded this session",
		"end    shutdown.window_a.client_exit  dur=1.648s",
	} {
		if !strings.Contains(file, w) {
			t.Errorf("missing %q:\n%s", w, file)
		}
	}
	got := errb.String()
	for _, line := range []string{
		"yolo: shutdown.window_a took 1.690s",
		"yolo: podman stayed 1.6s after its container was removed, blocked in " + want,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("stderr missing %q:\n%s", line, got)
		}
	}
	settled := perfFile(t, ws)
	time.Sleep(150 * time.Millisecond)
	if after := perfFile(t, ws); after != settled {
		t.Errorf("the probe wrote after the report printed:\n%s", strings.TrimPrefix(after, settled))
	}
}

// A probe that could not be armed is recorded as a token, and the line says why.
func TestUnarmedProbeRecordsWhy(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	var errb bytes.Buffer
	o.Stderr = &errb
	client, _ := lingeringClient(t)
	o.startLingerProbe("podman", "yolo-ws-test0000", "", client.Process) // the id never appeared
	o.Perf.Mark("child.exited")
	end := markAt(t, o, "child.exited")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, 1500*time.Millisecond, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
	if !strings.Contains(perfFile(t, ws), "mark   shutdown.window_a_unsampled.no_ctr_id") {
		t.Errorf("no unsampled token:\n%s", perfFile(t, ws))
	}
	if !strings.Contains(errb.String(), "(not sampled: the container id was never learned)") {
		t.Errorf("line does not say why:\n%s", errb.String())
	}
	// And a non-podman runtime arms nothing and records nothing about it.
	o2 := quietRecordingOptions(t, t.TempDir(), home)
	o2.startLingerProbe("container", "yolo-ws-test0000", lingerCtrID, client.Process)
	if _, tok := o2.stopLingerProbe(); tok != "" {
		t.Errorf("Apple Container recorded an unsampled token %q; it has no Window A at all", tok)
	}
}

// THE TERMINATE ARM: a client the user gave up on (^Z, then `kill %1`) is still
// alive when the signal arrives, so there is no child.exited. Window A must
// still be recorded, cut at the signal and saying so.
func TestTerminateArmRecordsAWindowACutAtTheSignal(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	o.Stderr = &bytes.Buffer{}
	exits := t.TempDir()
	o.linger.exitDir = exits
	o.linger.delay, o.linger.interval = time.Hour, time.Hour // only the final sample runs

	client, ptsName := lingeringClient(t)
	o.startLingerProbe("podman", "yolo-ws-test0000", lingerCtrID[:12], client.Process)
	if o.linger.off == "start_failed" {
		t.Skip("linger probe failed to start (inotify watches exhausted / ENOSPC)")
	}
	if err := os.WriteFile(filepath.Join(exits, lingerCtrID), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ws, "shutdown.window_a.exit_file_seen")

	// What onTerminate does first (pinned in source by TestTheArmsCallTheProbe).
	o.Perf.Mark("terminate.signal")
	end := markAt(t, o, "terminate.signal") // windowAEnd's end on this arm: the cut
	o.lingerFinalSample("final (terminate arm)")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, 1500*time.Millisecond, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.recordWindowA("yolo-ws-test0000", "podman")

	file := perfFile(t, ws)
	if !strings.Contains(file, "final (terminate arm): pid ") || !strings.Contains(file, "read(0 → "+ptsName+")") {
		t.Errorf("no final sample naming the read:\n%s", file)
	}
	if !strings.Contains(file, "mark   shutdown.window_a_cut.signal") {
		t.Errorf("the cut is not marked:\n%s", file)
	}
	if ce, ok := o.Perf.LastEvent("shutdown.window_a.client_exit"); !ok {
		t.Error("the terminate arm recorded no split")
	} else if ce.Dur != 1458*time.Millisecond {
		t.Errorf("client_exit = %v, want exactly remove→signal = 1.458s: the window is cut AT the signal", ce.Dur)
	}
}

// The proxy's Observer is wired: runWithProxy (the production seam) hands
// forwarded input to noteForwardedInput. After the death each chunk is a note
// carrying its size — and a lone ^C its name — never the bytes; the gap from
// the last one to the exit is recorded at the teardown.
func TestForwardedInputAfterTheDeathIsTimedNeverRecorded(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	o.Stderr = &bytes.Buffer{}
	o.linger.deathSeen.Store(true) // the probe's OnDeath, as it leaves the slot

	master, slave, _ := openRunTestPty(t)
	defer master.Close()
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = slave, slave
	defer func() { os.Stdin, os.Stdout = origIn, origOut; slave.Close() }()

	done := make(chan int, 1)
	go func() {
		rc, _ := runWithProxy([]string{"sh", "-c",
			"stty raw -echo; dd bs=1 count=7 of=/dev/null 2>/dev/null; exit 3"}, nil, nil, o)
		done <- rc
	}()
	time.Sleep(300 * time.Millisecond)
	_, _ = master.Write([]byte("secret"))
	time.Sleep(100 * time.Millisecond)
	_, _ = master.Write([]byte{0x03})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child never exited")
	}
	if o.linger.ptyMode == nil {
		t.Error("runWithProxy did not hand the pty-mode reader to the slot (Observer.Pty)")
	}
	// The gap is written by recordWindowA itself, the production call site.
	o.Perf.Mark("child.exited")
	end := markAt(t, o, "child.exited")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, time.Second, 42*time.Millisecond)}
	}
	o.recordWindowA("yolo-ws-test0000", "podman")

	file := perfFile(t, ws)
	for _, want := range []string{
		"note   child.input  bytes=6\n",
		"note   child.input  bytes=1 key=ctrl-c\n",
		"note   shutdown.window_a.input_to_exit  gap=",
		"after_death=true chunks_after_death=2 key=ctrl-c",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("missing %q:\n%s", want, file)
		}
	}
	if strings.Contains(file, "secret") {
		t.Fatal("forwarded CONTENT reached the log — it can be a password")
	}
}

// Before the death, input is timed (two atomic stores) but never written.
func TestInputBeforeTheDeathWritesNothing(t *testing.T) {
	o := quietRecordingOptions(t, t.TempDir(), t.TempDir())
	o.noteForwardedInput(4, "")
	if _, ok := o.Perf.LastEvent("child.input"); ok {
		t.Error("a pre-death keystroke was logged: a whole session's typing would land in the file")
	}
	if o.linger.lastInput.Load() == 0 {
		t.Error("the last-input clock was not updated")
	}
}

// The podman facts ride the readiness gate's `podman info`, the launch's only one
// (docs/design/podman-reboot-readiness.md PR-D5, PR-D10): noted when runtime selection
// answers, and read from there by the host-loopback decision, which asks podman nothing.
func TestPodmanFactsAreRecordedFromTheReadinessGatesAnswer(t *testing.T) {
	o := quietRecordingOptions(t, t.TempDir(), t.TempDir())
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	info := strings.Replace(podmanInfoFixture, `"rootlessNetworkCmd": "pasta",`,
		`"rootlessNetworkCmd": "pasta", "databaseBackend": "sqlite", "eventLogger": "journald", "cgroupManager": "systemd",`, 1)
	info = strings.Replace(info, `"store":`, `"version": {"Version": "5.8.6"}, "store":`, 1)
	gate := answeringPodman(o, info)
	execInfo := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "info" {
			execInfo++
		}
		return ExecResult{Ran: true}
	}
	if rt, ok := o.resolveRuntime(nil); !ok || rt != "podman" {
		t.Fatalf("resolveRuntime = %q, %v", rt, ok)
	}
	ev, ok := o.Perf.LastEvent("podman.facts")
	if !ok {
		t.Fatal("no podman.facts note from the gate's answer")
	}
	if want := "version=5.8.6 database=sqlite events=journald rootless=true network=pasta cgroups=systemd"; ev.Detail != want {
		t.Errorf("facts = %q, want %q", ev.Detail, want)
	}
	if f := o.hostLoopbackFactsFor("podman", "bridge"); f.backend != "pasta" || !f.rootless {
		t.Errorf("the host-loopback decision did not read the gate's answer: %+v", f)
	}
	if gate.count() != 1 || execInfo != 0 {
		t.Errorf("podman info ran %d times at the gate and %d times after it; the launch asks once",
			gate.count(), execInfo)
	}
}

// THE CALL-SITE PINS for the probe, which moved with the main process's client into the keeper
// (docs/design/jail-lifetime-last-session-wins.md JL-D62): awaitRunning must learn the container's
// id, release the launch lock, then arm the probe with that id and the client's own *os.Process;
// the keeper's signal arm must take the final sample BEFORE it ends the jail, the step that can
// itself hang. No unit test can invoke either with a real client.
func TestTheArmsCallTheProbe(t *testing.T) {
	calls := map[string][]token.Pos{}
	var armArgs []ast.Expr
	for _, fn := range []string{"awaitRunning", "run"} {
		ast.Inspect(funcDecl(t, "keeper.go", fn), func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if name == "startLingerProbe" {
				armArgs = call.Args
			}
			calls[fn+"."+name] = append(calls[fn+"."+name], call.Pos())
			return true
		})
	}
	first := func(name string) token.Pos {
		if len(calls[name]) == 0 {
			t.Fatalf("the keeper no longer calls %s", name)
		}
		return calls[name][0]
	}
	if first("awaitRunning.probeRunningContainer") > first("awaitRunning.releaseLaunchLock") ||
		first("awaitRunning.releaseLaunchLock") > first("awaitRunning.startLingerProbe") {
		t.Error("awaitRunning must learn the id, release the launch lock, THEN arm the probe")
	}
	if len(armArgs) != 4 {
		t.Fatalf("startLingerProbe args = %d", len(armArgs))
	}
	if sel, ok := armArgs[3].(*ast.SelectorExpr); !ok || sel.Sel.Name != "Process" {
		t.Error("the probe must be armed with the main process's client's own *os.Process — the client it watches")
	}
	if id, ok := armArgs[2].(*ast.Ident); !ok || id.Name != "ctrID" {
		t.Error("the probe must be armed with the id the running wait learned")
	}
	// The keeper's signal branch: the final sample, then the jail's end.
	var sample, end token.Pos
	ast.Inspect(funcDecl(t, "keeper.go", "run"), func(n ast.Node) bool {
		cc, ok := n.(*ast.CommClause)
		if !ok {
			return true
		}
		for _, st := range cc.Body {
			ast.Inspect(st, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch skelCallee(call) {
				case "lingerFinalSample":
					sample = call.Pos()
				case "endJail":
					if sample != token.NoPos && end == token.NoPos {
						end = call.Pos()
					}
				}
				return true
			})
		}
		return true
	})
	if sample == token.NoPos || end == token.NoPos || sample > end {
		t.Error("the keeper's signal branch must take the final sample before it ends the jail")
	}
}

// perf.SlowSpanThreshold is the line's gate: a client that left promptly
// prints nothing extra.
func TestNoLingerLineUnderTheThreshold(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := quietRecordingOptions(t, ws, home)
	var errb bytes.Buffer
	o.Stderr = &errb
	o.Perf.Mark("child.exited")
	end := markAt(t, o, "child.exited")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(end, perf.SlowSpanThreshold/2, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
	if strings.Contains(errb.String(), "podman stayed") {
		t.Errorf("a prompt client got a lingering line:\n%s", errb.String())
	}
}
