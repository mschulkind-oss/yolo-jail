package entrypoint

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// withJailMainDir points the jail-main state at a fresh temp dir and shrinks every bound, so a
// test drives the real files and the real flock in milliseconds.
func withJailMainDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "main")
	saved := []any{jailMainDir, bootWaitLimit, provisionWaitLimit, claimWaitLimit, sessionPoll, waitNoticeAfter}
	jailMainDir = dir
	bootWaitLimit, provisionWaitLimit, claimWaitLimit = time.Second, time.Second, 300*time.Millisecond
	sessionPoll, waitNoticeAfter = 10*time.Millisecond, 0
	t.Cleanup(func() {
		jailMainDir = saved[0].(string)
		bootWaitLimit = saved[1].(time.Duration)
		provisionWaitLimit = saved[2].(time.Duration)
		claimWaitLimit = saved[3].(time.Duration)
		sessionPoll = saved[4].(time.Duration)
		waitNoticeAfter = saved[5].(time.Duration)
	})
	return dir
}

// TestParseEntryArgsTellsTheThreeFormsApart: the two launcher forms are exactly two arguments;
// anything else is a session's command, including the flags' own text as one argument, which
// is how `yolo -- --yolo-hold-main x` arrives.
func TestParseEntryArgsTellsTheThreeFormsApart(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		mode     entryMode
		argument string
	}{
		{[]string{HoldMainArg, "stage"}, modeHold, "stage"},
		{[]string{FirstSessionArg, "claude --x"}, modeFirstSession, "claude --x"},
		{nil, modeSession, "bash"},
		{[]string{"echo hi"}, modeSession, "echo hi"},
		{[]string{HoldMainArg + " x"}, modeSession, HoldMainArg + " x"},
		{[]string{HoldMainArg}, modeSession, HoldMainArg},
		{[]string{HoldMainArg, "a", "b"}, modeSession, HoldMainArg + " a b"},
	} {
		mode, arg := parseEntryArgs(tc.args)
		if mode != tc.mode || arg != tc.argument {
			t.Errorf("parseEntryArgs(%q) = (%v, %q), want (%v, %q)", tc.args, mode, arg, tc.mode, tc.argument)
		}
	}
}

// TestTheHoldDropsHangupsAndInterruptsAndEndsOnSigterm: a stray SIGHUP or SIGINT must not end
// every session; SIGTERM, which `podman stop` sends, does.
func TestTheHoldDropsHangupsAndInterruptsAndEndsOnSigterm(t *testing.T) {
	signals := make(chan os.Signal, 3)
	signals <- syscall.SIGHUP
	signals <- syscall.SIGINT
	done := make(chan bool, 1)
	go func() { done <- holdUntil(signals, nil) }()
	select {
	case <-done:
		t.Fatal("the hold ended on SIGHUP or SIGINT")
	case <-time.After(50 * time.Millisecond):
	}
	signals <- syscall.SIGTERM
	select {
	case terminated := <-done:
		if !terminated {
			t.Error("the hold ended on SIGTERM without saying a SIGTERM ended it")
		}
	case <-time.After(time.Second):
		t.Fatal("the hold did not end on SIGTERM")
	}
}

// TestAHoldThatASigtermEndedExitsWithItsStatus: a stopped jail's main process exits 128+SIGTERM,
// the status of a process the signal ended, which is what the jail's keeper reads off its client.
func TestAHoldThatASigtermEndedExitsWithItsStatus(t *testing.T) {
	var st *ExitStatus
	if err := holdExitStatus(true); !errors.As(err, &st) || st.Code != 128+int(syscall.SIGTERM) || st.Message != "" {
		t.Errorf("a hold a SIGTERM ended returns %v, want a silent exit status of %d", err, 128+int(syscall.SIGTERM))
	}
	if err := holdExitStatus(false); err != nil {
		t.Errorf("a hold that ended otherwise returns %v, want a clean exit", err)
	}
}

// TestTheHoldOutlivesItsFirstSession: since the keeper (step 3 of
// docs/design/jail-lifetime-last-session-wins.md §7), nothing a session does ends the hold. A first
// session registered and gone leaves it holding, and only a SIGTERM ends it. Until then the hold
// followed its first session out, which is what ended every other session with the first. Drives
// holdJail itself, so a hold that follows its first session again fails here.
func TestTheHoldOutlivesItsFirstSession(t *testing.T) {
	withJailMainDir(t)
	// A pid no process can have: the first session is gone.
	if !newSessionGate(false, &bytes.Buffer{}).registerFirst(1 << 30) {
		t.Fatal("the first registration was refused")
	}
	done := make(chan bool, 1)
	go func() { done <- holdJail("", io.Discard) }()
	select {
	case <-done:
		t.Fatal("the hold ended with its first session gone")
	case <-time.After(500 * time.Millisecond):
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case terminated := <-done:
		if !terminated {
			t.Error("the hold ended without saying a SIGTERM ended it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hold did not end on SIGTERM")
	}
}

// TestOnlyOneFirstSessionRegisters: a second exec asking to be first is an ordinary session.
func TestOnlyOneFirstSessionRegisters(t *testing.T) {
	withJailMainDir(t)
	g := newSessionGate(false, &bytes.Buffer{})
	if !g.registerFirst(10) {
		t.Fatal("the first registration was refused")
	}
	if g.registerFirst(11) {
		t.Fatal("a second first session registered")
	}
	if raw, _ := readMainState(firstSessionFile); raw != "10" {
		t.Errorf("first-session = %q, want the first registrant's pid", raw)
	}
}

// TestAnnounceReadyRecordsBeforeItSpeaks: the stage and the ready state are on disk when the
// line the launcher starts the first session on is printed, and the line is BootReadyLine.
func TestAnnounceReadyRecordsBeforeItSpeaks(t *testing.T) {
	withJailMainDir(t)
	markBoot(bootBooting)
	var out bytes.Buffer
	announceReady("echo provision", &out)
	if out.String() != BootReadyLine+"\n" {
		t.Errorf("printed %q, want exactly the ready line", out.String())
	}
	if st, _ := readMainState(bootStateFile); st != bootReady {
		t.Errorf("boot state %q, want %q", st, bootReady)
	}
	if st, _ := readMainState(provisionScriptFile); st != "echo provision" {
		t.Errorf("recorded stage %q", st)
	}
}

// TestASessionWaitsForTheBootAndIsRefusedByARefusedOne covers awaitBoot's three ends.
func TestASessionWaitsForTheBootAndIsRefusedByARefusedOne(t *testing.T) {
	withJailMainDir(t)
	var out bytes.Buffer
	g := newSessionGate(false, &out)

	markBoot(bootBooting)
	go func() { time.Sleep(50 * time.Millisecond); markBoot(bootReady) }()
	if err := g.awaitBoot(); err != nil {
		t.Fatalf("a boot that finished refused the session: %v", err)
	}
	if !strings.Contains(out.String(), "waiting for this jail's boot") {
		t.Errorf("the wait did not say what it waited for:\n%s", out.String())
	}

	markBoot(bootRefused)
	var st *ExitStatus
	if err := g.awaitBoot(); !errors.As(err, &st) || st.Code == 0 || !strings.Contains(st.Message, "boot refused") {
		t.Errorf("a refused boot must refuse the session, got %v", err)
	}

	markBoot(bootBooting)
	if err := g.awaitBoot(); !errors.As(err, &st) || !strings.Contains(st.Message, "has not finished") {
		t.Errorf("a boot that never finishes must end the wait at its bound, got %v", err)
	}
}

// TestTheFirstSessionProvisionsAndEveryOtherWaitsForTheOutcome: the first session claims and
// runs the stage; a session that arrives meanwhile waits while the lock is held and proceeds
// on "done" without running anything.
func TestTheFirstSessionProvisionsAndEveryOtherWaitsForTheOutcome(t *testing.T) {
	withJailMainDir(t)
	announceReady("the stage", &bytes.Buffer{})
	first := newSessionGate(true, &bytes.Buffer{})
	if p, err := first.claim(); err != nil || !p {
		t.Fatal(err)
	}

	var waiterOut bytes.Buffer
	waiter := newSessionGate(true, &waiterOut)
	type res struct {
		provisioner bool
		err         error
	}
	got := make(chan res, 1)
	go func() { p, err := waiter.await(); got <- res{p, err} }()
	select {
	case r := <-got:
		t.Fatalf("the waiter went ahead while the first session held provisioning: %+v", r)
	case <-time.After(60 * time.Millisecond):
	}

	var ran []string
	if rc := first.provision(func(stage string) int { ran = append(ran, stage); return 0 }); rc != 0 {
		t.Fatalf("provision rc %d", rc)
	}
	if len(ran) != 1 || ran[0] != "the stage" {
		t.Errorf("the first session ran %q, want the recorded stage once", ran)
	}
	r := <-got
	if r.err != nil || r.provisioner {
		t.Errorf("after done the waiter must proceed without provisioning, got %+v", r)
	}
	if !strings.Contains(waiterOut.String(), "finish provisioning") {
		t.Errorf("the waiter did not say what it waited for:\n%s", waiterOut.String())
	}
	if raw, _ := readMainState(provisionOutcomeFile); raw != outcomeDone {
		t.Errorf("outcome %q, want %q", raw, outcomeDone)
	}
}

// TestARefusedProvisioningRefusesEveryWaiterWithItsStatus: the refusal's own status reaches the
// waiter, so every session of a refused jail ends the way the first did.
func TestARefusedProvisioningRefusesEveryWaiterWithItsStatus(t *testing.T) {
	withJailMainDir(t)
	announceReady("stage", &bytes.Buffer{})
	first := newSessionGate(true, &bytes.Buffer{})
	if p, err := first.claim(); err != nil || !p {
		t.Fatal(err)
	}
	if rc := first.provision(func(string) int { return 78 }); rc != 78 {
		t.Fatalf("provision rc %d, want the stage's 78", rc)
	}
	_, err := newSessionGate(true, &bytes.Buffer{}).await()
	var st *ExitStatus
	if !errors.As(err, &st) || st.Code != 78 || !strings.Contains(st.Message, "refused") {
		t.Errorf("a waiter after a refusal must be refused with 78, got %v", err)
	}
}

// TestAnAbandonedRunIsRerunOnATerminalAndRefusedWithout: a claim, a free lock and no outcome
// is a run that was abandoned. A waiter with a terminal takes it over, holding the lock; one
// without is refused rather than let through where a person is asked.
func TestAnAbandonedRunIsRerunOnATerminalAndRefusedWithout(t *testing.T) {
	withJailMainDir(t)
	announceReady("stage", &bytes.Buffer{})
	first := newSessionGate(true, &bytes.Buffer{})
	if p, err := first.claim(); err != nil || !p {
		t.Fatal(err)
	}
	first.abandon() // the first session died mid-run

	_, err := newSessionGate(false, &bytes.Buffer{}).await()
	var st *ExitStatus
	if !errors.As(err, &st) || !strings.Contains(st.Message, "abandoned") {
		t.Fatalf("a waiter with no terminal must be refused, got %v", err)
	}

	var out bytes.Buffer
	rerun := newSessionGate(true, &out)
	p, err := rerun.await()
	if err != nil || !p {
		t.Fatalf("a waiter with a terminal must take the run over, got provisioner=%v err=%v", p, err)
	}
	other, oerr := rerun.openLock()
	if oerr != nil {
		t.Fatal(oerr)
	}
	defer other.Close()
	if syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		t.Error("the waiter that took the run over does not hold the provisioning lock")
	}
	if rc := rerun.provision(func(string) int { return 0 }); rc != 0 {
		t.Fatal(rc)
	}
	if raw, _ := readMainState(provisionOutcomeFile); raw != outcomeDone {
		t.Errorf("outcome %q after the rerun", raw)
	}
}

// TestAFirstSessionThatNeverArrivesIsTreatedAsAbandoned: nobody claimed the run within the
// bound, so its launcher is gone, and the waiter's terminal takes it.
func TestAFirstSessionThatNeverArrivesIsTreatedAsAbandoned(t *testing.T) {
	withJailMainDir(t)
	announceReady("stage", &bytes.Buffer{})
	start := time.Now()
	p, err := newSessionGate(true, &bytes.Buffer{}).await()
	if err != nil || !p {
		t.Fatalf("got provisioner=%v err=%v, want the waiter to take the unclaimed run", p, err)
	}
	if time.Since(start) < claimWaitLimit {
		t.Errorf("the waiter took the run after %s, before the claim bound %s", time.Since(start), claimWaitLimit)
	}
}

// TestALateFirstSessionActsOnATakenOverRunInsteadOfRunningItAgain: a waiter gave up on a first
// session that had not arrived and ran the stage itself. The first session that does arrive
// waits for that run while it holds the lock, then acts on its outcome: done lets it through
// without running the stage a second time, and a refusal refuses it with the stage's status.
func TestALateFirstSessionActsOnATakenOverRunInsteadOfRunningItAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		rc   int
	}{{"done", 0}, {"refused", 78}} {
		t.Run(tc.name, func(t *testing.T) {
			withJailMainDir(t)
			announceReady("stage", &bytes.Buffer{})
			takeover := newSessionGate(true, &bytes.Buffer{})
			if p, err := takeover.claim(); err != nil || !p {
				t.Fatalf("the takeover could not claim a free run: %v", err)
			}
			type res struct {
				provisioner bool
				err         error
			}
			got := make(chan res, 1)
			go func() {
				p, err := newSessionGate(true, &bytes.Buffer{}).claim()
				got <- res{p, err}
			}()
			select {
			case r := <-got:
				t.Fatalf("the late first session went ahead while the takeover held the run: %+v", r)
			case <-time.After(60 * time.Millisecond):
			}
			takeover.provision(func(string) int { return tc.rc })
			r := <-got
			if r.provisioner {
				t.Fatal("the late first session would run the stage a second time")
			}
			var st *ExitStatus
			if tc.rc == 0 && r.err != nil {
				t.Errorf("after done the late first session was refused: %v", r.err)
			}
			if tc.rc != 0 && (!errors.As(r.err, &st) || st.Code != tc.rc) {
				t.Errorf("after a refusal the late first session got %v, want exit %d", r.err, tc.rc)
			}
		})
	}
}

// TestAnEmptyStageIsDoneWithoutRunning: a launch that handed pid 1 no stage has nothing to
// provision, and the outcome is still recorded for the waiters.
func TestAnEmptyStageIsDoneWithoutRunning(t *testing.T) {
	withJailMainDir(t)
	announceReady("", &bytes.Buffer{})
	g := newSessionGate(true, &bytes.Buffer{})
	if p, err := g.claim(); err != nil || !p {
		t.Fatal(err)
	}
	if rc := g.provision(func(string) int { t.Error("ran an empty stage"); return 1 }); rc != 0 {
		t.Fatal(rc)
	}
	if raw, _ := readMainState(provisionOutcomeFile); raw != outcomeDone {
		t.Errorf("outcome %q", raw)
	}
}

// TestTheStageRunsWithItsStatusAndTheSessionActivation runs a real stage through bash: its exit
// status is what provisioning records, and it runs after the activation every session's shell
// gets, which sources the frozen user env file.
func TestTheStageRunsWithItsStatusAndTheSessionActivation(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("needs bash")
	}
	t.Setenv("PATH", os.Getenv("PATH"))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-user-env.sh"),
		[]byte("export STAGE_SAW=activated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	e.Home = home
	marker := filepath.Join(t.TempDir(), "saw")
	if rc := runProvisionStage(e, `printf %s "$STAGE_SAW" > '`+marker+`'; exit 78`); rc != 78 {
		t.Errorf("rc %d, want the stage's own 78", rc)
	}
	if b, _ := os.ReadFile(marker); string(b) != "activated" {
		t.Errorf("the stage ran without the session activation: saw %q", b)
	}
	if rc := runProvisionStage(e, "true"); rc != 0 {
		t.Errorf("rc %d for a stage that succeeded", rc)
	}
}

// TestMainWiresTheHoldAndTheGateInOrder pins Main's call sites, which no test can execute (the
// session path ends in an exec that replaces the process): the main process marks its boot
// before the boot and holds after it, and exits with what ended its hold; a session of a
// hold-main jail waits for the boot and takes or waits for provisioning before its own pass,
// which writes the pass log rather than the main process's boot.log (attachPassLog, whose
// choice TestASessionsPassLeavesTheJailsBootLog drives); the stage runs after the pass and
// before the exec, through provisionThisSession, which records its duration. Deleting any of
// these calls fails here.
func TestMainWiresTheHoldAndTheGateInOrder(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func Main(args []string) error {")
	if start < 0 {
		t.Fatal("Main not found")
	}
	body = body[start:]
	order := []string{
		"mode, command := parseEntryArgs(args)",
		"markBoot(bootBooting)",
		"e.Getenv(JailMainEnv) == JailMainHold",
		"gate.registerFirst(os.Getpid())",
		"gate.awaitBoot()",
		"gate.claim()",
		"gate.await()",
		"blog := attachPassLog(e, mode, gate != nil, os.Stderr)",
		"runBootSteps(&bootRun{e: e, target: bootContainer",
		"if err := genFailuresError(e); err != nil {",
		"gate.abandon()",
		"markBoot(bootRefused)",
		"blog.finish(nil)",
		"return holdExitStatus(holdJail(command, os.Stderr))",
		"provisionThisSession(e, gate, runProvisionStage)",
		"return &ExitStatus{Code: rc}",
		"return execBash(e, command, mode != modeFirstSession)",
	}
	last := -1
	for _, frag := range order {
		i := strings.Index(body, frag)
		if i < 0 {
			t.Errorf("Main no longer contains %q", frag)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order in Main", frag)
		}
		last = i
	}
	hold, _ := os.ReadFile("jailmain.go")
	// Since the keeper, nothing but a SIGTERM ends the hold: it follows no session out
	// (TestTheHoldOutlivesItsFirstSession drives it).
	if !strings.Contains(string(hold), "holdUntil(signals, nil)") {
		t.Error("holdJail no longer holds until a SIGTERM alone")
	}
	if !strings.Contains(string(hold), "signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)") {
		t.Error("holdJail no longer catches the signals it must drop and the one it ends on")
	}
}

// TestProvisioningHandsTheShellItsDuration: the in-container timing block reads how long the
// first session's provisioning took from ProvisionMillisEnv, in the session's own environment
// and in the process's, which the shell it execs inherits.
func TestProvisioningHandsTheShellItsDuration(t *testing.T) {
	withJailMainDir(t)
	t.Setenv(ProvisionMillisEnv, "")
	announceReady("the stage", &bytes.Buffer{})
	g := newSessionGate(true, &bytes.Buffer{})
	if p, err := g.claim(); err != nil || !p {
		t.Fatalf("claim: %v %v", p, err)
	}
	e := NewEnv(map[string]string{})
	var ran string
	rc := provisionThisSession(e, g, func(got *Env, stage string) int {
		if got != e {
			t.Error("the stage was not run with the session's own Env")
		}
		ran = stage
		time.Sleep(30 * time.Millisecond)
		return 0
	})
	if rc != 0 || ran != "the stage" {
		t.Fatalf("rc %d ran %q, want 0 and the recorded stage", rc, ran)
	}
	for where, v := range map[string]string{"the session's Env": e.Getenv(ProvisionMillisEnv), "the process": os.Getenv(ProvisionMillisEnv)} {
		if ms, err := strconv.Atoi(v); err != nil || ms < 30 {
			t.Errorf("%s carries %s=%q, want the stage's duration in milliseconds (at least 30)", where, ProvisionMillisEnv, v)
		}
	}
}

// TestProvisioningWritesItsDurationIntoTheJailPerfLog: a stage that ran leaves its duration and
// status in ~/.yolo-perf.log, inside the block the session's boot pass dumped, and adds no block
// header of its own: the in-container profile prints only the log's last `=== YOLO` block, so a
// header would hide the boot checkpoints. Deleting the write in provisionThisSession fails here.
func TestProvisioningWritesItsDurationIntoTheJailPerfLog(t *testing.T) {
	withJailMainDir(t)
	t.Setenv(ProvisionMillisEnv, "")
	home := t.TempDir()
	logPath := filepath.Join(home, ".yolo-perf.log")
	boot := "=== YOLO Jail Entrypoint Perf (2026-10-08 00:00:00) ===\n    0.010s             a step\n  Total: 0.010s\n\n"
	if err := os.WriteFile(logPath, []byte(boot), 0o644); err != nil {
		t.Fatal(err)
	}
	announceReady("the stage", &bytes.Buffer{})
	g := newSessionGate(true, &bytes.Buffer{})
	if p, err := g.claim(); err != nil || !p {
		t.Fatalf("claim: %v %v", p, err)
	}
	e := NewEnv(map[string]string{})
	e.Home = home
	if rc := provisionThisSession(e, g, func(*Env, string) int { return 3 }); rc != 3 {
		t.Fatalf("rc %d, want the stage's 3", rc)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if n := strings.Count(got, "=== YOLO"); n != 1 {
		t.Errorf("the log has %d block headers, want the boot pass's one:\n%s", n, got)
	}
	last := got[strings.LastIndex(got, "=== YOLO"):]
	if !strings.Contains(last, "a step") || !regexp.MustCompile(`(?m)^  provisioning stage: [0-9]+ms \(exit 3\)$`).MatchString(last) {
		t.Errorf("the last block lacks the boot checkpoints or the stage's line:\n%s", got)
	}
}

// TestNoStageWritesNoProvisioningLine: a jail whose recorded stage is empty ran nothing, so the
// perf log gains no provisioning line (and an empty home writes no file anywhere).
func TestNoStageWritesNoProvisioningLine(t *testing.T) {
	withJailMainDir(t)
	t.Setenv(ProvisionMillisEnv, "")
	home := t.TempDir()
	announceReady("", &bytes.Buffer{})
	g := newSessionGate(true, &bytes.Buffer{})
	if p, err := g.claim(); err != nil || !p {
		t.Fatalf("claim: %v %v", p, err)
	}
	e := NewEnv(map[string]string{})
	e.Home = home
	if rc := provisionThisSession(e, g, func(*Env, string) int {
		t.Error("an empty stage was run")
		return 0
	}); rc != 0 {
		t.Fatalf("rc %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(home, ".yolo-perf.log")); !os.IsNotExist(err) {
		t.Errorf("a stage that never ran wrote the perf log (%v)", err)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	appendProvisionPerf("", 1, 0)
	if _, err := os.Stat(filepath.Join(cwd, ".yolo-perf.log")); !os.IsNotExist(err) {
		t.Errorf("an empty home wrote a perf log in the working directory (%v)", err)
	}
}

const provisionInterruptRoleEnv = "YOLO_ENTRYPOINT_PROVISION_INTERRUPT_ROLE"

// TestACtrlCDuringProvisioningIsTheStagesToDecide: a Ctrl-C at the first session's terminal
// reaches its entrypoint and the stage together (one process group). The entrypoint must
// survive it, so that it records what the STAGE decided: a stage that exits 130 on the
// interrupt is a refusal with that status, which every waiting session then reads. Without the
// entrypoint catching SIGINT for the stage's life it would die first, and the waiters would
// read an abandoned run instead. The child process stands in for the first session's
// entrypoint, in a process group of its own, as a terminal's foreground job is.
func TestACtrlCDuringProvisioningIsTheStagesToDecide(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("needs bash")
	}
	if dir := os.Getenv(provisionInterruptRoleEnv); dir != "" {
		jailMainDir = dir
		announceReady(`trap 'exit 130' INT; kill -INT 0; sleep 5; exit 0`, &bytes.Buffer{})
		g := newSessionGate(true, &bytes.Buffer{})
		if p, err := g.claim(); err != nil || !p {
			fmt.Println("claim:", p, err)
			os.Exit(2)
		}
		home := t.TempDir()
		e := NewEnv(map[string]string{"JAIL_HOME": home})
		e.Home = home
		rc := provisionThisSession(e, g, runProvisionStage)
		raw, _ := readMainState(provisionOutcomeFile)
		fmt.Printf("RC=%d OUTCOME=%s\n", rc, raw)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestACtrlCDuringProvisioningIsTheStagesToDecide$", "-test.count=1")
	cmd.Env = append(os.Environ(), provisionInterruptRoleEnv+"="+filepath.Join(t.TempDir(), "main"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the first session's entrypoint did not survive the Ctrl-C (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "RC=130 OUTCOME=refused 130") {
		t.Errorf("want the stage's 130 recorded as the run's refusal:\n%s", out)
	}
}
