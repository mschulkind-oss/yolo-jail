//go:build linux

package run

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
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

// windowAEventsFixture is a --rm container's events, stamped relative to now:
// it died `ago` before now, podman's teardown took `teardown`, and an attached
// exec session died just before the container did.
func windowAEventsFixture(ago, teardown time.Duration) string {
	die := time.Now().Add(-ago)
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
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(2*time.Second, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}

	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)

	total, ok := o.Perf.LastEvent("shutdown.window_a")
	if !ok || total.Dur < 1900*time.Millisecond || total.Dur > 2300*time.Millisecond {
		t.Fatalf("total = %v (%v), want ~2s under the OLD name so old logs still compare", total.Dur, ok)
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
		events      func() string
	}{
		{"no teardown event", "no_teardown", func() string {
			return fmt.Sprintf("%d died\n", time.Now().Add(-1500*time.Millisecond).UnixNano())
		}},
		{"teardown after exit", "teardown_after_exit", func() string {
			return fmt.Sprintf("%d died\n%d remove\n", time.Now().Add(-1500*time.Millisecond).UnixNano(),
				time.Now().Add(time.Hour).UnixNano())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := quietRecordingOptions(t, ws, home)
			o.Stderr = &bytes.Buffer{}
			o.Perf.Mark("child.exited")
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if len(argv) > 1 && argv[1] == "events" {
					return ExecResult{Ran: true, Stdout: tc.events()}
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
	return c, ptsName
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
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(1600*time.Millisecond, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
	o.emitTimingReport(0, "yolo-ws-test0000", "podman")

	if !strings.Contains(perfFile(t, ws), "note   shutdown.window_a.input_to_exit  no input forwarded this session") {
		t.Errorf("the input-gap note is missing on a launch that saw the death:\n%s", perfFile(t, ws))
	}
	got := errb.String()
	line := "yolo: podman stayed 1.6s after its container was removed, blocked in " + want
	if !strings.Contains(got, line) {
		t.Errorf("stderr missing %q:\n%s", line, got)
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
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(1500*time.Millisecond, 42*time.Millisecond)}
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
	o.lingerFinalSample("final (terminate arm)")
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(1500*time.Millisecond, 42*time.Millisecond)}
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
	if _, ok := o.Perf.LastEvent("shutdown.window_a.client_exit"); !ok {
		t.Error("the terminate arm recorded no split")
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
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: windowAEventsFixture(time.Second, 42*time.Millisecond)}
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

// The podman facts ride the `podman info` the loopback probe already runs.
func TestPodmanFactsAreRecordedFromTheExistingInfoCall(t *testing.T) {
	o := quietRecordingOptions(t, t.TempDir(), t.TempDir())
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	info := strings.Replace(podmanInfoFixture, `"rootlessNetworkCmd": "pasta",`,
		`"rootlessNetworkCmd": "pasta", "databaseBackend": "sqlite", "eventLogger": "journald", "cgroupManager": "systemd",`, 1)
	info = strings.Replace(info, `"store":`, `"version": {"Version": "5.8.6"}, "store":`, 1)
	infoCalls := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "info" {
			infoCalls++
			return ExecResult{Ran: true, Stdout: info}
		}
		return ExecResult{Ran: true}
	}
	o.hostLoopbackFactsFor("podman", "bridge")
	ev, ok := o.Perf.LastEvent("podman.facts")
	if !ok {
		t.Fatal("no podman.facts note")
	}
	if want := "version=5.8.6 database=sqlite events=journald rootless=true network=pasta cgroups=systemd"; ev.Detail != want {
		t.Errorf("facts = %q, want %q", ev.Detail, want)
	}
	if infoCalls != 1 {
		t.Errorf("podman info ran %d times; the facts must come from the one call already made", infoCalls)
	}
}

// THE CALL-SITE PINS for the two closures inside runContainer, which no unit
// test can invoke: onStarted must arm the probe with its own *os.Process and the
// id awaitRunningContainer returned, before the housekeeping slot; onTerminate
// must mark the signal and take the final sample BEFORE stopJail, the step
// that can itself hang.
func TestTheArmsCallTheProbe(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "run.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]token.Pos{}
	var armArgs []ast.Expr
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "runContainer" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if name == "Mark" && len(call.Args) == 1 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok {
					name = "Mark(" + lit.Value + ")"
				}
			}
			if name == "startLingerProbe" {
				armArgs = call.Args
			}
			calls[name] = append(calls[name], call.Pos())
			return true
		})
	}
	first := func(name string) token.Pos {
		if len(calls[name]) == 0 {
			t.Fatalf("runContainer no longer calls %s", name)
		}
		return calls[name][0]
	}
	if first("awaitRunningContainer") > first("startLingerProbe") ||
		first("startLingerProbe") > first("runHousekeeping") {
		t.Error("onStarted must learn the id, arm the probe, THEN run housekeeping (which can run a minute)")
	}
	if len(armArgs) != 4 {
		t.Fatalf("startLingerProbe args = %d", len(armArgs))
	}
	if id, ok := armArgs[3].(*ast.Ident); !ok || id.Name != "proc" {
		t.Error("the probe must be armed with onStarted's own *os.Process — the client it watches")
	}
	if id, ok := armArgs[2].(*ast.Ident); !ok || id.Name != "ctrID" {
		t.Error("the probe must be armed with the id awaitRunningContainer learned")
	}
	sig := first(`Mark("terminate.signal")`)
	final := first("lingerFinalSample")
	if sig > final || final > first("stopJail") {
		t.Error("onTerminate must mark the signal and take the final sample before stopJail")
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
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "events" {
			return ExecResult{Ran: true, Stdout: windowAEventsFixture(perf.SlowSpanThreshold/2, 42*time.Millisecond)}
		}
		return ExecResult{Ran: true}
	}
	o.teardownAfterExit(nil, "", nil, t.TempDir(), "yolo-ws-test0000", "podman", "", 0)
	if strings.Contains(errb.String(), "podman stayed") {
		t.Errorf("a prompt client got a lingering line:\n%s", errb.String())
	}
}
