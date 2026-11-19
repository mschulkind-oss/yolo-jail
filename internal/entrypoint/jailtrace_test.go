package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailtrace"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The jail half of the launch's trace (docs/design/startup-tracing.md §5.3, §5.6; the plan's U5).

const testTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

type traceLine struct {
	T    int64  `json:"t"`
	K    string `json:"k"`
	Name string `json:"name"`
	PID  int    `json:"pid"`
	Res  string `json:"res"`
}

func parseTraceLines(t *testing.T, b []byte) []traceLine {
	t.Helper()
	var out []traceLine
	for _, s := range strings.Split(string(b), "\n") {
		if s == "" {
			continue
		}
		var l traceLine
		if err := json.Unmarshal([]byte(s), &l); err != nil {
			t.Fatalf("trace line %q is not JSON: %v", s, err)
		}
		out = append(out, l)
	}
	return out
}

func traceEvents(lines []traceLine) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.K+" "+l.Name)
	}
	return out
}

// lineSignal is a trace destination that hands each line to the test as it is written, so a
// test waits on the write itself.
type lineSignal chan string

func (c lineSignal) Write(b []byte) (int, error) { c <- string(b); return len(b), nil }

// guard is a failure guard only: it turns a hang into a red test, and never decides a pass.
const guard = 30 * time.Second

func nextLine(t *testing.T, c lineSignal) traceLine {
	t.Helper()
	select {
	case s := <-c:
		return parseTraceLines(t, []byte(s))[0]
	case <-time.After(guard):
		t.Fatal("no trace line was written")
		return traceLine{}
	}
}

// A HUNG BOOT LEAVES A LINE. The perf marks are dumped once at the end, so a step that never
// returns left nothing on disk; the trace's start line is written before the step runs. The step
// here blocks on a channel: the test receives its start line while it is blocked, then releases
// it and receives the end.
func TestABootStepThatHangsHasAlreadyWrittenItsStart(t *testing.T) {
	lines := make(lineSignal, 16)
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &strings.Builder{}}
	e.trace = jailtrace.NewWriter(lines, 7, time.Now)
	release := make(chan struct{})
	steps := []bootStep{
		{name: "excluded", run: func(*bootRun) {}, notContainer: "a reason given here"},
		{name: "hangs", run: func(*bootRun) { <-release }},
		{name: "quiet", run: func(*bootRun) {}, noMark: true},
	}
	done := make(chan struct{})
	go func() {
		runSteps(&bootRun{e: e, target: bootContainer, perf: newPerfLog()}, steps)
		close(done)
	}()
	if l := nextLine(t, lines); l.K != "start" || l.Name != "boot.hangs" || l.PID != 7 {
		t.Fatalf("first line while the step hangs = %+v, want start boot.hangs", l)
	}
	select {
	case s := <-lines:
		t.Fatalf("a line was written while the step was still running: %s", s)
	default:
	}
	close(release)
	if l := nextLine(t, lines); l.K != "end" || l.Name != "boot.hangs" {
		t.Fatalf("after release = %+v, want end boot.hangs", l)
	}
	// A step that marks no perf checkpoint is still a span of the trace.
	for _, want := range []string{"start boot.quiet", "end boot.quiet"} {
		if l := nextLine(t, lines); l.K+" "+l.Name != want {
			t.Fatalf("got %s %s, want %s", l.K, l.Name, want)
		}
	}
	<-done
}

// Main is where the boot's writer comes from: it opens one from the YOLO_TRACEPARENT the launch
// handed it, before the boot's waits and its table, and brackets the table in the pass's span.
// Fails if the call site is deleted or moved below the table.
func TestMainOpensTheTraceFromTheLaunchsContextBeforeTheTable(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	body = body[strings.Index(body, "func Main(args []string) error {"):]
	open := strings.Index(body, "e.trace = jailtrace.Open(e.Home, e.Getenv(jailtrace.Env))")
	await := strings.Index(body, "err := gate.awaitBoot()")
	start := strings.Index(body, "e.trace.Start(pass)")
	table := strings.Index(body, "runBootSteps(&bootRun{e: e, target: bootContainer")
	end := strings.Index(body, "e.trace.End(pass)")
	if open < 0 || await < open || start < await || table < start || end < table {
		t.Errorf("Main must open the trace, then wait, then bracket the table in the pass span "+
			"(open %d, await %d, start %d, table %d, end %d)", open, await, start, table, end)
	}
	if bootPassSpan(false) == bootPassSpan(true) {
		t.Error("the jail's own boot and a session's pass share a span name")
	}
}

// THE STARTUP WINDOW ENDS AT THE EXEC (TR-D10): execBash writes session.exec before it replaces
// the process, and the mark is already written when the exec happens.
func TestExecBashMarksSessionExecBeforeTheExec(t *testing.T) {
	saved := sysExec
	var buf bytes.Buffer
	var atExec []string
	sysExec = func(string, []string, []string) error {
		atExec = traceEvents(parseTraceLines(t, buf.Bytes()))
		return nil
	}
	t.Cleanup(func() { sysExec = saved })
	t.Setenv("PATH", os.Getenv("PATH"))
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	e.trace = jailtrace.NewWriter(&buf, 1, time.Now)
	if err := execBash(e, "true", false); err != nil {
		t.Skipf("execBash could not reach its exec here (%v); it needs bash on the boot PATH", err)
	}
	if len(atExec) != 1 || atExec[0] != "mark "+sessionExecMark {
		t.Errorf("trace at the exec = %v, want the one session.exec mark", atExec)
	}
}

// The provisioning stage is a span of the session's trace, whatever its status.
func TestTheProvisioningStageIsASpan(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH; the stage is bash")
	}
	var buf bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	e.trace = jailtrace.NewWriter(&buf, 1, time.Now)
	if rc := runProvisionStage(e, "exit 3"); rc != 3 {
		t.Fatalf("rc = %d, want the stage's 3", rc)
	}
	got := traceEvents(parseTraceLines(t, buf.Bytes()))
	if want := []string{"start provision.stage", "end provision.stage"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

// Each required in-jail daemon is a span from the supervisor's start to its readiness answer,
// written by the entrypoint; a failed answer also leaves a mark.
func TestRequiredDaemonsAreSpansToTheirReadinessAnswer(t *testing.T) {
	bin := writeSupervisor(t, `printf 'ready wire-bridge\n' >&3; printf 'failed other no-route\n' >&3`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	var buf bytes.Buffer
	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge,other",
	})
	e.trace = jailtrace.NewWriter(&buf, 1, time.Now)
	if err := startJailDaemonSupervisor(e); err == nil {
		t.Fatal("a failed readiness answer did not refuse")
	}
	got := strings.Join(traceEvents(parseTraceLines(t, buf.Bytes())), ",")
	want := "start daemon.wire-bridge,start daemon.other,end daemon.wire-bridge,end daemon.other,mark daemon.other.failed"
	if got != want {
		t.Errorf("trace = %s\nwant    %s", got, want)
	}
}

// runTraceShell runs traceShellFn under bash with HOME=home and the given environment, then the
// script, and returns the trace file's contents ("" when there is none).
func runTraceShell(t *testing.T, home string, env []string, script string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH; the generated shell is bash")
	}
	cmd := exec.Command(bash, "-c", traceShellFn+script)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, env...)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("the trace function changed the script's behavior: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(home, jailtrace.FileName("0af7651916cd43dd8448eb211c80319c")))
	return string(b)
}

// THE GENERATED SHELL'S WRITER: a row only when the trace file exists (the Go writer creates it),
// microseconds where EPOCHREALTIME is set and "res":"s" where it is not, the file the Go writer
// names, a sanitized name, and nothing for a missing or malformed context or through a link.
func TestTheShellTraceWriter(t *testing.T) {
	ctx := []string{jailtrace.Env + "=" + testTraceparent}

	t.Run("no file, no row", func(t *testing.T) {
		home := t.TempDir()
		if got := runTraceShell(t, home, ctx, "_yolo_trace start x\n"); got != "" {
			t.Fatalf("the shell created the trace file: %q", got)
		}
	})

	t.Run("microseconds and seconds", func(t *testing.T) {
		home := t.TempDir()
		w := jailtrace.Open(home, testTraceparent) // the Go writer creates the file
		if w == nil {
			t.Fatal("no Go writer")
		}
		w.Close()
		got := parseTraceLines(t, []byte(runTraceShell(t, home, ctx,
			"_yolo_trace start 'program.readiness.a\"b c/<x>'\n"+
				"unset EPOCHREALTIME\n_yolo_trace end y\n_yolo_trace bogus z\n")))
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2: %+v", len(got), got)
		}
		fine, coarse := got[0], got[1]
		if fine.K != "start" || fine.Name != "program.readiness.a_b_c__x_" {
			t.Errorf("first row = %+v", fine)
		}
		// Microseconds where the shell has EPOCHREALTIME (bash 5): no res, and nanoseconds
		// padded from microseconds.
		if v, _ := exec.Command("bash", "-c", "echo ${BASH_VERSINFO[0]}").Output(); strings.TrimSpace(string(v)) >= "5" {
			if fine.Res != "" || fine.T%1000 != 0 {
				t.Errorf("a row with EPOCHREALTIME = %+v, want microseconds and no res", fine)
			}
		}
		now := time.Now().UnixNano()
		for _, r := range got {
			if r.T <= 0 || r.T > now || now-r.T > int64(time.Hour) {
				t.Errorf("row %+v has no plausible time (now %d)", r, now)
			}
		}
		if coarse.K != "end" || coarse.Res != "s" || coarse.T%1_000_000_000 != 0 {
			t.Errorf("a row with no EPOCHREALTIME = %+v, want whole seconds and res s", coarse)
		}
	})

	t.Run("no context or a link", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, jailtrace.FileName("0af7651916cd43dd8448eb211c80319c"))
		writeTestFile(t, path, "")
		runTraceShell(t, home, nil, "_yolo_trace start x\n")
		runTraceShell(t, home, []string{jailtrace.Env + "=00-0AF7651916CD43DD8448EB211C80319C-b7ad6b7169203331-01"}, "_yolo_trace start x\n")
		if b, _ := os.ReadFile(path); len(b) != 0 {
			t.Fatalf("a row was written with no valid context: %q", b)
		}
		target := filepath.Join(home, "target")
		writeTestFile(t, target, "")
		_ = os.Remove(path)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		runTraceShell(t, home, ctx, "_yolo_trace start x\n")
		if b, _ := os.ReadFile(target); len(b) != 0 {
			t.Fatalf("the shell wrote through a link: %q", b)
		}
	})
}

// THE BOOTSTRAP AND EACH PROGRAM'S READINESS ARE SPANS, through the generated bootstrap itself
// (the real template, run under bash): provision.bootstrap brackets the script, the readiness
// act's start and end sit inside it, and a script with no trace file writes nothing.
func TestTheBootstrapWritesItsSpansAndEachProgramsReadiness(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	path := filepath.Join(h.e.Home, jailtrace.FileName("0af7651916cd43dd8448eb211c80319c"))
	if rc, out, _ := h.run(t, jailtrace.Env+"="+testTraceparent); rc != 0 {
		t.Fatalf("rc = %d:\n%s", rc, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the bootstrap created the trace file (%v); only the Go writer may", err)
	}
	writeTestFile(t, path, "")
	if rc, out, _ := h.run(t, jailtrace.Env+"="+testTraceparent); rc != 0 {
		t.Fatalf("rc = %d:\n%s", rc, out)
	}
	b, _ := os.ReadFile(path)
	got := traceEvents(parseTraceLines(t, b))
	want := []string{
		"start provision.bootstrap",
		"start program.readiness." + readyBin,
		"end program.readiness." + readyBin,
		"end provision.bootstrap",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trace = %v\nwant    %v", got, want)
	}
}
