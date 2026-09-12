package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// provisionCfg is a config that gives the stage something to do.
func provisionCfg() *jsonx.OrderedMap {
	mise := jsonx.NewOrderedMap()
	mise.Set("neovim", "nightly")
	cfg := jsonx.NewOrderedMap()
	cfg.Set("mise_tools", mise)
	return cfg
}

// THE CALL-SITE TEST. Every other test in this file examines the plan, and a plan is a
// description — deleting the orchestrator's `deps.Run(plan.ProvisionArgv)` leaves all of
// them green while the backend installs nothing, which is exactly the shape AGENTS.md says
// this repo has shipped five times. This one runs the orchestrator and asserts the stage
// was EXECUTED, and that it ran in the one position that is correct: after the bootstrap
// (which generates the script it execs) and before the agent (which needs the tools).
func TestProvisioningStageRunsBetweenTheBootstrapAndTheAgent(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = provisionCfg()

	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("rc = %d, want 42 (proxy exit)\n%s", rc, buf.String())
	}

	bootstrap, stage, launch := -1, -1, -1
	for i, line := range rec {
		switch {
		case strings.Contains(line, "darwin-bootstrap"):
			bootstrap = i
		case strings.HasPrefix(line, "run:") && strings.Contains(line, "sandbox-exec"):
			stage = i
		case strings.HasPrefix(line, "proxy:"):
			launch = i
		}
	}
	if stage < 0 {
		t.Fatalf("the provisioning stage never ran — a config declaring mise_tools got a "+
			"sandbox with no tools in it:\n%s", strings.Join(rec, "\n"))
	}
	if bootstrap < 0 || launch < 0 {
		t.Fatalf("bootstrap or launch missing:\n%s", strings.Join(rec, "\n"))
	}
	if !(bootstrap < stage && stage < launch) {
		t.Errorf("stage ran out of order (bootstrap=%d stage=%d launch=%d). It must follow "+
			"the bootstrap, which generates the script it execs, and precede the agent, "+
			"which is what the tools are for.\n%s",
			bootstrap, stage, launch, strings.Join(rec, "\n"))
	}
}

// The SKIP half, and it is the reason the rule is a rule rather than a mood: a workspace
// that declares no tools must not pay a third privileged step, a sudo prompt, or a
// `mise install` against an empty config.
func TestNoStageWhenNothingIsDeclared(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
		t.Fatalf("rc = %d, want 42\n%s", rc, buf.String())
	}
	for _, line := range rec {
		if strings.HasPrefix(line, "run:") && strings.Contains(line, "sandbox-exec") {
			t.Errorf("a bare launch ran a provisioning stage:\n%s", line)
		}
	}
}

// ⚠ THE MEASURED DEFECT THIS STAGE HAD TO ROUTE AROUND. `sudo --login` does not execve
// its argv: it concatenates and backslash-escapes the command, leaving `$` for an
// intermediate login shell to expand, and NEITHER failure is loud — the wrong command
// runs and exits 0. The stage script is full of `$_prc`, `${PIPESTATUS[0]}` and
// `$(date …)`, so a stage forwarded through that flag would provision nothing and report
// success.
//
// The LAUNCH argv keeps the flag deliberately (the login rc files are what re-prepend
// PATH after macOS path_helper, the acceptance bar OQ-1 passed on hardware), so this test
// asserts the two argvs differ ON PURPOSE rather than that the flag is gone everywhere —
// a reader who "fixes the inconsistency" by adding it back is the failure being guarded.
func TestProvisionArgvDoesNotForwardThroughSudoLogin(t *testing.T) {
	plan := provisionPlan(t)
	for _, a := range plan.ProvisionArgv {
		if a == "--login" {
			t.Fatalf("the stage argv carries sudo --login:\n%s",
				strings.Join(plan.ProvisionArgv, " "))
		}
	}
	if !containsArg(plan.LaunchArgv, "--login") {
		t.Error("the launch argv lost --login; the login rc files are what re-prepend PATH " +
			"after macOS path_helper (OQ-1), so dropping it there is a separate change " +
			"with its own measurement — not a consequence of this one")
	}
	// And the invariant must SAY so, or a future edit reintroduces the flag with the
	// whole suite green.
	broken := plan
	broken.ProvisionArgv = append([]string{"sudo", "--login"}, plan.ProvisionArgv[1:]...)
	if !hasProblem(PlanInvariants(broken), "--login") {
		t.Error("PlanInvariants accepts a stage argv with --login")
	}
}

// The stage runs VENDOR code — npm postinstall hooks, mise plugins — which the container
// runs inside its jail. The bootstrap's unconfined argv is tolerable because it runs
// yolo's own code against a root-owned tree; this one is not.
func TestProvisionArgvIsConfinedUnderThisSessionsProfile(t *testing.T) {
	plan := provisionPlan(t)
	if !containsArg(plan.ProvisionArgv, "/usr/bin/sandbox-exec") {
		t.Fatalf("the stage is not confined:\n%s", strings.Join(plan.ProvisionArgv, " "))
	}
	if !containsArg(plan.ProvisionArgv, plan.ProfilePath) {
		t.Errorf("the stage does not name this session's profile %s:\n%s",
			plan.ProfilePath, strings.Join(plan.ProvisionArgv, " "))
	}
	// The mutation: drop the confinement and the invariant must fire. Without this the
	// assertion above is a property of today's code rather than a rule.
	broken := plan
	broken.ProvisionArgv = []string{"sudo", "--user=_yolojail", "/bin/bash", "-c", "true"}
	if !hasProblem(PlanInvariants(broken), "sandbox-exec") {
		t.Error("PlanInvariants accepts an unconfined provisioning stage")
	}
}

// THE TWO ENDS OF THE STAGE, checked against the paths their OTHER halves use:
// the script the darwin bootstrap generates, and the log the briefing reads.
func TestStageExecsTheGeneratedScriptAndLogsWhereTheBriefingLooks(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	plan := provisionPlan(t)

	// The generator resolves the script through the sidecar it is handed; the stage
	// resolves it through the workspace. They must land on one path.
	want := entrypoint.DarwinBootstrapScriptPath(paths.WorkspaceHomeState(ws))
	if plan.ProvisionScriptPath != want {
		t.Errorf("stage execs %s, bootstrap writes %s", plan.ProvisionScriptPath, want)
	}
	if !argvMentions(plan.ProvisionArgv, want) {
		t.Errorf("the stage argv never names the generated script:\n%s",
			strings.Join(plan.ProvisionArgv, " "))
	}
	// ⚠ AND NOT IN THE SHARED ACCOUNT HOME. The script carries this workspace's config;
	// /Users/_yolojail is shared by every workspace on the machine, so a home-rooted copy
	// would be the cross-workspace write-write race the home split exists to end,
	// re-introduced by this change rather than inherited.
	if strings.Contains(plan.ProvisionScriptPath, SandboxHome()+"/") {
		t.Errorf("the generated script lives in the shared account home: %s",
			plan.ProvisionScriptPath)
	}

	// The failure record has to land where jailcontent.ReadProvisioningFailed reads it.
	// On the container a bind makes ~/… and <ws>/.yolo/… one file; here nothing does.
	if !argvMentions(plan.ProvisionArgv, provision.StartupLog(ws)) {
		t.Errorf("the stage does not write %s, so a failed provision would never reach the "+
			"agent's briefing:\n%s", provision.StartupLog(ws),
			strings.Join(plan.ProvisionArgv, " "))
	}
	if !argvMentions(plan.ProvisionArgv, provision.FailedMarker) {
		t.Errorf("the stage never emits %q, which is the literal the briefing greps for",
			provision.FailedMarker)
	}
}

// The stage must bypass the blocked-tool shims. The generated bootstrap script uses
// `find` and `grep`, and a jail that selects the guardrails pack refuses both with exit
// 127 — so without the bypass the stage dies on its first npm arm, in a way that reads as
// an npm failure.
func TestStageBypassesTheBlockedToolShims(t *testing.T) {
	plan := provisionPlan(t)
	if !containsArg(plan.ProvisionArgv, "YOLO_BYPASS_SHIMS=1") {
		t.Errorf("the stage does not bypass the blockers:\n%s",
			strings.Join(plan.ProvisionArgv, " "))
	}
	// And the AGENT must not inherit it: the blockers exist for the agent.
	for _, a := range plan.LaunchArgv {
		if a == "YOLO_BYPASS_SHIMS=1" {
			t.Error("the agent launch bypasses the blocked-tool shims")
		}
	}
}

// The stage installs INTO the prefixes the agent resolves through. Composing its
// environment separately would let it install into a prefix nothing later looks in — and
// would drift one key at a time, silently.
func TestStageAndAgentShareOneEnvironment(t *testing.T) {
	plan := provisionPlan(t)
	for _, want := range []string{
		"HOME=" + SandboxHome(),
		"MISE_DATA_DIR=" + SandboxMiseData(SandboxHome()),
	} {
		if !containsArg(plan.ProvisionArgv, want) {
			t.Errorf("stage env missing %s:\n%s", want, strings.Join(plan.ProvisionArgv, " "))
		}
		if !containsArg(plan.LaunchArgv, want) {
			t.Errorf("launch env missing %s", want)
		}
	}
}

// The four steps this backend takes, and the two it does not. Both omissions are
// decisions with stated reasons (an inert grant it cannot compute; a Linux-absolute
// script), so both are pinned — an omission nothing asserts is indistinguishable from a
// step someone forgot.
func TestStageTakesFourOfTheSixSteps(t *testing.T) {
	body := ProvisionSetup("/ws/.yolo/home/yolo-bootstrap.sh")
	for _, want := range []string{provision.StepMiseInstall, provision.StepAnnounceBootstrap} {
		if !strings.Contains(body, want) {
			t.Errorf("stage body is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "YOLO_STORE_PRUNE_OK") {
		t.Error("the stage carries the store prune, which this backend can never enable: " +
			"the grant comes from proving no other jail is live, and nothing here does")
	}
	if strings.Contains(body, "venv-precreate") {
		t.Error("the stage carries the venv-precreate script, whose body is Linux-absolute " +
			"(/workspace, /bin/python3) and would exit 0 having done nothing on every launch")
	}
	// It reaches the script by ABSOLUTE path, never through the container's bind name.
	if strings.Contains(body, "~/.yolo-bootstrap.sh") {
		t.Error("the stage execs ~/.yolo-bootstrap.sh, which is the CONTAINER's bind " +
			"destination; this backend has no bind and no file at that path")
	}
}

// provisionPlan builds a plan whose config asks for the stage.
func provisionPlan(t *testing.T) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/yolo/proj", provisionCfg(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo-jail/bin/yolo", "", "", jsonx.NewOrderedMap(),
		mockDarwin(), nil)
}

// hasProblem reports whether any invariant violation mentions sub.
func hasProblem(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// THE LOCK IS HELD ACROSS THE PRIVILEGED STEPS AND RELEASED BEFORE THE AGENT, and both
// halves are the test. Two launches in one workspace really do run two provisioning
// stages here — this backend has no attach — against one npm prefix and one mise store;
// the container serialises the same window with the same lock and then attaches to
// whichever jail won.
//
// The release half matters as much: holding it across the agent would make a second
// terminal in the same workspace block until the first session ENDED, which is a
// serialisation no backend has and which would read as a hang.
func TestTheWorkspaceLockCoversTheStageAndNotTheAgent(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	d.LockWorkspace = func(ws, cname string) func() {
		rec = append(rec, "lock:"+ws+" "+cname)
		return func() { rec = append(rec, "unlock") }
	}
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = provisionCfg()
	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("rc = %d, want 42\n%s", rc, buf.String())
	}

	lock, stage, unlock, launch := -1, -1, -1, -1
	for i, line := range rec {
		switch {
		case strings.HasPrefix(line, "lock:"):
			lock = i
		case strings.HasPrefix(line, "run:") && strings.Contains(line, "sandbox-exec"):
			stage = i
		case line == "unlock" && unlock < 0:
			unlock = i
		case strings.HasPrefix(line, "proxy:"):
			launch = i
		}
	}
	if lock < 0 {
		t.Fatalf("the launch never took the workspace lock:\n%s", strings.Join(rec, "\n"))
	}
	if !(lock < stage && stage < unlock && unlock < launch) {
		t.Errorf("lock=%d stage=%d unlock=%d launch=%d — the lock must be taken before the "+
			"privileged steps, cover the stage, and be released before the agent\n%s",
			lock, stage, unlock, launch, strings.Join(rec, "\n"))
	}
}

// A machine that cannot lock still launches. A workspace lock is a courtesy against a
// self-inflicted race, not a safety property worth refusing over — the same choice the
// container's own acquire makes, and the reason the seam may return nil.
func TestAnUnavailableLockDoesNotRefuseTheLaunch(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	d.LockWorkspace = func(string, string) func() { return nil }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = provisionCfg()
	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("rc = %d, want 42 — a lock that could not be taken refused the launch\n%s",
			rc, buf.String())
	}
}
