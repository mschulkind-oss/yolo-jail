package entrypoint

// requiredservice_test.go pins OQ-R8 (docs/reference/loopback-tls-reachability.md, ruled
// 2026-10-05: (a)): the reachability hatch, YOLO_ALLOW_UNREACHABLE_SERVICES, reaches the boot's
// refusal when a REQUIRED in-jail service (the wire bridge) cannot start or publish, and that
// refusal says what it is about. It used to read
//
//	refusing to start the jail: 1 config generator(s) failed:
//	  - start_jail_daemon_supervisor: jail daemon "wire-bridge" cannot publish …: address already in use
//
// which calls the relay a config generator, names neither the pack that brought it nor a way
// forward, and could not be got past with the hatch its own sibling refusal advertises.
//
// Every case drives the boot STEP from the table (runSteps over the table's own entry) and reads
// the refusal through genFailuresError, the value Main branches on, so deleting the step's call
// to the supervisor, its hatch check or its refusal fails here.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// heldPortReason is the shape of the bridge's own `failed` reason on a held port: the cause in
// plain words, the bind error verbatim, the holder, and the next step.
const heldPortReason = "port 8214 is already taken (cannot bind 127.0.0.1:8214: listen tcp " +
	"127.0.0.1:8214: bind: address already in use — the address is already held: " +
	"127.0.0.1:8214 held by pid 42 (socat) [tcp]); free it and launch again"

// requiredServiceBoot is a container boot whose launch requires the wire bridge, with the
// shipped claude and wire-bridge packs staged, and a stand-in supervisor running body. The
// hatch is set when hatch is.
func requiredServiceBoot(t *testing.T, body string, hatch bool) (*bootRun, *bytes.Buffer) {
	t.Helper()
	bin := writeSupervisor(t, body)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	vars := map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	}
	if hatch {
		vars[paths.AllowUnreachableServicesEnv] = "1"
	}
	var stderr bytes.Buffer
	e := NewEnv(vars)
	e.Stderr = &stderr
	return &bootRun{e: e, target: bootContainer, packsLoaded: true,
		packs: capabilityPacks(t, "claude", "wire-bridge")}, &stderr
}

// runSupervisorStep runs the table's supervisor step the way the boot runs it.
func runSupervisorStep(t *testing.T, b *bootRun) {
	t.Helper()
	runSteps(b, []bootStep{mustBootStep(t, "start_jail_daemon_supervisor")})
}

func TestARequiredServiceThatCannotPublishRefusesNamingServicePackCauseAndHatch(t *testing.T) {
	b, stderr := requiredServiceBoot(t, `printf 'failed wire-bridge `+heldPortReason+`\n' >&3`, false)
	runSupervisorStep(t, b)

	err := genFailuresError(b.e)
	if err == nil {
		t.Fatal("a required in-jail service that cannot publish must still refuse the boot when " +
			"the hatch is not set; genFailuresError returned nil")
	}
	logPath := filepath.Join(b.e.Home, ".local", "state", "yolo-jail-daemons", "wire-bridge.log")
	for what, out := range map[string]string{"the refusal": err.Error(), "the boot's stderr": stderr.String()} {
		if strings.Contains(out, "config generator") {
			t.Errorf("%s still calls the relay a config generator:\n%s", what, out)
		}
		for _, want := range []string{
			"service 'wire-bridge'",                  // the service
			`pack "wire-bridge"`,                     // the pack whose service it is
			`"claude"`,                               // the selected pack whose needs brought it
			"port 8214 is already",                   // the cause, in the daemon's words
			"free it",                                // the daemon's next step
			paths.AllowUnreachableServicesEnv + "=1", // the way to a shell
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s does not name %q:\n%s", what, want, out)
			}
		}
	}
	if !strings.Contains(stderr.String(), logPath) {
		t.Errorf("the refusal does not name the daemon's log %s:\n%s", logPath, stderr.String())
	}
}

// THE HATCH REACHES EVERY WAY THE READINESS WAIT CAN END WITHOUT ITS SERVICE: a `failed` report,
// the supervisor going away before the service reported, and a supervisor that never started.
// Each continues the boot, and says what it let through, that nothing was repaired, and why.
func TestTheUnreachableServicesHatchLetsTheBootPastARequiredServiceThatDidNotStart(t *testing.T) {
	for _, tc := range []struct {
		name, body, cause string
		noSupervisor      bool
	}{
		{name: "failed report", body: `printf 'failed wire-bridge ` + heldPortReason + `\n' >&3`,
			cause: "port 8214 is already taken"},
		{name: "supervisor exits first", body: `exit 0`, cause: "exited before"},
		{name: "no supervisor", noSupervisor: true, cause: "yolo-jaild"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, stderr := requiredServiceBoot(t, tc.body, true)
			if tc.noSupervisor {
				t.Setenv("PATH", t.TempDir())
			}
			runSupervisorStep(t, b)
			if err := genFailuresError(b.e); err != nil {
				t.Fatalf("%s=1 must keep the boot going past a required service that did not "+
					"start (OQ-R8 (a)); genFailuresError returned: %v", paths.AllowUnreachableServicesEnv, err)
			}
			got := stderr.String()
			for _, want := range []string{paths.AllowUnreachableServicesEnv, "CONTINUING",
				"service 'wire-bridge'", "Nothing was repaired", tc.cause} {
				if !strings.Contains(got, want) {
					t.Errorf("the override notice does not say %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "Error:") {
				t.Errorf("a boot the hatch let through printed an error as if it refused:\n%s", got)
			}
		})
	}
}

// The hatch speaks only where it suppresses something (the witness's rule): a required service
// that reports ready under the hatch prints nothing at all.
func TestTheHatchSaysNothingOnABootWhoseRequiredServiceStarted(t *testing.T) {
	b, stderr := requiredServiceBoot(t, `printf 'ready wire-bridge\n' >&3`, true)
	runSupervisorStep(t, b)
	if err := genFailuresError(b.e); err != nil {
		t.Fatalf("a ready service refused the boot: %v", err)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("the hatch spoke on a boot it saved nothing on:\n%s", got)
	}
}

// AN ORPHAN IS NOT THE HATCH'S: daemons left running with no supervisor hold this jail's service
// ports, and continuing would start a second supervisor beside them. The refusal still names the
// pid, and no longer calls the step a config generator.
func TestTheHatchDoesNotReachTheOrphanRefusal(t *testing.T) {
	b, _ := requiredServiceBoot(t, `printf 'ready wire-bridge\n' >&3`, true)
	oldLegacy, oldFind := legacySupervisorPIDFiles, findJailDaemonOrphans
	legacySupervisorPIDFiles = []string{filepath.Join(t.TempDir(), "absent-legacy.pid")}
	findJailDaemonOrphans = func(string) ([]orphanedJailDaemon, error) {
		return []orphanedJailDaemon{{Name: "wire-bridge", PID: 8214}}, nil
	}
	t.Cleanup(func() { legacySupervisorPIDFiles, findJailDaemonOrphans = oldLegacy, oldFind })

	runSupervisorStep(t, b)
	err := genFailuresError(b.e)
	if err == nil {
		t.Fatal("an orphaned daemon must refuse the boot whatever the hatch says")
	}
	if !strings.Contains(err.Error(), "8214") || strings.Contains(err.Error(), "config generator") {
		t.Errorf("want the orphan refusal, naming the pid, and not as a config generator:\n%v", err)
	}
	if strings.Contains(err.Error(), paths.AllowUnreachableServicesEnv) {
		t.Errorf("the refusal offers a hatch that cannot get past it:\n%v", err)
	}
}

// ONE FAULT, ONE REPORT: the witness probes the bridge's endpoint too, and a bridge the readiness
// wait already reported has no endpoint file, so without a skip one held port was reported twice,
// in two vocabularies, with two hatch lines. Refused, the boot names the service once; let
// through, it prints one override notice.
func TestTheWitnessDoesNotReportARequiredServiceTheReadinessWaitReported(t *testing.T) {
	shrinkReachabilityBudget(t)
	unpublished := filepath.Join(servicesDir(t), "wire-bridge"+paths.ServiceEndpointExt)
	for _, hatch := range []bool{false, true} {
		b, stderr := requiredServiceBoot(t, `printf 'failed wire-bridge `+heldPortReason+`\n' >&3`, hatch)
		b.e.Vars[paths.ServiceEnvVarPrefix+"WIRE_BRIDGE"+paths.ServiceEnvVarSuffix] = unpublished
		b.e.Vars[paths.HostLoopbackEnvVar] = "shared"
		var logOnly bytes.Buffer
		b.e.LogOnly = &logOnly
		runSteps(b, []bootStep{mustBootStep(t, "start_jail_daemon_supervisor"),
			mustBootStep(t, "probe_service_reachability")})

		got := stderr.String()
		if n := strings.Count(got, paths.AllowUnreachableServicesEnv); n != 1 {
			t.Errorf("hatch=%v: want the hatch named once, by the readiness wait; named %d times:\n%s",
				hatch, n, got)
		}
		if strings.Contains(got, "no endpoint file") || strings.Contains(got, "unusable from inside") {
			t.Errorf("hatch=%v: the witness re-reported the service the readiness wait reported:\n%s",
				hatch, got)
		}
		if !strings.Contains(logOnly.String(), "wire-bridge") {
			t.Errorf("hatch=%v: boot.log does not record that the witness left wire-bridge to the "+
				"readiness wait:\n%s", hatch, logOnly.String())
		}
		err := genFailuresError(b.e)
		if hatch != (err == nil) {
			t.Errorf("hatch=%v: genFailuresError = %v", hatch, err)
		}
		if err != nil && strings.Count(err.Error(), "service 'wire-bridge'") != 1 {
			t.Errorf("the refusal must list wire-bridge exactly once:\n%v", err)
		}
	}
}

// THE WITNESS'S REFUSAL IS NOT A CONFIG GENERATOR EITHER: an unusable host service is listed as a
// service the jail cannot use, with the hatch, on the refusal's last line.
func TestTheWitnessRefusalIsListedAsAServiceNotAGenerator(t *testing.T) {
	shrinkReachabilityBudget(t)
	_, err := runProbe(t, brokenServiceVars(t, map[string]string{paths.HostLoopbackEnvVar: "requested"}))
	if err == nil {
		t.Fatal("the fixture is an unreachable service on a requested disposition; it must refuse")
	}
	if strings.Contains(err.Error(), "config generator") {
		t.Errorf("the witness's refusal still calls an unusable service a config generator:\n%v", err)
	}
	if !strings.Contains(err.Error(), paths.AllowUnreachableServicesEnv+"=1") {
		t.Errorf("the refusal's summary does not name the hatch:\n%v", err)
	}
}

// A generator failure beside a service refusal keeps both headings, and the hatch line is left
// off: setting the variable would not get the boot past the generator.
func TestTheRefusalOffersTheHatchOnlyWhenItWouldGetPastEverything(t *testing.T) {
	e := NewEnv(map[string]string{})
	e.refuseService("service 'wire-bridge' did not start: port 8214 is already taken", true)
	genStep(e, "generate_bashrc", func() error { return errTest("disk full") })
	err := genFailuresError(e)
	if err == nil {
		t.Fatal("two refusals returned nil")
	}
	msg := err.Error()
	for _, want := range []string{"service 'wire-bridge'", "1 boot step(s) failed", "generate_bashrc"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, paths.AllowUnreachableServicesEnv) {
		t.Errorf("the hatch is offered on a boot it cannot get past:\n%s", msg)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// The pack phrase reads the STAGED packs: the pack whose service the daemon runs, and the selected
// pack whose `needs` names it. A service no staged pack declares is named alone.
func TestTheRequiredServicePhraseNamesItsPackAndWhatNeedsIt(t *testing.T) {
	packs := capabilityPacks(t, "claude", "wire-bridge")
	got := requiredServicePhrase(packs, []string{"wire-bridge"})
	for _, want := range []string{"service 'wire-bridge'", `pack "wire-bridge"`, `"claude"`} {
		if !strings.Contains(got, want) {
			t.Errorf("phrase %q does not name %q", got, want)
		}
	}
	if got := requiredServicePhrase(nil, []string{"wire-bridge"}); got != "service 'wire-bridge'" {
		t.Errorf("with no staged packs the phrase is the service alone, got %q", got)
	}
	if got := requiredServicePhrase([]*packload.Pack{}, []string{"a", "b"}); got != "services 'a', 'b'" {
		t.Errorf("two services, got %q", got)
	}
}
