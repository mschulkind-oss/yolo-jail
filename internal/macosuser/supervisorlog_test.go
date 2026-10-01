package macosuser

// supervisorlog_test.go pins JD-8 (macos-user-nix-and-features.md): the guest's supervisor
// sends its own stdout and stderr to supervisor.log beside the daemons' logs, and the launch
// prints "Started …" only once the supervisor's readiness line is in that log — a supervisor
// that exits first refuses the launch naming the log and its last lines, and one still silent
// past the bound is said to be unconfirmed.
//
// The argv tests read the plan the orchestrator runs and the argv it hands StartBackground; the
// outcome tests drive RunMacosUser, so deleting startJailDaemons' wait, its disclosure or the
// wrapper in JailDaemonArgv fails a test here. What only a Mac can confirm is listed at the top
// of jaildaemon_test.go.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// readyLine is what a started supervisor writes into its log (supervisor.Main).
var readyLine = supervisor.StartedLinePrefix + "openai-auth-broker (pid 4242)\n"

// fakeSupervisor wires d as a supervisor start for workspace ws: its log holds logBefore until
// StartBackground is called and logBefore+logAfter from then on, the process has already exited
// when exited is set, and launcherOut is what it wrote on its own stdout and stderr. Any other
// file reads as absent. The log is matched by its exact path, so a launch reading any other file
// sees no readiness line.
func fakeSupervisor(d *Deps, rec *[]string, ws, logBefore, logAfter, launcherOut string, exited bool) {
	started := false
	log := SupervisorLogPath(ws)
	d.ReadFile = func(p string) (string, bool) {
		if p != log {
			return "", true
		}
		if started {
			return logBefore + logAfter, true
		}
		return logBefore, true
	}
	d.StartBackground = func(argv []string) (Background, error) {
		*rec = append(*rec, "start:"+strings.Join(argv, " "))
		started = true
		ch := make(chan struct{})
		if exited {
			close(ch)
		}
		return Background{
			Stop:   func() { *rec = append(*rec, "stop") },
			Exited: ch,
			Output: func() string { return launcherOut },
		}, nil
	}
}

func runGuestLaunch(t *testing.T, d Deps) (int, string) {
	t.Helper()
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	o.JailDaemons = openAIAdapterDaemons("")
	return RunMacosUser(d, o), buf.String()
}

// THE SUPERVISOR'S OWN STDOUT AND STDERR GO TO <ws>/.yolo/home/local/state/yolo-jail-daemons/
// supervisor.log, through the wrapper, INSIDE the profile and in front of the env-file reader —
// in the plan, and in the argv the launch actually starts.
func TestTheSupervisorArgvSendsItsOutputToTheWorkspaceLog(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	want := filepath.Join(ws, ".yolo", "home", "local", "state", "yolo-jail-daemons", "supervisor.log")
	plan := guestPlan(t, openAIAdapterDaemons("/src"))
	if plan.SupervisorLog != want {
		t.Fatalf("SupervisorLog = %q, want %q", plan.SupervisorLog, want)
	}
	argv := strings.Join(plan.JailDaemonArgv, "\x00")
	wrap := strings.Join([]string{sandboxEnvShell, "-c", supervisorLogWrapper, supervisorLogWrapperName, want}, "\x00")
	confined := strings.Index(argv, "/usr/bin/sandbox-exec\x00-f\x00"+plan.ProfilePath)
	logged := strings.Index(argv, wrap)
	reader := strings.Index(argv, sandboxEnvReader)
	jaild := strings.Index(argv, GuestBinaryPath(JaildName, "")+"\x00supervise")
	if confined < 0 || logged < 0 || reader < 0 || jaild < 0 || !(confined < logged && logged < reader && reader < jaild) {
		t.Fatalf("want sandbox-exec < log wrapper < env reader < supervise; got %d %d %d %d:\n%s",
			confined, logged, reader, jaild, strings.Join(plan.JailDaemonArgv, " "))
	}

	var rec []string
	d := mockDeps(&rec)
	fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
	if rc, out := runGuestLaunch(t, d); rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out)
	}
	for _, r := range rec {
		if strings.HasPrefix(r, "start:") && !strings.Contains(r, supervisorLogWrapperName+" "+want+" ") {
			t.Errorf("the started argv does not send the supervisor's output to %s:\n%s", want, r)
		}
	}
}

// THE WRAPPER REALLY DOES IT: run the plan's own argv from the wrapper on, with the env file and
// the supervisor swapped for stand-ins, and both streams land in the log, appended, in a
// directory the wrapper made. /bin/sh and /bin/mkdir are the same absolute paths on Linux and
// macOS, so this runs the wrapper's exact bytes.
func TestTheLogWrapperAppendsStdoutAndStderrToTheLog(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	ws := t.TempDir()
	plan := BuildRunPlanWithDaemons(ws, jsonx.NewOrderedMap(), []string{"codex"}, []string{"codex"},
		"/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), mockDarwin(), nil,
		openAIAdapterDaemons("/src"), FloorStage{})
	start := -1
	for i, a := range plan.JailDaemonArgv {
		if a == supervisorLogWrapper {
			start = i - 2
		}
	}
	if start < 0 {
		t.Fatalf("no log wrapper in %v", plan.JailDaemonArgv)
	}
	envFile := filepath.Join(t.TempDir(), "daemons.env")
	if err := os.WriteFile(envFile, []byte("export FROM_ENV_FILE=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var argv []string
	for _, a := range plan.JailDaemonArgv[start:] {
		switch a {
		case plan.DaemonEnvFile:
			a = envFile
		case GuestBinaryPath(JaildName, ""):
			argv = append(argv, "/bin/sh", "-c", `echo "out $FROM_ENV_FILE"; echo err >&2`)
			continue
		case "supervise":
			continue
		}
		argv = append(argv, a)
	}
	for i := 0; i < 2; i++ {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("run %d: err=%v, output reached the launcher instead of the log: %q", i, err, out)
		}
	}
	got, err := os.ReadFile(plan.SupervisorLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "out yes\nerr\nout yes\nerr\n" {
		t.Errorf("log = %q, want both streams from both runs, appended", got)
	}
}

// A STARTED SUPERVISOR IS SAID TO BE STARTED, only once its readiness line is in the log, and
// the disclosure names the host-side log directory.
func TestAStartedSupervisorIsDisclosedAsStarted(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, &rec, ws, "an earlier session's line\n", readyLine, "", false)
	rc, out := runGuestLaunch(t, d)
	if rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out)
	}
	if !strings.Contains(out, "Started openai-auth-broker inside the sandbox") ||
		!strings.Contains(out, "Logs: "+filepath.Dir(SupervisorLogPath(ws))) {
		t.Errorf("the launch does not disclose the started supervisor and its logs:\n%s", out)
	}
}

// AN EARLIER SESSION'S READINESS LINE IS NOT THIS START'S. The wait reads only what the log
// gained after the start, so a supervisor that never speaks is not reported started on the
// strength of yesterday's line.
func TestAnOldReadinessLineDoesNotCountAsThisStart(t *testing.T) {
	old := supervisorReadyBound
	supervisorReadyBound = 60 * time.Millisecond
	t.Cleanup(func() { supervisorReadyBound = old })
	var rec []string
	d := mockDeps(&rec)
	fakeSupervisor(&d, &rec, "/Users/Shared/yolo/proj", readyLine, "", "", false)
	rc, out := runGuestLaunch(t, d)
	if rc != 42 {
		t.Fatalf("rc = %d\n%s", rc, out)
	}
	if strings.Contains(out, "Started openai-auth-broker") {
		t.Errorf("an earlier session's readiness line was taken for this start:\n%s", out)
	}
	if !strings.Contains(out, "has not said it is supervising") ||
		!strings.Contains(out, SupervisorLogPath("/Users/Shared/yolo/proj")) {
		t.Errorf("a silent supervisor is not reported as unconfirmed, naming its log:\n%s", out)
	}
}

// A SUPERVISOR THAT EXITS BEFORE IT STARTS REFUSES THE LAUNCH, and says so: the log's path, the
// lines THIS start added (not an earlier session's), and what sudo or sandbox-exec printed
// before the log took over. It is stopped and its env file swept, and the agent never runs.
func TestASupervisorThatExitsBeforeStartingIsDisclosedWithItsLog(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, &rec, ws, "an earlier session's line\n",
		"yolo-sandbox-env: cannot open /var/yolo-jail/env/x.daemons.env\n",
		"sudo: a password is required\n", true)
	rc, out := runGuestLaunch(t, d)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out)
	}
	for _, want := range []string{
		"supervisor exited before it started openai-auth-broker",
		SupervisorLogPath(ws),
		"cannot open /var/yolo-jail/env/x.daemons.env",
		"sudo: a password is required",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "an earlier session's line") || strings.Contains(out, "Started ") {
		t.Errorf("the refusal quotes an earlier session or claims a start:\n%s", out)
	}
	joined := strings.Join(rec, "\n")
	if strings.Contains(joined, "proxy:") {
		t.Error("the agent ran after its supervisor failed")
	}
	if !strings.Contains(joined, "stop\nrun:sudo "+rmBin+" -f "+SandboxDaemonEnvFile(cnameFor(ws), "")) {
		t.Errorf("the failed supervisor is not stopped and its env file swept:\n%s", joined)
	}
}

// EXITED OUTRANKS THE LINE: a supervisor that wrote its readiness line and died at once is not
// running, and is refused rather than reported started.
func TestASupervisorThatDiesAfterItsLineIsNotStarted(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	fakeSupervisor(&d, &rec, "/Users/Shared/yolo/proj", "", readyLine+"panic: boom\n", "", true)
	rc, out := runGuestLaunch(t, d)
	if rc != 1 || strings.Contains(out, "Started ") || !strings.Contains(out, "panic: boom") {
		t.Errorf("rc = %d, want a refusal quoting the log:\n%s", rc, out)
	}
}

// THE REAL STARTER REPORTS AN EXIT AND KEEPS WHAT THE PROCESS PRINTED — the two things the wait
// needs to say "exited before it started" and quote sudo's refusal. Without Exited a failed start
// would read as a silent one; without Output a sudo -n refusal would leave no trace again.
func TestStartBackgroundRealReportsExitAndCapturesOutput(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	bg, err := startBackgroundReal([]string{"/bin/sh", "-c", "echo 'sudo: a password is required' >&2; exit 1"})
	if err != nil {
		t.Fatal(err)
	}
	defer bg.Stop()
	select {
	case <-bg.Exited:
	case <-time.After(10 * time.Second):
		t.Fatal("Exited never closed for a process that exited")
	}
	if got := bg.Output(); got != "sudo: a password is required\n" {
		t.Errorf("Output = %q", got)
	}
}
