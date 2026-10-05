package macosuser

// serviceprobe_test.go pins the macos-user launch's host-service witness (serviceprobe.go): the
// plan carries a confined probe argv exactly when the session env carries a published endpoint,
// the env file says `shared` so the witness escalates, the orchestrator runs the stage after the
// jail daemons and before the agent, and it reads the stage's status the way the design rules
// (78 refuses, any other failure warns and launches).
//
// ⚠ NONE OF THIS HAS EXECUTED ON A MAC. What only a Mac can settle: that sandbox-exec admits the
// staged yolo's `internal probe-services` under the session profile, that the sandbox account's
// read on a granted endpoint really lets the dial through, that an UNGRANTED endpoint really
// comes back EACCES or EPERM (faultUnreadable) rather than something else, and that the plain
// sudo's prompt after a long provisioning stage is answerable on the launch's terminal
// (TestMacosUserServiceProbeRefusesAnEndpointTheSandboxCannotRead in integration/).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

const probeWS = "/Users/Shared/yolo/proj"

// brokerEndpointEnv is a launch env carrying one published host-service endpoint that no guest
// client reads, so a plan built from it runs the witness and stages no guest binary.
func brokerEndpointEnv() *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	env.Set("YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT", "/private/tmp/yolo-host-services-x/claude-oauth-broker.endpoint")
	return env
}

func probePlan(t *testing.T, env *jsonx.OrderedMap) RunPlan {
	t.Helper()
	return BuildRunPlan(probeWS, jsonx.NewOrderedMap(), []string{"claude"}, []string{"claude"},
		"/opt/yolo/bin/yolo", "", HomeOverlay{}, HostContext{}, env, mockDarwin(), nil)
}

// THE PROBE ARGV, EXACTLY: plain sudo as the sandbox account, the closed `env -i` list,
// sandbox-exec under THIS session's profile, the env-file reader over the session env file, and
// the staged yolo's probe verb last.
func TestBuildRunPlanEmitsTheConfinedProbeArgvForAPublishedEndpoint(t *testing.T) {
	plan := probePlan(t, brokerEndpointEnv())
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a well-formed probe plan fails its invariants:\n%s", strings.Join(problems, "\n"))
	}
	argv := plan.ProbeArgv
	if len(argv) == 0 {
		t.Fatal("a plan carrying a published endpoint runs no witness stage")
	}
	if got := strings.Join(argv[:4], " "); got != "sudo --user="+SandboxUser+" /usr/bin/env -i" {
		t.Errorf("the probe does not open with a PLAIN sudo as the sandbox account: %q", got)
	}
	for _, w := range argv {
		if w == "-n" || w == "--login" {
			t.Errorf("the probe's sudo carries %q; it runs in the foreground and must be able to prompt", w)
		}
	}
	if !containsArgPair(argv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		t.Errorf("the probe is not confined under the session's profile:\n%v", argv)
	}
	if plan.EnvFile == "" || !SandboxArgvReadsEnvFile(plan.EnvFile, argv) {
		t.Errorf("the probe does not read the session env file %q:\n%v", plan.EnvFile, argv)
	}
	tail := []string{plan.StagedYolo, "internal", ProbeServicesVerb}
	if got := argv[len(argv)-3:]; strings.Join(got, " ") != strings.Join(tail, " ") {
		t.Errorf("the probe does not end by exec'ing the staged yolo's probe verb: %v", got)
	}
	if len(SandboxArgvEnvProblems("probe", argv)) > 0 {
		t.Errorf("the probe argv carries a composed value: %v", SandboxArgvEnvProblems("probe", argv))
	}
}

// NO ENDPOINT, NO PROBE: a launch that started no host service pays nothing.
func TestBuildRunPlanRunsNoProbeWithoutAPublishedEndpoint(t *testing.T) {
	plan := probePlan(t, jsonx.NewOrderedMap())
	if len(plan.ProbeArgv) != 0 {
		t.Errorf("a plan with no published endpoint runs a witness: %v", plan.ProbeArgv)
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan with no endpoint fails its invariants:\n%s", strings.Join(problems, "\n"))
	}
}

// THE DISPOSITION IS `shared`, OVER EVERY LAYER: the witness escalates only on an exact value,
// so a session env without it, or with a layer's own spelling, would never refuse.
func TestEveryPlanSaysTheSandboxSharesTheLaunchersNetwork(t *testing.T) {
	env := brokerEndpointEnv()
	env.Set(paths.HostLoopbackEnvVar, paths.HostLoopbackUnknown) // a composed layer's attempt
	plan := probePlan(t, env)
	if !SandboxEnvFileSets(plan.EnvFileContent, paths.HostLoopbackEnvVar, paths.HostLoopbackShared) {
		t.Errorf("the session env file does not export %s=%s over the layers:\n%s",
			paths.HostLoopbackEnvVar, paths.HostLoopbackShared, plan.EnvFileContent)
	}
	if v, _ := sandboxEnvFileValue(plan.EnvFileContent, paths.HostLoopbackEnvVar); v != paths.HostLoopbackShared {
		t.Errorf("the last %s in the env file is %q", paths.HostLoopbackEnvVar, v)
	}
}

// PlanInvariants' witness rules each fail the plan they describe.
func TestPlanInvariantsCatchAMissingOrUnconfinedProbe(t *testing.T) {
	good := probePlan(t, brokerEndpointEnv())
	drop := func(argv []string, word string, n int) []string {
		var out []string
		for i := 0; i < len(argv); i++ {
			if argv[i] == word {
				i += n - 1
				continue
			}
			out = append(out, argv[i])
		}
		return out
	}
	for name, mutate := range map[string]func(*RunPlan){
		"no probe":     func(p *RunPlan) { p.ProbeArgv = nil },
		"unconfined":   func(p *RunPlan) { p.ProbeArgv = drop(p.ProbeArgv, "/usr/bin/sandbox-exec", 4) },
		"no env file":  func(p *RunPlan) { p.ProbeArgv = drop(p.ProbeArgv, sandboxEnvShell, 5) },
		"sudo -n":      func(p *RunPlan) { p.ProbeArgv = append([]string{"sudo", "-n"}, p.ProbeArgv[1:]...) },
		"another verb": func(p *RunPlan) { p.ProbeArgv[len(p.ProbeArgv)-1] = "footer" },
		"no disposition": func(p *RunPlan) {
			p.EnvFileContent = strings.ReplaceAll(p.EnvFileContent,
				exportLine(paths.HostLoopbackEnvVar, paths.HostLoopbackShared), "")
		},
		"a probe with nothing to probe": func(p *RunPlan) {
			p.EnvFileContent = strings.ReplaceAll(p.EnvFileContent, "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_ENDPOINT", "UNRELATED")
		},
	} {
		p := good
		p.ProbeArgv = append([]string(nil), good.ProbeArgv...)
		mutate(&p)
		if len(serviceProbeInvariants(p)) == 0 || len(PlanInvariants(p)) == 0 {
			t.Errorf("%s: PlanInvariants accepted the plan", name)
		}
	}
}

// probeLaunch runs one launch whose session env carries a published endpoint (and, when daemons,
// the guest's adapter), with the probe stage's status probeRC. It returns the record, the output and
// the launch's status.
func probeLaunch(t *testing.T, probeRC int, daemons bool) ([]string, string, int) {
	t.Helper()
	var rec []string
	d := mockDeps(&rec)
	run := d.Run
	d.Run = func(argv []string) int {
		got := run(argv)
		if containsArgRun(argv, []string{"internal", ProbeServicesVerb}) {
			return probeRC
		}
		return got
	}
	o := newOpts(probeWS)
	o.SandboxEnv = brokerEndpointEnv()
	if daemons {
		fakeSupervisor(&d, &rec, probeWS, "", readyLine, "", false)
		d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
		o.JailDaemons = openAIAdapterDaemons("")
	}
	var buf bytes.Buffer
	d.Out = &buf
	status := RunMacosUser(d, o)
	return rec, buf.String(), status
}

func probeRecIndex(rec []string, match func(string) bool) int {
	for i, r := range rec {
		if match(r) {
			return i
		}
	}
	return -1
}

// THE STAGE RUNS AFTER THE JAIL DAEMONS START AND BEFORE THE AGENT. Deleting step 3.7 from
// RunMacosUser fails this.
func TestTheOrchestratorRunsTheProbeAfterTheDaemonsAndBeforeTheAgent(t *testing.T) {
	rec, out, rc := probeLaunch(t, 0, true)
	if rc != 42 {
		t.Fatalf("rc = %d, want the agent's\n%s", rc, out)
	}
	start := probeRecIndex(rec, func(r string) bool { return strings.HasPrefix(r, "start:") })
	probe := probeRecIndex(rec, func(r string) bool {
		return strings.HasPrefix(r, "run:sudo --user="+SandboxUser+" ") && strings.HasSuffix(r, " internal "+ProbeServicesVerb)
	})
	proxy := probeRecIndex(rec, func(r string) bool { return strings.HasPrefix(r, "proxy:") })
	if start < 0 || probe < 0 || proxy < 0 || !(start < probe && probe < proxy) {
		t.Fatalf("want supervisor start < probe < agent; got %d %d %d:\n%s",
			start, probe, proxy, strings.Join(rec, "\n"))
	}
	if !strings.Contains(rec[probe], "/usr/bin/sandbox-exec -f ") {
		t.Errorf("the probe the launch ran is not confined: %s", rec[probe])
	}
}

// 78 IS A REFUSAL: the launch stops with 1, the agent never runs, and every deferred teardown
// still runs — the supervisor is stopped and both env files are swept.
func TestARefusingProbeStopsTheLaunchAndStillSweepsTheSession(t *testing.T) {
	rec, out, rc := probeLaunch(t, provision.RefusedStatus, true)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out)
	}
	if probeRecIndex(rec, func(r string) bool { return strings.HasPrefix(r, "proxy:") }) >= 0 {
		t.Errorf("the agent ran after the witness refused:\n%s", strings.Join(rec, "\n"))
	}
	if !strings.Contains(out, "The sandbox cannot use a host service this launch enabled") {
		t.Errorf("the refusal is not said:\n%s", out)
	}
	key := launchedSessionKey(t, rec, probeWS)
	joined := strings.Join(rec, "\n")
	for _, f := range []string{SandboxEnvFile(key, ""), SandboxDaemonEnvFile(key, "")} {
		if !strings.Contains(joined, "run:sudo "+rmBin+" -f "+f) {
			t.Errorf("%s is not swept after the refusal:\n%s", f, joined)
		}
	}
	if !strings.Contains(joined, "\nstop\n") {
		t.Errorf("the supervisor is not stopped after the refusal:\n%s", joined)
	}
}

// ANY OTHER FAILURE IS A STAGE THAT NEVER ANSWERED: warn, and launch.
func TestAProbeThatCannotRunWarnsAndLaunches(t *testing.T) {
	rec, out, rc := probeLaunch(t, 1, false)
	if rc != 42 {
		t.Fatalf("rc = %d, want the agent's\n%s", rc, out)
	}
	if probeRecIndex(rec, func(r string) bool { return strings.HasPrefix(r, "proxy:") }) < 0 {
		t.Error("the agent did not run")
	}
	for _, want := range []string{"could not run", "claude-oauth-broker", "yolo check"} {
		if !strings.Contains(out, want) {
			t.Errorf("the could-not-run warning does not say %q:\n%s", want, out)
		}
	}
}

// THE HATCH CROSSES INTO THE SANDBOX when the user set it on the host, and only then.
func TestTheReachabilityHatchCrossesIntoTheSandboxWhenSet(t *testing.T) {
	d := mockDeps(nil)
	if _, ok := MacosSandboxEnv(d, jsonx.NewOrderedMap()).Get(paths.AllowUnreachableServicesEnv); ok {
		t.Error("the hatch crossed although the host has none set")
	}
	d.Getenv = func(k string) string {
		if k == paths.AllowUnreachableServicesEnv {
			return "1"
		}
		return ""
	}
	o := newOpts(probeWS)
	o.SandboxEnv = brokerEndpointEnv()
	plan := buildPlan(d, o, mockDarwin())
	if !SandboxEnvFileSets(plan.EnvFileContent, paths.AllowUnreachableServicesEnv, "1") {
		t.Errorf("the hatch set on the host does not reach the session env file:\n%s", plan.EnvFileContent)
	}
}

// A DRY RUN NAMES THE WITNESS EITHER WAY.
func TestTheDryRunNamesTheWitness(t *testing.T) {
	for _, tc := range []struct {
		env  *jsonx.OrderedMap
		want string
	}{
		{brokerEndpointEnv(), "internal " + ProbeServicesVerb},
		{nil, "skipped — this launch carries no published host-service endpoint"},
	} {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		o := newOpts(probeWS)
		o.SandboxEnv = tc.env
		o.DryRun = true
		if rc := RunMacosUser(d, o); rc != 0 {
			t.Fatalf("dry run rc = %d\n%s", rc, buf.String())
		}
		if !strings.Contains(buf.String(), "host-service witness") || !strings.Contains(buf.String(), tc.want) {
			t.Errorf("the dry run does not name the witness (%q):\n%s", tc.want, buf.String())
		}
	}
}
