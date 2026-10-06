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
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
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

// guestLaunch is a launch that runs a jail daemon (so it builds the guest binaries and starts the
// supervisor), provisions (provisionCfg) and probes a published host service (brokerEndpointEnv),
// so each of RunMacosUser's later step boundaries has a step on each side of it.
func guestLaunch(rec *[]string) (Deps, Options, *bytes.Buffer) {
	d := mockDeps(rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, rec, ws, "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(ws)
	o.Config = provisionCfg()
	o.JailDaemons = openAIAdapterDaemons("")
	o.PackEnv = brokerEndpointEnv()
	return d, o, &buf
}

// isProbe is the service probe's argv; isProvision the provisioning stage's, the first sandboxed
// Run after the bootstrap that is not the probe.
func isProbe(argv []string) bool { return slices.Contains(argv, ProbeServicesVerb) }

func isBootstrap(argv []string) bool {
	return strings.Contains(strings.Join(argv, " "), "darwin-bootstrap")
}

// A SIGNAL DURING THE GUEST-BINARIES BUILD ends the launch at the boundary after it, whatever the
// build returned: no message about the binaries, and not one privileged step after it.
func TestASignalDuringTheGuestBinariesBuildEndsTheLaunch(t *testing.T) {
	for name, result := range map[string]error{"stopped": nixchildren.ErrStopped, "finished": nil} {
		t.Run(name, func(t *testing.T) {
			var rec []string
			d, o, buf := guestLaunch(&rec)
			flip := endingAfter(&d)
			d.GuestBinaries = func(string) (string, error) {
				flip()
				if result != nil {
					return "", result
				}
				return "/opt/yolo/bin/darwin-arm64", nil
			}
			if rc := RunMacosUser(d, o); rc != 143 {
				t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
			}
			if strings.Contains(buf.String(), "Could not provide") {
				t.Errorf("a build the signal ended was reported as one that failed:\n%s", buf.String())
			}
			if i := slices.IndexFunc(rec, func(r string) bool {
				return strings.HasPrefix(r, "install:") || strings.HasPrefix(r, "run:sudo")
			}); i >= 0 {
				t.Errorf("the launch went on past the guest binaries to %s", rec[i])
			}
		})
	}
}

// A SIGNAL DURING THE PROVISIONING STAGE ends the launch whatever the stage reported — exiting 0
// with no marker reads as "never ran" and would launch — so the supervisor never starts and the
// session never runs, and none of the stage's own failure lines is printed (one of them says
// "Launching anyway").
func TestASignalDuringProvisioningNeverStartsTheSupervisorOrTheSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rc      int
		cleared bool // the stage's log was cleared, so an empty one reads as "never ran"
	}{
		{"finished", 0, false},
		{"cut short", 1, false},
		{"never started", 1, true},
		{"refused", provision.RefusedStatus, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []string
			d, o, buf := guestLaunch(&rec)
			if tc.cleared {
				d.RemoveFile = func(string) bool { return true }
			}
			flip := endingAfter(&d)
			run := d.Run
			booted, provisioned := false, false
			d.Run = func(argv []string) int {
				rc := run(argv)
				switch {
				case isBootstrap(argv):
					booted = true
				case booted && !provisioned && !isProbe(argv) && slices.Contains(argv, "/usr/bin/sandbox-exec"):
					provisioned = true
					flip()
					return tc.rc
				}
				return rc
			}
			started := 0
			o.OnAgentStart = func() { started++ }
			if rc := RunMacosUser(d, o); rc != 143 {
				t.Fatalf("rc = %d, want 143\n%s\n%s", rc, buf.String(), strings.Join(rec, "\n"))
			}
			if !provisioned {
				t.Fatalf("the fixture never provisioned:\n%s", strings.Join(rec, "\n"))
			}
			if i := recAt(rec, "start:"); i >= 0 {
				t.Errorf("the supervisor started after the signal: %s", rec[i])
			}
			if recAt(rec, "proxy:") >= 0 || started != 0 {
				t.Errorf("the session (or its start hook, %d) ran after the signal:\n%s", started, strings.Join(rec, "\n"))
			}
			for _, quiet := range []string{"Provisioning refused", "Provisioning was aborted", "could not be started"} {
				if strings.Contains(buf.String(), quiet) {
					t.Errorf("a stage the signal cut short was reported (%q):\n%s", quiet, buf.String())
				}
			}
		})
	}
}

// A PRIVILEGED STEP THE SIGNAL MADE FAIL — its sudo stopped at the password prompt fails like any
// refusal — returns the signal's status and does not report the step's failure, which would name a
// cause that is not the cause. Each row fails one step after flipping the ending.
func TestAStepTheSignalMadeFailReturnsTheSignalsStatus(t *testing.T) {
	type failAt struct {
		install func(path string) bool // InstallRootFile of path fails
		run     func(argv []string) bool
	}
	ws := "/Users/Shared/yolo/proj"
	envFile := func(path string) bool {
		return strings.HasSuffix(path, ".env") && !strings.HasSuffix(path, ".daemons.env")
	}
	joined := func(sub string) func([]string) bool {
		return func(argv []string) bool { return strings.Contains(strings.Join(argv, " "), sub) }
	}
	for _, tc := range []struct {
		name  string
		at    failAt
		quiet string // the step's failure message, absent once the signal is ending the launch
		ca    bool   // the fixture with a keychain CA, so the CA files are written
	}{
		{"profile", failAt{install: func(p string) bool { return strings.HasSuffix(p, ".sb") }}, "Could not write Seatbelt profile", false},
		{"stage", failAt{run: joined("/var/yolo-jail/bin/yolo.new")}, "Could not stage entrypoint", false},
		{"env dir", failAt{run: joined("/bin/chmod 0700 /var/yolo-jail/env")}, "Could not prepare the session environment directory", false},
		{"env file", failAt{install: envFile}, "Could not write the session environment file", false},
		{"env grant", failAt{run: func(argv []string) bool {
			return slices.ContainsFunc(argv, envFile) && slices.Contains(argv, "+a")
		}}, "Could not grant", false},
		// The CA files' own messages are cabundle.go's, which prints them whatever the signal; the
		// launch's status is still the signal's.
		{"CA bundle", failAt{install: func(p string) bool { return strings.HasSuffix(p, caBundleSuffix) }}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []string
			d := mockDeps(&rec)
			if tc.ca {
				d = caDeps(t, &rec, newKeychainFixture(t))
			}
			flip := endingAfter(&d)
			hit := false
			if tc.at.install != nil {
				install := d.InstallRootFile
				d.InstallRootFile = func(path, content, mode string) bool {
					ok := install(path, content, mode)
					if !hit && tc.at.install(path) {
						hit = true
						flip()
						return false
					}
					return ok
				}
			}
			if tc.at.run != nil {
				run := d.Run
				d.Run = func(argv []string) int {
					rc := run(argv)
					if !hit && tc.at.run(argv) {
						hit = true
						flip()
						return 1
					}
					return rc
				}
			}
			var buf bytes.Buffer
			d.Out = &buf
			rc := RunMacosUser(d, newOpts(ws))
			if !hit {
				t.Fatalf("the fixture never reached the step it fails:\n%s", strings.Join(rec, "\n"))
			}
			if rc != 143 {
				t.Errorf("rc = %d, want the signal's 143\n%s", rc, buf.String())
			}
			if tc.quiet != "" && strings.Contains(buf.String(), tc.quiet) {
				t.Errorf("a step the signal made fail was reported as failing (%q):\n%s", tc.quiet, buf.String())
			}
			if recAt(rec, "darwin-bootstrap") >= 0 || recAt(rec, "proxy:") >= 0 {
				t.Errorf("the launch went on after the failed step:\n%s", strings.Join(rec, "\n"))
			}
		})
	}
}

// THE SUPERVISOR'S START, ended by the signal: a supervisor that exited, or never started, returns
// the signal's status and says nothing of a supervisor failure.
func TestASupervisorTheSignalEndedReturnsTheSignalsStatus(t *testing.T) {
	for name, exited := range map[string]bool{"exited": true, "never started": false} {
		t.Run(name, func(t *testing.T) {
			var rec []string
			d, o, buf := guestLaunch(&rec)
			fakeSupervisor(&d, &rec, o.Workspace, "", "", "", exited)
			flip := endingAfter(&d)
			start := d.StartBackground
			d.StartBackground = func(argv []string) (Background, error) {
				flip()
				if !exited {
					rec = append(rec, "start:"+strings.Join(argv, " "))
					return Background{}, errFake("sudo: a password is required")
				}
				return start(argv)
			}
			if rc := RunMacosUser(d, o); rc != 143 {
				t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
			}
			for _, quiet := range []string{"supervisor exited before", "Could not start the sandbox's jail daemons"} {
				if strings.Contains(buf.String(), quiet) {
					t.Errorf("a supervisor the signal ended was reported as failing (%q):\n%s", quiet, buf.String())
				}
			}
			if recAt(rec, "proxy:") >= 0 {
				t.Error("the session ran after the signal")
			}
		})
	}
}

// A SIGNAL WHILE THE SUPERVISOR STARTS ends the launch before the service probe, the next step.
func TestASignalWhileTheSupervisorStartsRunsNoServiceProbe(t *testing.T) {
	var rec []string
	d, o, buf := guestLaunch(&rec)
	flip := endingAfter(&d)
	start := d.StartBackground
	d.StartBackground = func(argv []string) (Background, error) {
		bg, err := start(argv)
		flip()
		return bg, err
	}
	probed := false
	run := d.Run
	d.Run = func(argv []string) int {
		probed = probed || isProbe(argv)
		return run(argv)
	}
	if rc := RunMacosUser(d, o); rc != 143 {
		t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
	}
	if probed {
		t.Errorf("the service probe ran after the signal:\n%s", strings.Join(rec, "\n"))
	}
}

// A SIGNAL DURING THE SERVICE PROBE: a probe it made refuse returns the signal's status, and a probe
// that passed still never reaches the session or its start hook (the last boundary).
func TestASignalDuringTheServiceProbeNeverReachesTheSession(t *testing.T) {
	for name, probeRC := range map[string]int{"refused": provision.RefusedStatus, "passed": 0} {
		t.Run(name, func(t *testing.T) {
			var rec []string
			d, o, buf := guestLaunch(&rec)
			flip := endingAfter(&d)
			run := d.Run
			probed := false
			d.Run = func(argv []string) int {
				rc := run(argv)
				if isProbe(argv) {
					probed = true
					flip()
					return probeRC
				}
				return rc
			}
			started := 0
			o.OnAgentStart = func() { started++ }
			if rc := RunMacosUser(d, o); rc != 143 {
				t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
			}
			if !probed {
				t.Fatalf("the fixture never probed:\n%s", strings.Join(rec, "\n"))
			}
			if recAt(rec, "proxy:") >= 0 || started != 0 {
				t.Errorf("the session (or its start hook, %d) ran after the signal:\n%s", started, strings.Join(rec, "\n"))
			}
		})
	}
}

// A SIGNAL AT THE CONTEXT PREFLIGHT — a Ctrl-C at its `sudo -u _yolojail` password prompt fails
// the probe — returns the signal's status, with no refusal claiming the sandbox account cannot
// reach the folder, and asks no further probe: whether the probe it ended was the last one or not.
func TestASignalAtTheContextPreflightIsNotARefusal(t *testing.T) {
	for name, links := range map[string][]ContextLink{
		"the last probe": {{Dest: "/ctx/notes.txt", Source: "/Users/matt/notes.txt"}},
		"an earlier probe": {{Dest: "/ctx/notes", Source: "/Users/matt/notes", Dir: true},
			{Dest: "/ctx/data", Source: "/Users/matt/data", Dir: true}},
	} {
		t.Run(name, func(t *testing.T) {
			var rec []string
			d := mockDeps(&rec)
			flip := endingAfter(&d)
			probes := 0
			d.Run = func(argv []string) int {
				rec = append(rec, "run:"+strings.Join(argv, " "))
				if slices.Contains(argv, "/bin/test") {
					probes++
					flip()
					return 1
				}
				return 0
			}
			var buf bytes.Buffer
			d.Out = &buf
			o := newOpts("/Users/Shared/yolo/proj")
			o.HostCtx.Links = links
			if rc := RunMacosUser(d, o); rc != 143 {
				t.Fatalf("rc = %d, want 143\n%s", rc, buf.String())
			}
			if strings.Contains(buf.String(), "cannot reach every context mount") {
				t.Errorf("a preflight the signal ended was reported as a refusal:\n%s", buf.String())
			}
			if probes != 1 {
				t.Errorf("the preflight asked %d probes, want it to stop at the one the signal ended", probes)
			}
		})
	}
}

// THE HOLD IS ASKED BEFORE THE CONTEXT PREFLIGHT: it runs no sudo, so a launch the hold refuses
// never prompts for the preflight's password.
func TestARefusedHoldRunsNoContextPreflight(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.HoldAccountHome = func(string, string, string) (func(), string) {
		return nil, "a macos-user session of /Users/Shared/yolo/other is running."
	}
	o := newOpts("/Users/Shared/yolo/proj")
	o.HostCtx.Links = []ContextLink{{Dest: "/ctx/notes", Source: "/Users/matt/notes", Dir: true}}
	var buf bytes.Buffer
	d.Out = &buf
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d, want the hold's refusal\n%s", rc, buf.String())
	}
	if i := recAt(rec, "run:"); i >= 0 {
		t.Errorf("a launch the hold refused ran %s first", rec[i])
	}
	if strings.Contains(buf.String(), "sudo may prompt") {
		t.Errorf("a launch the hold refused announced a sudo prompt:\n%s", buf.String())
	}
}
