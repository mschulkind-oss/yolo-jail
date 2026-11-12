//go:build linux

package run

import (
	"bytes"
	"context"
	"errors"
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

	"github.com/mschulkind-oss/yolo-jail/internal/lingerprobe"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	b, err := os.ReadFile(paths.WorkspaceLogPath(ws, HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lingerFailureEvidence is what the quiet-quit test prints beside its stderr assertion, which
// names the blocked call but not the process that made it. It holds the fixture's resolved
// command, the root pid's exe and start time, what /proc shows of the tree now (before
// cleanup kills it), and the perf log the test already read. It only reads: a pid it cannot
// see is reported as gone, so the report never fails in its place.
func lingerFailureEvidence(perfLog, cmdPath string, pid int) string {
	s := lingerprobe.Sampler{Root: "/proc"}
	var b strings.Builder
	fmt.Fprintf(&b, "fixture cmd.Path=%q root pid=%d\n", cmdPath, pid)
	exe, err := os.Readlink(filepath.Join(s.Root, strconv.Itoa(pid), "exe"))
	if err != nil {
		exe = "unreadable (" + err.Error() + ")"
	}
	fmt.Fprintf(&b, "root exe=%s\n", exe)
	if st, ok := s.Stat(pid); ok {
		fmt.Fprintf(&b, "root stat: comm=%s state=%s ppid=%d starttime=%d\n", st.Comm, st.State, st.PPID, st.StartTime)
	} else {
		fmt.Fprintf(&b, "root stat: pid %d is gone or unreadable\n", pid)
	}
	fmt.Fprintf(&b, "tree: %s\n", s.Sample(pid).Line())
	b.WriteString("--- host-perf.log (as read at the assertion) ---\n")
	b.WriteString(perfLog)
	return b.String()
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
		if b, err := os.ReadFile(paths.WorkspaceLogPath(ws, HostPerfLogName)); err == nil &&
			strings.Contains(string(b), want) {
			return
		}
	}
	t.Fatalf("host-perf.log never showed %q:\n%s", want, perfFile(t, ws))
}

// The quiet-quit test's stderr assertion names the blocked call, not the process that made
// it, so its failure report carries the evidence that does (lingerFailureEvidence). Both
// cases read the real /proc: the test's own process, and a pid no process holds, which must
// be named as gone rather than abort the report.
func TestLingerFailureEvidenceNamesTheProcessOrItsAbsence(t *testing.T) {
	const perfLog = "mark   shutdown.window_a.exit_file_seen\n"
	self := os.Getpid()
	got := lingerFailureEvidence(perfLog, "/usr/bin/dd", self)
	for _, want := range []string{
		`fixture cmd.Path="/usr/bin/dd"`,
		fmt.Sprintf("pid=%d", self),
		"exe=",
		"starttime=",
		fmt.Sprintf("pid %d ", self),
		"--- host-perf.log (as read at the assertion) ---\n" + perfLog,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("live evidence missing %q:\n%s", want, got)
		}
	}

	const missingPID = 1<<31 - 1 // beyond any pid_max: no /proc entry exists
	gone := lingerFailureEvidence(perfLog, "/usr/bin/dd", missingPID)
	for _, want := range []string{"gone or unreadable", "tree: no process readable", perfLog} {
		if !strings.Contains(gone, want) {
			t.Errorf("missing-process evidence missing %q:\n%s", want, gone)
		}
	}
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
	lines := []string{
		"yolo: shutdown.window_a took 1.690s",
		"yolo: podman stayed 1.6s after its container was removed, blocked in " + want,
	}
	if os.Getenv(quietQuitMismatchEnv) != "" {
		// Child-only: no run prints this line, so the failure branch below runs on this session.
		lines = append(lines, "yolo: forced quiet-quit mismatch (child only)")
	}
	for _, line := range lines {
		if !strings.Contains(got, line) {
			t.Errorf("stderr missing %q:\n%s\n%s", line, got, lingerFailureEvidence(file, client.Path, client.Process.Pid))
		}
	}
	settled := perfFile(t, ws)
	time.Sleep(150 * time.Millisecond)
	if after := perfFile(t, ws); after != settled {
		t.Errorf("the probe wrote after the report printed:\n%s", strings.TrimPrefix(after, settled))
	}
}

// quietQuitMismatchEnv marks the child run that TestQuietQuitFailureReportCarriesTheProcessEvidence
// starts: the quiet-quit test then expects one stderr line no run prints, so its failure branch
// runs on the real session and writes the report this parent test reads.
const quietQuitMismatchEnv = "YOLO_TEST_QUIETQUIT_FORCED_MISMATCH"

// THE CALL SITE of lingerFailureEvidence: the quiet-quit test's failure branch must print the
// process evidence and the perf log it read. The child is the same compiled test binary, run
// on that one test only; the forced line is the only difference from a passing run.
func TestQuietQuitFailureReportCarriesTheProcessEvidence(t *testing.T) {
	if os.Getenv(quietQuitMismatchEnv) != "" {
		t.Fatal("the forced-mismatch child must not start another child")
	}
	out, runErr := runForcedQuietQuitChild(t, os.Environ())
	skipReason, problem := forcedQuietQuitVerdict(out, runErr)
	if problem != "" {
		t.Fatalf("%s:\n%s", problem, out)
	}
	if skipReason != "" {
		t.Skipf("the quiet-quit test could not run here, so its failure branch was not exercised: %s", skipReason)
	}
	report := out
	for _, want := range []string{
		`stderr missing "yolo: forced quiet-quit mismatch (child only)"`,
		"yolo: shutdown.window_a took 1.690s",
		"fixture cmd.Path=",
		"root pid=",
		"root exe=",
		"tree: ",
		"--- host-perf.log (as read at the assertion) ---\n",
		"mark   shutdown.window_a.exit_file_seen",
		"end    shutdown.window_a.client_exit  dur=1.648s",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the failure report missing %q:\n%s", want, report)
		}
	}
}

// THE CALLER UNDER A MISSING FIXTURE: with no dd on PATH the quiet-quit test skips before its
// assertion, so TestQuietQuitFailureReportCarriesTheProcessEvidence must SKIP too, naming dd,
// instead of failing on the child's clean exit. Both cases run the parent test as a child of
// this binary: the first with PATH naming an empty directory, the second with the real PATH,
// where the forced failure must produce its report and the parent must PASS. If PTY setup
// fails before dd, or probe startup fails after it, the nested parent SKIPs with that
// prerequisite. The missing-dd wrapper verifies the skip and passes; the positive wrapper
// skips because the failure report was not exercised.
func TestQuietQuitDiagnosticSkipsWhenItsFixtureIsMissing(t *testing.T) {
	if os.Getenv(quietQuitMismatchEnv) != "" {
		t.Fatal("the forced-mismatch child must not start another child")
	}
	const parent = "TestQuietQuitFailureReportCarriesTheProcessEvidence"
	runParent := func(t *testing.T, env []string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), quietQuitChildTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+parent+"$", "-test.count=1", "-test.v=true")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("the parent run hung past the guard:\n%s", out)
		}
		return string(out), err
	}
	t.Run("a missing dd skips the parent", func(t *testing.T) {
		out, err := runParent(t, withEnvVar(os.Environ(), "PATH", t.TempDir()))
		status, messages := selectedTestOutcome(out, parent)
		reason := strings.Join(messages, "\n")
		switch {
		case err != nil || status != "SKIP":
			t.Fatalf("the parent run exited %v with outcome %q, want a clean SKIP:\n%s", err, status, out)
		case strings.Contains(reason, "dd:"):
			// the fixture reached dd and found none
		case quietQuitPrerequisiteSkip(reason):
			// the fixture stopped before dd, so there is no dd reason to name
		default:
			t.Errorf("parent skipped with %q, want the dd skip or a fixture prerequisite:\n%s", reason, out)
		}
	})
	t.Run("the real PATH passes the parent with its report", func(t *testing.T) {
		if _, err := exec.LookPath("dd"); err != nil {
			t.Skip("no dd on this machine: the positive run needs the fixture")
		}
		out, err := runParent(t, os.Environ())
		status, messages := selectedTestOutcome(out, parent)
		reason := strings.Join(messages, "\n")
		switch {
		case err == nil && status == "PASS":
		case err == nil && status == "SKIP" && quietQuitPrerequisiteSkip(reason):
			t.Skipf("the quiet-quit fixture cannot be set up here, so the report was not exercised: %s", reason)
		default:
			t.Fatalf("the parent run exited %v with outcome %q, want PASS or a prerequisite SKIP:\n%s", err, status, out)
		}
	})
}

// quietQuitPrerequisiteSkip reports whether a skip message names a fixture the quiet-quit test
// could not set up on this machine: no pty, a refused ioctl, no dd, or an unarmed probe.
func quietQuitPrerequisiteSkip(reason string) bool {
	for _, marker := range []string{"no pty:", "unlockpt:", "ptsname:", "open slave:", "dd:", "linger probe failed to start"} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

// quietQuitTestName is the quiet-quit test whose failure branch the forced child drives.
const quietQuitTestName = "TestLingeringClientIsSampledAndNamedAtAQuietQuit"

// quietQuitChildTimeout is a hang guard for a child test run, not a budget: the children's own
// waits are event-driven and bounded.
const quietQuitChildTimeout = 3 * time.Minute

// runForcedQuietQuitChild runs the quiet-quit test in a child of this test binary with the forced
// mismatch set, under env, and returns its verbose output and exit error. A child past the guard
// fails the caller.
func runForcedQuietQuitChild(t *testing.T, env []string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), quietQuitChildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+quietQuitTestName+"$", "-test.count=1", "-test.v=true")
	cmd.Env = append(env, quietQuitMismatchEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the forced-mismatch child hung past the guard:\n%s", out)
	}
	return string(out), err
}

// forcedQuietQuitVerdict reads the forced child's outcome from the selected test's own status line.
// A SKIP that exits clean is the test saying it could not run here (its fixture or probe was
// missing): it returns the skip messages and no problem. A FAIL that exits non-zero is the
// expected failing run and returns neither. Anything else is a problem: a PASS (the forced line
// never reached the failure branch), a FAIL with no exit error, a SKIP that exited non-zero, or
// no status at all (a crash or an early stop).
func forcedQuietQuitVerdict(out string, runErr error) (skipReason, problem string) {
	status, messages := selectedTestOutcome(out, quietQuitTestName)
	var exit *exec.ExitError
	switch {
	case status == "SKIP" && runErr == nil:
		return strings.Join(messages, "; "), ""
	case status == "SKIP":
		return "", "the forced-mismatch child skipped and then exited " + runErr.Error()
	case status == "FAIL" && errors.As(runErr, &exit):
		return "", ""
	case status == "FAIL":
		return "", "the forced-mismatch child failed without an exit error: " + runErr.Error()
	case status == "PASS":
		return "", "the forced-mismatch child passed: its forced line never reached the failure branch"
	default:
		return "", "the forced-mismatch child printed no outcome for the selected test; it crashed or stopped early"
	}
}

// selectedTestOutcome returns the final status (PASS, FAIL or SKIP) that -test.v printed for the
// top-level test name, and the indented messages it logged after its own "=== RUN" line. Only a
// column-0 status line counts, so an indented subtest line or another test's line never stands in.
func selectedTestOutcome(out, name string) (status string, messages []string) {
	lines := strings.Split(out, "\n")
	run, end := -1, -1
	for i, line := range lines {
		if line == "=== RUN   "+name {
			run = i
		}
		for _, s := range []string{"PASS", "FAIL", "SKIP"} {
			if strings.HasPrefix(line, "--- "+s+": "+name+" (") {
				status, end = s, i
			}
		}
	}
	if run < 0 || end < run {
		return "", nil
	}
	for _, line := range lines[run+1 : end] {
		if strings.HasPrefix(line, "    ") {
			messages = append(messages, strings.TrimSpace(line))
		}
	}
	return status, messages
}

// withEnvVar returns env with key set to value, replacing any earlier entry for key.
func withEnvVar(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}

// The forced child's verdict is read from the selected test's own column-0 status line and
// nothing else: only a clean SKIP of that test is a skip, and every other outcome is either the
// expected failing run or a problem the parent must fail on.
func TestForcedQuietQuitVerdictReadsOnlyTheSelectedTestsOutcome(t *testing.T) {
	const name = quietQuitTestName
	exited := exec.Command(os.Args[0], "-test.nosuchflag").Run() // this binary's own flag error: a real *exec.ExitError, as a failing child gives, with no tool looked up on PATH
	var exitErr *exec.ExitError
	if !errors.As(exited, &exitErr) {
		t.Fatalf("the test binary's flag error gave %v, want a *exec.ExitError", exited)
	}
	for _, tc := range []struct {
		desc    string
		out     string
		runErr  error
		skip    string
		problem bool
	}{
		{"the selected test's own skip is propagated",
			"=== RUN   " + name + "\n    lingerprobe_test.go:306: dd: not found\n--- SKIP: " + name + " (0.00s)\nPASS\n",
			nil, "lingerprobe_test.go:306: dd: not found", false},
		{"another test's skip is not this test's skip",
			"=== RUN   TestOther\n--- SKIP: TestOther (0.00s)\n=== RUN   " + name + "\n--- PASS: " + name + " (0.01s)\nPASS\n",
			nil, "", true},
		{"an indented subtest line is not the test's status",
			"=== RUN   " + name + "\n    --- SKIP: " + name + " (0.00s)\n",
			nil, "", true},
		{"a SKIP mentioned mid-line is not a status line",
			"=== RUN   " + name + "\nnote: --- SKIP: " + name + " (0.00s)\n",
			nil, "", true},
		{"a skip that exits non-zero is a problem",
			"=== RUN   " + name + "\n--- SKIP: " + name + " (0.00s)\n",
			exited, "", true},
		{"a failure that also logged a skip is the expected failing run, never a skip",
			"=== RUN   " + name + "\n    lingerprobe_test.go:9: dd: not found\n--- FAIL: " + name + " (0.00s)\nFAIL\n",
			exited, "", false},
		{"a pass is an unexpected success",
			"=== RUN   " + name + "\n--- PASS: " + name + " (0.00s)\nPASS\n",
			nil, "", true},
		{"output with no status for the test is a problem",
			"=== RUN   " + name + "\npanic: runtime error\n",
			exited, "", true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			skip, problem := forcedQuietQuitVerdict(tc.out, tc.runErr)
			if skip != tc.skip {
				t.Errorf("skip = %q, want %q", skip, tc.skip)
			}
			if (problem != "") != tc.problem {
				t.Errorf("problem = %q, want a problem: %v", problem, tc.problem)
			}
		})
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
	// EVERY WRITE WAITS FOR WHAT IT DEPENDS ON, never for a fixed time. Two sleeps (300ms,
	// then 100ms) stood in for both, and a machine where sh and stty took longer to exec lost
	// the first: input that reaches a still-cooked pty is held for a newline and its ^C is
	// eaten by ISIG, so dd never read its 7 bytes and the test gave up on "child never exited".
	//
	// 1. The child's pty is raw, read through the pty-mode reader the proxy handed the linger
	//    slot (Observer.Pty). From then on the pty passes every byte through to dd, whether or
	//    not dd has reached its read yet.
	waitForProxyState(t, "the child's pty to turn raw (Observer.Pty's reader)", func() bool {
		o.linger.mu.Lock()
		mode := o.linger.ptyMode
		o.linger.mu.Unlock()
		return mode != nil && mode() == "icanon=off isig=off echo=off"
	})
	_, _ = master.Write([]byte("secret"))
	// 2. The proxy has forwarded "secret" as a chunk of its own, so the ^C below is one too:
	//    a proxy slower to read than the old 100ms sleep got both in a single 7-byte chunk,
	//    which is neither note the assertions want. It also means the proxy is in its pump, so
	//    its own stdin is raw and the ^C is forwarded rather than eaten there.
	waitForProxyState(t, "the proxy to forward the first chunk", func() bool {
		return o.linger.inputNotes.Load() >= 1
	})
	_, _ = master.Write([]byte{0x03})
	select {
	case <-done:
	case <-time.After(forwardedInputBudget):
		t.Fatalf("child never exited within %v of its 7th byte", forwardedInputBudget)
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

// forwardedInputBudget bounds each wait in TestForwardedInputAfterTheDeathIsTimedNeverRecorded.
// Every one of them is normally met within milliseconds of a process's exec; ten seconds is
// generous for exec latency on a loaded laptop or CI runner, and a proxy or child that never
// gets there still fails, naming the step.
const forwardedInputBudget = 10 * time.Second

// waitForProxyState polls cond until it holds, failing with what it waited for once
// forwardedInputBudget runs out.
func waitForProxyState(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(forwardedInputBudget); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", forwardedInputBudget, what)
		}
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
// (docs/reference/podman-readiness.md PR-D5, PR-D10): noted when runtime selection
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
	if first("awaitRunning.probeRunningContainer") > first("awaitRunning.releaseLaunchLockAtRunning") ||
		first("awaitRunning.releaseLaunchLockAtRunning") > first("awaitRunning.startLingerProbe") {
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
