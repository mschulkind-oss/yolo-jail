package macosuser

// serviceprobe.go is the macos-user launch's HOST-SERVICE WITNESS: the container boot's last
// step (internal/entrypoint's ProbeServiceReachability), run here as a stage of its own, since
// this backend has no boot to run it in.
//
// # Why it is a confined stage and not a host-side check
//
// The question is whether the SANDBOX can use each host service the launch enabled, and only
// the sandbox account can answer it. A host-side dial runs as the invoking user, who wrote the
// endpoint file and can read it whatever its ACL says, under no Seatbelt profile; it would pass
// in exactly the state this exists for. So the stage runs the way the agent will: as
// SandboxUser, under the session's profile, with the session env file read by the same reader
// (ExecWithEnvFile), executing the staged yolo's hidden `yolo internal probe-services`
// (internal/cli/probeservices.go). A green here means the agent's own client gets through.
//
// # What it can catch on this backend
//
// The network hop the container witness was written for is absent: the sandbox shares the
// Mac's network stack (internal/cli/run's sharesLauncherNetns), and every host daemon
// advertises 127.0.0.1. What is left is the rest of OQ-R4's fault classes, plus one the
// container never meets: an endpoint the sandbox account may not READ, because the launch's
// cross-uid ACL grant (EndpointGrantCommands) did not take. That is the witness's
// faultUnreadable class, and it is the likeliest failure on a Mac.
//
// # FATAL, as everywhere
//
// The launch writes YOLO_HOST_LOOPBACK=shared into the session env (BuildRunPlanWithDaemons),
// and `shared` escalates (docs/reference/loopback-tls-reachability.md, OQ-R5): an enabled host
// service the sandbox cannot use refuses the launch, and YOLO_ALLOW_UNREACHABLE_SERVICES
// (forwarded by MacosSandboxEnv) is the hatch the refusal names.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// ProbeServicesVerb is the hidden `yolo internal` verb the probe stage execs. One spelling for
// the argv here and the dispatch in internal/cli/internal.go.
const ProbeServicesVerb = "probe-services"

// ProbeServicesArgv builds the witness stage's argv: `sudo --user=<sb> /usr/bin/env -i <the
// closed identity list> /usr/bin/sandbox-exec -f <profile> -- <env-file reader> <staged yolo>
// internal probe-services`.
//
// LaunchArgv's shape for the reason JailDaemonArgv has it, with ONE DIFFERENCE from that argv:
// PLAIN sudo, never `-n`. The supervisor runs in the background beside a terminal the agent is
// about to own, so it must fail rather than prompt; this stage runs in the FOREGROUND before
// the agent, on the launch's own terminal, so a prompt is answerable — and the credential cache
// the staging steps warmed may have expired by now, a provisioning stage being able to take
// longer than sudo's timeout. A `-n` there would turn a slow first launch into a refusal.
//
// stagedYolo is the root-owned copy the bootstrap self-execs (StagedYoloPath): the host's own
// binary may sit where the sandbox account cannot read it.
func ProbeServicesArgv(stagedYolo, profilePath, envFile, user, home string, pathPrefix []string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	out := []string{
		"sudo",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, sandboxEnvPairs(home, user, SandboxPath(home, pathPrefix), envFile)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out, ExecWithEnvFile(envFile, []string{stagedYolo, "internal", ProbeServicesVerb})...)
	return out
}

// carriesServiceEndpoint reports whether env names at least one published host-service endpoint
// (isServiceEndpointVar, with a value): the condition for a probe stage at all. The env is the
// manifest, for endpointGrantCommands' reason: the run pipeline set one variable per service
// that published.
func carriesServiceEndpoint(env *jsonx.OrderedMap) bool {
	if env == nil {
		return false
	}
	for _, k := range env.Keys() {
		if !isServiceEndpointVar(k) {
			continue
		}
		if v, _ := env.Get(k); asStr(v) != "" {
			return true
		}
	}
	return false
}

// serviceProbeInvariants is PlanInvariants' rule for the witness stage. A plan whose session env
// carries a published endpoint must run the probe, and the probe must run the way the agent
// will: confined under THIS session's profile, reading the session env file, executing the
// staged yolo's probe verb, carrying no composed value on its argv, with a plain sudo. And the
// env file must say `shared`, or the witness reads the disposition as unattributed and no
// failure it finds can refuse anything.
func serviceProbeInvariants(plan RunPlan) []string {
	var problems []string
	carries := false
	for _, key := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if !isServiceEndpointVar(key) {
			continue
		}
		if v, ok := sandboxEnvFileValue(plan.EnvFileContent, key); ok && v != "" {
			carries = true
		}
	}
	argv := plan.ProbeArgv
	if !carries {
		if len(argv) > 0 {
			problems = append(problems, "the plan runs the host-service witness although the "+
				"session env carries no published endpoint; it would probe nothing")
		}
		return problems
	}
	if len(argv) == 0 {
		return append(problems, "the session env carries a published host-service endpoint "+
			"and the plan runs no witness stage; an endpoint the sandbox cannot use would reach "+
			"the agent unannounced (docs/reference/loopback-tls-reachability.md, OQ-R2)")
	}
	if !containsArgPair(argv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		problems = append(problems, "the host-service witness does not run under `sandbox-exec -f "+
			plan.ProfilePath+"`; unconfined, it would pass for an endpoint the agent cannot open")
	}
	if !SandboxArgvReadsEnvFile(plan.EnvFile, argv) {
		problems = append(problems, "the host-service witness never reads the session env file ("+
			plan.EnvFile+"); it would see none of the endpoints it is there to probe")
	}
	if !containsArgRun(argv, []string{plan.StagedYolo, "internal", ProbeServicesVerb}) {
		problems = append(problems, "the host-service witness does not exec `"+plan.StagedYolo+
			" internal "+ProbeServicesVerb+"`")
	}
	for _, w := range argv {
		if w == "/usr/bin/env" {
			break // the end of sudo's own flags
		}
		if w == "-n" || w == "--non-interactive" || w == "--login" || w == "-i" {
			problems = append(problems, "the host-service witness's sudo carries `"+w+"`; it runs in "+
				"the foreground and must be able to prompt (the cache may have expired during a long "+
				"provisioning stage), and a login sudo rewrites the command it is given")
		}
	}
	problems = append(problems, SandboxArgvEnvProblems("host-service witness", argv)...)
	if !SandboxEnvFileSets(plan.EnvFileContent, paths.HostLoopbackEnvVar, paths.HostLoopbackShared) {
		problems = append(problems, "the session env file does not export "+paths.HostLoopbackEnvVar+
			"="+paths.HostLoopbackShared+"; the witness would read the disposition as unattributed "+
			"and never refuse a launch whose host service the sandbox cannot use (OQ-R5)")
	}
	return problems
}

// probeOutcome is what runServiceProbe tells the launch to do.
type probeOutcome int

const (
	probePassed probeOutcome = iota
	probeRefused
	probeNotRun
)

// runServiceProbe runs the witness stage and reads its status:
//
//   - 0: every enabled service is usable, or the hatch let an unusable one through (it said so
//     itself). The launch goes on.
//   - provision.RefusedStatus: the witness REFUSED the launch, and its message, naming each
//     service and the hatch, is already on the terminal. The launch stops.
//   - anything else: the stage never answered — sudo refusing authorization, sandbox-exec
//     rejecting the profile, the env-file reader failing — so nothing was learned about the
//     services. Warn and go on, provisioning's rule for an exec-layer failure nobody chose:
//     a refusal needs a finding, and there is none.
func runServiceProbe(deps Deps, out printer, plan RunPlan) probeOutcome {
	switch rc := deps.Run(plan.ProbeArgv); rc {
	case 0:
		return probePassed
	case provision.RefusedStatus:
		out.print("[bold red]The sandbox cannot use a host service this launch enabled; the " +
			"launch stops here.[/bold red] The lines above name each service and why.")
		return probeRefused
	default:
		out.printf("[yellow]The sandbox-side check of this launch's host services could not run "+
			"(status %d)[/yellow] — sudo, sandbox-exec or the session env file, not the services "+
			"themselves. Launching anyway; if a client of %s fails, `yolo check` on this Mac "+
			"reports each service.", rc, strings.Join(probedServiceNames(plan), ", "))
		return probeNotRun
	}
}

// probedServiceNames is the services the witness probes, named by their endpoint variables'
// slugs in lower case, for the could-not-run warning.
func probedServiceNames(plan RunPlan) []string {
	var out []string
	for _, key := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if !isServiceEndpointVar(key) {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(key, paths.ServiceEnvVarPrefix), paths.ServiceEnvVarSuffix)
		out = append(out, strings.ReplaceAll(strings.ToLower(slug), "_", "-"))
	}
	return out
}
