package entrypoint

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// TestAHoldThatASigtermEndedExitsWithItsStatus: the main process's status is the one fact the
// launcher has that tells a jail stopped from outside from a first session killed while its
// jail ran (run.firstSessionStatus), since the kernel SIGKILLs every session in both cases.
func TestAHoldThatASigtermEndedExitsWithItsStatus(t *testing.T) {
	var st *ExitStatus
	if err := holdExitStatus(true); !errors.As(err, &st) || st.Code != 128+int(syscall.SIGTERM) || st.Message != "" {
		t.Errorf("a hold a SIGTERM ended returns %v, want a silent exit status of %d", err, 128+int(syscall.SIGTERM))
	}
	if err := holdExitStatus(false); err != nil {
		t.Errorf("a hold that followed its first session out returns %v, want a clean exit", err)
	}
}

// TestTheHoldFollowsTheFirstSession: until the keeper exists the jail lives with its first
// session, so the hold ends once the registered first session's process is gone — and not
// before one registered.
func TestTheHoldFollowsTheFirstSession(t *testing.T) {
	withJailMainDir(t)
	alive := make(chan struct{})
	saved := processExists
	processExists = func(pid int) bool {
		select {
		case <-alive:
			return false
		default:
			return pid == 4242
		}
	}
	t.Cleanup(func() { processExists = saved })

	gone := followFirstSession(5 * time.Millisecond)
	select {
	case <-gone:
		t.Fatal("the hold followed a first session that never registered")
	case <-time.After(40 * time.Millisecond):
	}
	if !newSessionGate(false, &bytes.Buffer{}).registerFirst(4242) {
		t.Fatal("the first registration was refused")
	}
	select {
	case <-gone:
		t.Fatal("the hold ended while the first session was alive")
	case <-time.After(40 * time.Millisecond):
	}
	close(alive)
	select {
	case <-gone:
	case <-time.After(time.Second):
		t.Fatal("the hold did not end when the first session's process did")
	}

	signals := make(chan os.Signal)
	done := make(chan bool, 1)
	go func() { done <- holdUntil(signals, gone) }()
	select {
	case terminated := <-done:
		if terminated {
			t.Error("a hold that followed its first session out says a SIGTERM ended it")
		}
	case <-time.After(time.Second):
		t.Fatal("holdUntil did not return on the first session's end")
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
// before the exec. Deleting any of these calls fails here.
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
		"gate.provision(",
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
	if !strings.Contains(string(hold), "holdUntil(signals, followFirstSession(firstSessionPoll))") {
		t.Error("holdJail no longer follows the first session")
	}
	if !strings.Contains(string(hold), "signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)") {
		t.Error("holdJail no longer catches the signals it must drop and the one it ends on")
	}
}
