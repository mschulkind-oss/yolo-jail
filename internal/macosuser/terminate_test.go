package macosuser

// terminate_test.go pins what RunMacosUser does once the launch's signal arm says a signal is
// ending it (Deps.Ending; internal/cli/run's macosuserarm.go): it returns that status at the next
// step boundary, so every deferred teardown runs — never the next step and never the session —
// and its teardown's removals run `sudo -n`, since the terminal that could answer a password prompt
// may be gone. And what it does with the hook the pipeline hands it for the session's start, and
// with the account home's hold. Each test fails if the orchestrator's call site is deleted.

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
)

// endingAfter wires d.Ending to report 143 once the returned flip has been called.
func endingAfter(d *Deps) (flip func()) {
	ending := false
	d.Ending = func() (int, bool) {
		if ending {
			return 128 + int(syscall.SIGTERM), true
		}
		return 0, false
	}
	return func() { ending = true }
}

// recAt is the index of the first record containing want, -1 for none.
func recAt(rec []string, want string) int {
	return slices.IndexFunc(rec, func(r string) bool { return strings.Contains(r, want) })
}

// A SIGNAL DURING THE BOOTSTRAP ENDS THE LAUNCH AT THE BOUNDARY AFTER IT: 143, no provisioning
// stage and no session, and the teardown removes the env file and the profile non-interactively.
func TestASignalDuringTheBootstrapEndsTheLaunchThroughItsTeardown(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	flip := endingAfter(&d)
	run := d.Run
	d.Run = func(argv []string) int {
		rc := run(argv)
		if strings.Contains(strings.Join(argv, " "), "darwin-bootstrap") {
			flip()
		}
		return rc
	}
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	o.Config = provisionCfg()
	started := 0
	o.OnAgentStart = func() { started++ }
	if rc := RunMacosUser(d, o); rc != 143 {
		t.Fatalf("rc = %d, want 143 (the signal's status)\n%s\n%s", rc, buf.String(), strings.Join(rec, "\n"))
	}
	boot := recAt(rec, "darwin-bootstrap")
	if boot < 0 {
		t.Fatalf("the fixture never bootstrapped:\n%s", strings.Join(rec, "\n"))
	}
	for i, r := range rec[boot+1:] {
		if strings.HasPrefix(r, "proxy:") {
			t.Errorf("the session ran after the signal: %s", r)
		}
		if strings.HasPrefix(r, "run:") && strings.Contains(r, "sandbox-exec") {
			t.Errorf("the provisioning stage ran after the signal (record %d): %s", boot+1+i, r)
		}
	}
	if started != 0 {
		t.Errorf("the session-start hook ran %d times on a launch the signal ended", started)
	}
	key := launchedSessionKey(t, rec, o.Workspace)
	for _, file := range []string{SandboxEnvFile(key, ""), profilePathFor(key)} {
		if recAt(rec, "run:sudo -n "+rmBin+" -f "+file) < 0 {
			t.Errorf("the teardown did not remove %s with `sudo -n`:\n%s", file, strings.Join(rec, "\n"))
		}
	}
	if strings.Contains(buf.String(), "entrypoint bootstrap failed") {
		t.Errorf("a bootstrap the signal ended was reported as failed:\n%s", buf.String())
	}
}

// profilePathFor is the Seatbelt profile a session of key installs.
func profilePathFor(key string) string { return stateDir + "/profile-" + key + ".sb" }

// A SIGNAL WHILE THE SUPERVISOR STARTS: the launch ends at the boundary after it, and its teardown
// stops the supervisor and sweeps the daemons' env file non-interactively, before the session-start
// hook or the session could run.
func TestASignalWhileTheSupervisorStartsStopsItAndSweepsItsEnvFile(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
	flip := endingAfter(&d)
	start := d.StartBackground
	d.StartBackground = func(argv []string) (Background, error) {
		bg, err := start(argv)
		flip()
		return bg, err
	}
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(ws)
	o.JailDaemons = openAIAdapterDaemons("")
	started := 0
	o.OnAgentStart = func() { started++ }
	if rc := RunMacosUser(d, o); rc != 143 {
		t.Fatalf("rc = %d, want 143\n%s\n%s", rc, buf.String(), strings.Join(rec, "\n"))
	}
	if recAt(rec, "stop") < 0 {
		t.Errorf("the teardown never stopped the supervisor:\n%s", strings.Join(rec, "\n"))
	}
	key := launchedSessionKey(t, rec, ws)
	if recAt(rec, "run:sudo -n "+rmBin+" -f "+SandboxDaemonEnvFile(key, "")) < 0 {
		t.Errorf("the daemons' env file was not swept with `sudo -n`:\n%s", strings.Join(rec, "\n"))
	}
	if recAt(rec, "proxy:") >= 0 || started != 0 {
		t.Errorf("the session (or its start hook, %d) ran after the signal:\n%s", started, strings.Join(rec, "\n"))
	}
}

// WITHOUT A SIGNAL the teardown is unchanged: plain `sudo`, which may still prompt on the terminal
// the session leaves, and no `-n` anywhere.
func TestAnUnsignaledTeardownKeepsPlainSudo(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	endingAfter(&d)
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want the session's 42", rc)
	}
	if i := recAt(rec, "sudo -n"); i >= 0 {
		t.Errorf("an unsignaled launch ran `sudo -n`: %s", rec[i])
	}
	if recAt(rec, "run:sudo "+rmBin+" -f ") < 0 {
		t.Errorf("the teardown removed nothing:\n%s", strings.Join(rec, "\n"))
	}
}

// A NIX THE SIGNAL STOPPED is not a failed build: the launch returns the signal's status and says
// nothing about fixing a package.
func TestABuildTheSignalStoppedIsNotReportedAsFailed(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	flip := endingAfter(&d)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		flip()
		return nil, false, nixchildren.ErrStopped
	}
	var buf bytes.Buffer
	d.Out = &buf
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 143 {
		t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
	}
	if strings.Contains(buf.String(), "Could not materialize") {
		t.Errorf("a build the signal stopped was reported as a package failure:\n%s", buf.String())
	}
	if recAt(rec, "install:") >= 0 {
		t.Errorf("the launch went on to install its profile:\n%s", strings.Join(rec, "\n"))
	}
}

// WITH AN ARM WIRED, RunMacosUser arms no nix stop of its own: that stop ends the process, which
// would skip the arm's teardown. With none wired it still arms it (TestASignalWhileTheSandboxNixRunsStopsIt).
func TestTheNixStopIsNotArmedWhenTheLaunchHasAnArm(t *testing.T) {
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	var rec []string
	d := mockDeps(&rec)
	d.Ending = func() (int, bool) { return 0, false }
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		select {
		case code := <-s.Exited():
			t.Errorf("RunMacosUser armed its own nix stop beside the arm, and it ended the launch %d", code)
		case <-time.After(300 * time.Millisecond):
		}
		return mockDarwin(), true, nil
	}
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want the session's 42", rc)
	}
}

// THE SESSION-START HOOK runs once, after the workspace lock is released and just before the
// session; never on a dry run or a refusal.
func TestTheSessionStartHookRunsOnceJustBeforeTheSession(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	released := false
	d.LockWorkspace = func(string, string) func() {
		return func() { released = true }
	}
	o := newOpts("/Users/Shared/yolo/proj")
	calls := 0
	o.OnAgentStart = func() {
		calls++
		rec = append(rec, "agent-start")
		if !released {
			t.Error("the session-start hook ran with the workspace lock still held")
		}
	}
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	hook, session := recAt(rec, "agent-start"), recAt(rec, "proxy:")
	if calls != 1 || hook < 0 || session < 0 || hook != session-1 {
		t.Errorf("the hook ran %d times at %d, the session at %d; want once, just before it:\n%s",
			calls, hook, session, strings.Join(rec, "\n"))
	}

	for name, mutate := range map[string]func(*Deps, *Options){
		"dry run":    func(_ *Deps, o *Options) { o.DryRun = true },
		"refusal":    func(d *Deps, _ *Options) { d.IsMacOS = func() bool { return false } },
		"late fault": func(d *Deps, _ *Options) { d.Run = func([]string) int { return 1 } },
	} {
		d := mockDeps(nil)
		o := newOpts("/Users/Shared/yolo/proj")
		mutate(&d, &o)
		calls := 0
		o.OnAgentStart = func() { calls++ }
		RunMacosUser(d, o)
		if calls != 0 {
			t.Errorf("%s: the session-start hook ran %d times", name, calls)
		}
	}
}

// THE ACCOUNT HOME'S HOLD is asked before the nix build and kept through the session: a refusal
// returns 1 before any build, sudo or bootstrap, naming what the seam said; an admitted launch
// holds it while its session runs and releases it after its teardown.
func TestTheAccountHomeHoldGatesTheBuildAndSpansTheSession(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var gotWS, gotCname, gotStep string
	d.HoldAccountHome = func(ws, cname, step string) (func(), string) {
		gotWS, gotCname, gotStep = ws, cname, step
		rec = append(rec, "hold")
		return nil, "a macos-user session of /Users/Shared/yolo/other is running."
	}
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		rec = append(rec, "materialize")
		return mockDarwin(), true, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts("/Users/Shared/yolo/proj")
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d, want the refusal's 1\n%s", rc, buf.String())
	}
	for _, r := range rec {
		if r == "materialize" || strings.HasPrefix(r, "install:") || strings.HasPrefix(r, "run:") {
			t.Errorf("a refused hold went on to %s", r)
		}
	}
	if !strings.Contains(buf.String(), "Refusing the macos-user launch: a macos-user session of /Users/Shared/yolo/other") {
		t.Errorf("the refusal is not printed:\n%s", buf.String())
	}
	if gotWS != resolvePathAbs(o.Workspace) || gotCname != cnameFor(resolvePathAbs(o.Workspace)) || gotStep != "" {
		t.Errorf("the hold was asked for (%q, %q, %q)", gotWS, gotCname, gotStep)
	}

	rec = nil
	d = mockDeps(&rec)
	held := false
	d.HoldAccountHome = func(string, string, string) (func(), string) {
		held = true
		return func() { held = false; rec = append(rec, "released") }, ""
	}
	heldInSession := false
	d.RunWithProxy = func([]string) int { heldInSession = held; rec = append(rec, "proxy:"); return 42 }
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	if !heldInSession {
		t.Error("the account home was not held while the session ran")
	}
	if held {
		t.Error("the account home's hold outlived the launch")
	}
	if rel, rm := recAt(rec, "released"), slices.IndexFunc(rec, func(r string) bool {
		return strings.HasPrefix(r, "run:sudo "+rmBin)
	}); rel < 0 || rm < 0 || rel < rm {
		t.Errorf("the hold must be released after the teardown's removals (released at %d, first removal at %d):\n%s",
			rel, rm, strings.Join(rec, "\n"))
	}
}
