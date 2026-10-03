package run

// podmanready_test.go pins the podman readiness gate (podmanready.go,
// docs/design/podman-reboot-readiness.md) THROUGH runtime selection, never at an isolated
// helper: every case runs once down each of the three ways a launch reaches it — YOLO_RUNTIME,
// the config's `runtime` key, and autodetection — so deleting the gate from either
// validateExplicitRuntime or resolveRuntime's candidate loop fails every test here. The fake
// attempt runner is on a fake clock, so a minute's wait costs nothing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// gatePath is one way a launch reaches runtime selection.
type gatePath struct {
	name   string
	getenv func(string) string
	cfg    *jsonx.OrderedMap
}

func gatePaths() []gatePath {
	return []gatePath{
		{name: "YOLO_RUNTIME", getenv: func(k string) string {
			if k == "YOLO_RUNTIME" {
				return "podman"
			}
			return ""
		}},
		{name: "config runtime", getenv: func(string) string { return "" }, cfg: newConfig("runtime", "podman")},
		{name: "autodetected", getenv: func(string) string { return "" }},
	}
}

// gateOptions is a Linux host with podman on PATH, reaching the gate down path p.
func gateOptions(p gatePath, stdout, stderr *bytes.Buffer) *Options {
	o := &Options{IsLinux: true}
	fillDefaults(o)
	o.Getenv = p.getenv
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	o.Stdout, o.Stderr = stdout, stderr
	return o
}

func busyAttempt(line string) yoloruntime.Attempt {
	return yoloruntime.Attempt{Exited: true, RC: 125, Stderr: line + "\n"}
}

// A podman that is busy, then answers — including on the very last attempt the budget
// allows — is waited for and accepted, and every early exit is printed as it happens.
func TestTheGateRecoversAPodmanThatAnswersLate(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			// 0, 1, 3, 7, …, 55 s busy; the attempt at 59 s answers. It is the last one
			// whose backoff fits in the 60 s budget, so success there is success at the edge.
			script := make([]yoloruntime.Attempt, 0, 16)
			for i := 0; i < 16; i++ {
				script = append(script, busyAttempt("Error: database is locked"))
			}
			script = append(script, yoloruntime.Attempt{Exited: true, RC: 0, Stdout: podmanInfoFixture})
			gate := scriptedPodman(o, script...)

			rt, ok := o.resolveRuntime(p.cfg)
			if !ok || rt != "podman" {
				t.Fatalf("resolveRuntime = %q,%v; want podman after a late answer\nstdout:\n%s\nstderr:\n%s",
					rt, ok, stdout.String(), stderr.String())
			}
			if gate.count() != 17 {
				t.Errorf("the gate ran %d attempts, want 17", gate.count())
			}
			if got := strings.Count(stderr.String(), "podman info: exit 125: Error: database is locked; retrying in"); got != 16 {
				t.Errorf("printed %d retry lines, want one per early exit (16):\n%s", got, stderr.String())
			}
			if o.podmanFacts == nil || !o.podmanFacts.parsed {
				t.Error("the late answer did not become the launch's Podman facts")
			}
			// The argv is the gate's: `podman info --format json`, the JSON the facts need.
			if argv := strings.Join(gate.argvs[0], " "); argv != "podman info --format json" {
				t.Errorf("the gate ran %q", argv)
			}
		})
	}
}

// An attempt still running at the end of the budget: refused, the pid named, and left
// running — the fake runner could not be told to kill it, and the gate never tries.
func TestAPodmanStillRunningAtTheBudgetIsNamedAndLeftRunning(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			gate := scriptedPodman(o, yoloruntime.Attempt{Pid: 4242})
			if rt, ok := o.resolveRuntime(p.cfg); ok || rt != "" {
				t.Fatalf("resolveRuntime = %q,%v; want a refusal", rt, ok)
			}
			got := stdout.String()
			for _, want := range []string{
				"Cannot query", "podman info did not answer within 60s (1 attempt, 60.0s)",
				"podman (pid 4242) is still running; yolo left it to finish.",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
			if gate.count() != 1 {
				t.Errorf("the gate ran %d attempts behind a still-running one, want 1", gate.count())
			}
		})
	}
}

// Every early exit is printed, and the refusal carries the last one's stderr line.
func TestEveryEarlyExitIsPrintedAndTheRefusalCarriesTheLast(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			var script []yoloruntime.Attempt
			for i := 1; i <= 20; i++ {
				script = append(script, busyAttempt("Error: storage busy, attempt "+string(rune('a'+i))))
			}
			gate := scriptedPodman(o, script...)
			if _, ok := o.resolveRuntime(p.cfg); ok {
				t.Fatal("an always-failing podman was accepted")
			}
			n := gate.count()
			for i := 1; i < n; i++ {
				line := "podman info: exit 125: Error: storage busy, attempt " + string(rune('a'+i))
				if !strings.Contains(stderr.String(), line) {
					t.Errorf("early exit %d was not printed (%q):\n%s", i, line, stderr.String())
				}
			}
			last := "the last attempt: exit 125: Error: storage busy, attempt " + string(rune('a'+n))
			if !strings.Contains(stdout.String(), last) {
				t.Errorf("the refusal does not carry the last stderr line %q:\n%s", last, stdout.String())
			}
			if !strings.Contains(stdout.String(), "attempts,") {
				t.Errorf("the refusal does not count the attempts:\n%s", stdout.String())
			}
		})
	}
}

// An answer that cannot clear on its own refuses at once and names the fix (OQ-PR1).
func TestAnAnswerThatCannotClearRefusesAtOnceNamingTheFix(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			gate := scriptedPodman(o, busyAttempt("cannot clone: Operation not permitted\nError: cannot re-exec process"))
			if _, ok := o.resolveRuntime(p.cfg); ok {
				t.Fatal("a podman that cannot create its user namespace was accepted")
			}
			if gate.count() != 1 {
				t.Errorf("the gate retried a permanent error: %d attempts", gate.count())
			}
			if strings.Contains(stderr.String(), "retrying") {
				t.Errorf("a permanent error printed a retry:\n%s", stderr.String())
			}
			got := stdout.String()
			if !strings.Contains(got, "does not clear on its own") || !strings.Contains(got, "cannot clone: Operation not permitted") ||
				!strings.Contains(got, "Fix: podman cannot create its user namespace") {
				t.Errorf("refusal = %q", got)
			}
		})
	}
}

// A podman that cannot be started at all fails at once, naming the fix; one whose start can
// clear on its own (its binary busy being replaced) is retried and accepted (PR-D23).
func TestAPodmanThatCannotStartFailsAtOnce(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			gate := scriptedPodman(o, yoloruntime.Attempt{StartErr: &os.PathError{Op: "fork/exec",
				Path: "/usr/bin/podman", Err: syscall.EACCES}})
			if _, ok := o.resolveRuntime(p.cfg); ok {
				t.Fatal("an unstartable podman was accepted")
			}
			if gate.count() != 1 || !strings.Contains(stdout.String(), "podman info could not run") ||
				!strings.Contains(stdout.String(), "Fix: a permission error") {
				t.Errorf("attempts=%d refusal=%q", gate.count(), stdout.String())
			}

			stdout.Reset()
			stderr.Reset()
			o = gateOptions(p, &stdout, &stderr)
			gate = scriptedPodman(o, yoloruntime.Attempt{StartErr: &os.PathError{Op: "fork/exec",
				Path: "/usr/bin/podman", Err: syscall.ETXTBSY}}, yoloruntime.Attempt{Exited: true, RC: 0, Stdout: podmanInfoFixture})
			if rt, ok := o.resolveRuntime(p.cfg); !ok || rt != "podman" || gate.count() != 2 {
				t.Errorf("a busy binary: resolveRuntime = %q,%v after %d attempts\nstdout:\n%s", rt, ok,
					gate.count(), stdout.String())
			}
			if !strings.Contains(stderr.String(), "podman info: could not run: fork/exec /usr/bin/podman: text file busy; retrying in 1s") {
				t.Errorf("the retry was not printed:\n%s", stderr.String())
			}
		})
	}
}

// Apple Container is out of the gate's scope (PR-D7): one probe, no wait, and never an
// attempt through the gate. Podman on macOS takes the gate's patient one-shot instead (PR-D24):
// one attempt through the gate's runner, whose deadline is the whole budget, and never Exec's
// killed `podman info` (podmanmachine_test.go pins what it reports).
func TestMacOSKeepsTheOneShotProbe(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run("apple container", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := &Options{IsMacOS: true}
			fillDefaults(o)
			o.Stdout, o.Stderr = &stdout, &stderr
			o.Getenv = func(k string) string {
				if explicit && k == "YOLO_RUNTIME" {
					return "container"
				}
				return ""
			}
			o.LookPath = func(name string) (string, bool) { return "/opt/bin/" + name, name == "container" }
			probes := 0
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if strings.Join(argv, " ") == "container system status" {
					probes++
					return ExecResult{Ran: true, RC: 1}
				}
				if len(argv) == 2 && argv[1] == "--version" {
					return ExecResult{Ran: true, RC: 0, Stdout: "container CLI version 1.0"}
				}
				return ExecResult{Ran: false}
			}
			gate := scriptedPodman(o, yoloruntime.Attempt{Exited: true, RC: 0, Stdout: "{}"})
			if _, ok := o.resolveRuntime(nil); ok {
				t.Fatal("a stopped runtime was accepted")
			}
			if probes != 1 || gate.count() != 0 {
				t.Errorf("one-shot probes=%d, gate attempts=%d; want 1 and 0", probes, gate.count())
			}
		})
		t.Run("podman machine", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := &Options{IsMacOS: true}
			fillDefaults(o)
			o.Stdout, o.Stderr = &stdout, &stderr
			o.Getenv = func(k string) string {
				if explicit && k == "YOLO_RUNTIME" {
					return "podman"
				}
				return ""
			}
			o.LookPath = func(name string) (string, bool) { return "/opt/bin/" + name, name == "podman" }
			execProbes := 0
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if len(argv) >= 2 && argv[0] == "podman" && argv[1] == "info" {
					execProbes++
				}
				return ExecResult{Ran: false}
			}
			now := time.Unix(1_000_000, 0)
			var deadlines []time.Duration
			var argvs []string
			o.PodmanReadiness = yoloruntime.ReadySeams{
				Attempt: func(argv []string, deadline time.Time, _ <-chan struct{}) yoloruntime.Attempt {
					deadlines = append(deadlines, deadline.Sub(now))
					argvs = append(argvs, strings.Join(argv, " "))
					return yoloruntime.Attempt{Exited: true, RC: 125, Stderr: stoppedMachineStderr}
				},
				Now:       func() time.Time { return now },
				Sleep:     func(time.Duration, <-chan struct{}) bool { return true },
				Interrupt: make(chan struct{}),
			}
			if _, ok := o.resolveRuntime(nil); ok {
				t.Fatal("a stopped machine was accepted")
			}
			if execProbes != 0 {
				t.Errorf("ran `podman info` through Exec %d times, whose timeout kills it", execProbes)
			}
			if len(deadlines) != 1 || deadlines[0] != yoloruntime.PodmanReadyBudget {
				t.Errorf("attempt deadlines = %v; want one, the whole %s budget", deadlines, yoloruntime.PodmanReadyBudget)
			}
			if len(argvs) != 1 || argvs[0] != "podman info --format json" {
				t.Errorf("asked %q", argvs)
			}
		})
	}
}

// The perf events are written on the FAILURE path too, before the refusal returns (PR-D10):
// today's probes.done mark comes after runtime selection, so a refused launch recorded
// nothing about its probe.
func TestTheGateRecordsItsSpanAndAttemptsOnARefusal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := quietRecordingOptions(t, t.TempDir(), t.TempDir())
	o.Stdout, o.Stderr = &stdout, &stderr
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	gate := scriptedPodman(o, busyAttempt("Error: database is locked"), busyAttempt("Error: database is locked"),
		busyAttempt("cannot clone: Operation not permitted"))
	if _, ok := o.resolveRuntime(nil); ok {
		t.Fatal("accepted")
	}
	ev, ok := o.Perf.LastEvent("runtime.ready")
	if !ok || ev.Kind != perf.KindEnd {
		t.Fatalf("no ended runtime.ready span on the refusal path: %+v %v", ev, ok)
	}
	last, ok := o.Perf.LastEvent("runtime.ready.attempt")
	if !ok || !strings.Contains(last.Detail, "n=3") || !strings.Contains(last.Detail, "outcome=exit=125") ||
		!strings.Contains(last.Detail, "stderr=cannot clone") {
		t.Errorf("last attempt note = %+v", last)
	}
	if gate.count() != 3 {
		t.Errorf("attempts = %d", gate.count())
	}
	if _, ok := o.Perf.LastEvent("podman.facts"); ok {
		t.Error("a refused gate noted podman facts it never got")
	}
}

// A Ctrl-C during the wait: Run exits 130 and names the podman it left running.
func TestAnInterruptedWaitExitsWith130(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
	scriptedPodman(o, yoloruntime.Attempt{Pid: 777, Interrupted: true})
	if rc := Run(*o); rc != 130 {
		t.Fatalf("Run = %d, want 130\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Interrupted while waiting for podman info to answer") ||
		!strings.Contains(stdout.String(), "podman (pid 777) is still running; yolo left it to finish.") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

// THE REAL Ctrl-C (readyInterrupt). Every other gate test hands the gate an interrupt channel
// of its own; this one leaves Options.PodmanReadiness.Interrupt nil, so the launch installs
// its own SIGINT catch, and the attempt sends this process a real SIGINT while it runs. The
// catch must turn it into the gate's interrupt: rc 130, the podman it left running named, and
// the launch recorded as interrupted. Without the catch, the signal's default action ends the
// test binary, which fails the run.
func TestARealCtrlCDuringTheWaitStopsTheWaitAndExitsWith130(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.PodmanReadiness = yoloruntime.ReadySeams{
		Attempt: func(_ []string, _ time.Time, interrupt <-chan struct{}) yoloruntime.Attempt {
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				t.Errorf("could not send SIGINT: %v", err)
			}
			select {
			case <-interrupt:
				return yoloruntime.Attempt{Pid: 4321, Interrupted: true}
			case <-time.After(10 * time.Second):
				return yoloruntime.Attempt{Exited: true, RC: 0, Stdout: minimalPodmanInfo, Pid: 4321}
			}
		},
	}
	if rc := Run(*o); rc != 130 {
		t.Fatalf("Run = %d, want 130: the SIGINT did not stop the wait\nstdout:\n%s", rc, stdout.String())
	}
	if !strings.Contains(stdout.String(), "podman (pid 4321) is still running; yolo left it to finish.") {
		t.Errorf("the refusal does not name the podman it left running:\n%s", stdout.String())
	}
	assertOneLaunchLine(t, ws, "tries=1", "outcome=interrupted", "rc=130")
}

// APPLE CONTAINER'S ATTACH PROBE (probeRunningContainer's `container ls` arm, PR-D18): the
// same tri-state as podman's, asked the way that CLI asks it. A listing that names the jail
// attaches, one that does not is "not running", and a `container ls` that fails, times out or
// cannot run is "could not ask", which the attach decision refuses on.
func TestAppleContainersAttachProbeKeepsTheTriState(t *testing.T) {
	cname := "yolo-ws-1234abcd"
	listing := "ID                 IMAGE   OS     ARCH   STATE    ADDR\n" +
		cname + "   yolo    linux  arm64  running  192.168.64.3\n"
	for _, tc := range []struct {
		name      string
		res       ExecResult
		wantID    string
		wantKnown bool
	}{
		{"running", ExecResult{Ran: true, RC: 0, Stdout: listing}, cname, true},
		{"not running", ExecResult{Ran: true, RC: 0, Stdout: "ID  IMAGE  OS  ARCH  STATE  ADDR\n"}, "", true},
		{"failed", ExecResult{Ran: true, RC: 1, Stderr: "Error: XPC connection error"}, "", false},
		{"timed out", ExecResult{Ran: true, Timeout: true}, "", false},
		{"could not run", ExecResult{Ran: false}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{}
			fillDefaults(o)
			var asked []string
			o.Exec = func(argv []string, _ string, _ []string, timeout time.Duration) ExecResult {
				asked = append(asked, strings.Join(argv, " "))
				if timeout != attachProbeTimeout {
					t.Errorf("asked with timeout %s, want %s", timeout, attachProbeTimeout)
				}
				return tc.res
			}
			id, known := o.probeRunningContainer(cname, "container", attachProbeTimeout)
			if id != tc.wantID || known != tc.wantKnown {
				t.Errorf("probeRunningContainer = %q,%v; want %q,%v", id, known, tc.wantID, tc.wantKnown)
			}
			if len(asked) != 1 || asked[0] != "container ls" {
				t.Errorf("asked %q, want exactly `container ls`: Apple's CLI has no `ps --filter`", asked)
			}
		})
	}
	if got := runningListCommand("container"); got != "container ls" {
		t.Errorf("the refusal tells an Apple Container user to run %q", got)
	}
}

// THE REFUSAL'S NEXT STEP, at its call site: the attach decision that could not ask names the
// command that lists the runtime's running containers, `container ls` on Apple Container,
// through runContainer itself. runningListCommand's own pin above cannot see a call site that
// goes back to printing "`<rt> ps`", which podman's case alone cannot tell apart.
func TestTheAttachRefusalNamesEachRuntimesListCommand(t *testing.T) {
	for _, tc := range []struct{ rt, want string }{
		{"podman", "Run `podman ps` to diagnose, then launch again."},
		{"container", "Run `container ls` to diagnose, then launch again."},
	} {
		t.Run(tc.rt, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			cname := yoloruntime.FromWorkspace(ws)
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, tc.rt, &stdout, &stderr, nil)
			var asked []string
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				asked = append(asked, strings.Join(argv, " "))
				return ExecResult{Ran: true, RC: 1, Stderr: "Error: the runtime is not answering"}
			}
			t.Cleanup(o.releaseLaunchLock)
			if rc := o.runContainer(nil, tc.rt, t.TempDir(), cname, stagedPacks{}, nil, nil, nil); rc != 1 {
				t.Fatalf("runContainer = %d, want the attach decision's refusal\nstdout:\n%s", rc, stdout.String())
			}
			if !strings.Contains(stdout.String(), "could not ask "+tc.rt+" whether this workspace's jail") ||
				!strings.Contains(stdout.String(), tc.want) {
				t.Errorf("the refusal does not name %q:\n%s", tc.want, stdout.String())
			}
			for _, a := range asked {
				if strings.HasPrefix(a, tc.rt+" run") {
					t.Errorf("a launch that could not ask whether its jail runs started one: %q", a)
				}
			}
		})
	}
}

// THE ATTACH DECISION IS TRI-STATE (PR-D8): a `ps` that could not answer refuses the launch
// rather than starting a fresh jail beside one that may be running.
func TestAnAttachDecisionThatCannotAskRefuses(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	started := false
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		joined := strings.Join(argv, " ")
		switch {
		case len(argv) >= 3 && argv[1] == "ps" && argv[2] == "-q" && strings.Contains(joined, "name=^/"+cname+"$"):
			return ExecResult{Ran: true, RC: 125, Stderr: "Error: database is locked"}
		case len(argv) >= 2 && argv[1] == "run":
			started = true
		}
		return ExecResult{Ran: true, RC: 0}
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "could not ask podman whether this workspace's jail") ||
		!strings.Contains(stdout.String(), "Run `podman ps` to diagnose") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if started {
		t.Error("a launch that could not ask whether its jail runs started a container")
	}
}

// ONE `podman info` PER LAUNCH (PR-D5), across a whole fake fresh launch through Run: the
// gate asks, and the host-loopback decision and the image copy's store facts read its answer.
// It fails if either brings back a query of its own. And the answer the gate waited for is
// the one that decides the argv: a rootless pasta host that recovered inside the gate gets
// the forwarding option and YOLO_HOST_LOOPBACK=requested — the wiring only; whether a real
// rootless host forwards is for CI or a real host to say (AGENTS.md, the two carve-outs).
func TestALaunchAsksPodmanOnceAndEveryReaderTakesTheGatesAnswer(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	bin, rec := t.TempDir(), t.TempDir()
	argvFile := filepath.Join(rec, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvFile + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	// Busy twice, then the answer: a rootless podman on pasta, with its store.
	info := strings.Replace(podmanInfoFixture, `"store":`, `"store": {"graphDriverName":"overlay",`+
		`"graphRoot":"/u/.local/share/containers/storage","runRoot":"/run/user/1000/containers"}, "x":`, 1)
	gate := scriptedPodman(o, busyAttempt("Error: database is locked"), busyAttempt("Error: database is locked"),
		yoloruntime.Attempt{Exited: true, RC: 0, Stdout: info})
	execInfo := 0
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		joined := strings.Join(argv, " ")
		switch {
		case len(argv) >= 2 && argv[1] == "info":
			execInfo++
			return ExecResult{Ran: true, RC: 0, Stdout: info}
		case strings.HasSuffix(joined, "pasta --help"):
			return ExecResult{Ran: true, RC: 0, Stdout: pastaHelpWithFlag}
		case len(argv) >= 2 && argv[1] == "ps":
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	var storeFacts image.PodmanStoreFacts
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		// The copy arm's read, as the real image load makes it.
		storeFacts = opts.StoreFacts()
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	rc := Run(*o)
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the launch never ran its container: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if gate.count() != 3 || execInfo != 0 {
		t.Errorf("podman info ran %d times at the gate and %d times after it; a launch asks once, "+
			"and only at the gate", gate.count(), execInfo)
	}
	if storeFacts.Rootless != image.RootlessYes || storeFacts.Store.GraphRoot != "/u/.local/share/containers/storage" {
		t.Errorf("the image copy's store facts did not come from the gate's answer: %+v", storeFacts)
	}
	lines := strings.Split(string(argv), "\n")
	if !containsLine(lines, pastaArg) || !containsLine(lines, "YOLO_HOST_LOOPBACK=requested") {
		t.Errorf("the container argv lacks the forwarding the gate's answer decides (%s, "+
			"YOLO_HOST_LOOPBACK=requested):\n%s", pastaArg, argv)
	}
	// And the machine-wide launch line (launchrecord.go, OQ-PR3) records the wait: two
	// backoffs, 1 s and 2 s on the gate's clock, and three tries, written when the
	// container started.
	assertOneLaunchLine(t, ws, "runtime=podman podman_wait=3.0s tries=3 outcome=started rc=-")
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// A gate that ends on yolo's own scratch file never ran podman: the launch's refusal names that
// error and the temporary directory's fix, and no step that points at podman follows it.
func TestARefusalOnYolosScratchFileNamesNoPodmanStep(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := gateOptions(p, &stdout, &stderr)
			scriptedPodman(o, yoloruntime.Attempt{StartErr: &yoloruntime.ProbeScratchError{
				Err: &os.PathError{Op: "open", Path: "/tmp/yolo-podman-ready-1.out", Err: syscall.ENOSPC}}})
			if _, ok := o.resolveRuntime(p.cfg); ok {
				t.Fatal("a launch whose gate never ran podman was accepted")
			}
			got := stdout.String()
			for _, want := range []string{"so podman info never ran", "no space left on device",
				"Fix: free space in the temporary directory"} {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "podman info` to diagnose") || strings.Contains(got, "did not answer") {
				t.Errorf("the refusal of a launch that never ran podman points at podman:\n%s", got)
			}
		})
	}
}
