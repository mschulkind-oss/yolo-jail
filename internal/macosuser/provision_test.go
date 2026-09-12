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
// THE LAUNCH ARGV NO LONGER CARRIES IT EITHER, since 2026-09-12. It did until then, on the
// belief that the login rc files that flag runs are what re-prepend PATH after macOS
// path_helper (OQ-1). They are not — `/usr/bin/env -i` is the very next word and wipes the
// environment that shell built, and the re-prepend OQ-1 measures happens downstream in the
// user's own `bash -lc`, inside the sandbox. Both measured on hardware the same day: the
// flag cost item 6 its measurement (a `$b` probe printed nine blank lines), and removing it
// left item 3's `fzf` still resolving into the store profile ahead of Homebrew's.
func TestNeitherArgvForwardsThroughSudoLogin(t *testing.T) {
	plan := provisionPlan(t)
	for name, argv := range map[string][]string{
		"stage":  plan.ProvisionArgv,
		"launch": plan.LaunchArgv,
	} {
		if containsArg(argv, "--login") {
			t.Errorf("the %s argv carries sudo --login, which rewrites the command it "+
				"forwards (drops newlines, expands $vars against an empty env) and still "+
				"exits 0:\n%s", name, strings.Join(argv, " "))
		}
	}
	// And the invariant must SAY so for EACH of them, or a future edit reintroduces the flag
	// with the whole suite green. Two argvs, two checks: the stage's has always been pinned,
	// and the launch's is what shipped with the fix.
	for name, mutate := range map[string]func(RunPlan) RunPlan{
		"stage": func(p RunPlan) RunPlan {
			p.ProvisionArgv = append([]string{"sudo", "--login"}, p.ProvisionArgv[1:]...)
			return p
		},
		"launch": func(p RunPlan) RunPlan {
			p.LaunchArgv = append([]string{"sudo", "--login"}, p.LaunchArgv[1:]...)
			return p
		},
	} {
		if !hasProblem(PlanInvariants(mutate(plan)), "--login") {
			t.Errorf("PlanInvariants accepts a %s argv with --login", name)
		}
	}
}

// A COMMAND CROSSES INTO THE SANDBOX AS ONE ARGUMENT, AND THAT IS THE WHOLE POINT OF THE
// FIX ABOVE. The two shapes below are the two that were measured mangled on hardware — a
// newline (which arrived as a `\`-continuation and was removed) and a `$var` (which an
// intermediate login shell expanded against an empty environment). Both are ordinary in a
// forwarded command, and `podman exec` carries both untouched on every other backend, so
// this is the backend-parity assertion: the argv yolo builds still HOLDS the text verbatim.
func TestLaunchArgvCarriesAForwardedCommandVerbatim(t *testing.T) {
	const cmd = "echo A\necho B; X=inner; echo got=$X"
	argv := LaunchArgv([]string{"bash", "-lc", cmd}, "/var/yolo-jail/p.sb",
		jsonx.NewOrderedMap(), "/Users/Shared/yolo/ws", "", "", nil)
	last := argv[len(argv)-1]
	if !strings.Contains(last, cmd) {
		t.Errorf("the forwarded command is not in the final argument intact.\ngot:  %q\nwant it to contain: %q",
			last, cmd)
	}
	// One argument, not several: a command split across argv elements is a command sudo or
	// the shell gets to re-join on its own terms, which is how the mangling started.
	for i, a := range argv[:len(argv)-1] {
		if strings.Contains(a, "echo B") {
			t.Errorf("argv[%d] = %q also carries part of the command; it must live in one "+
				"argument", i, a)
		}
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

// ⚠ THE INVERSION THIS BACKEND SHIPPED WITH, and the rule it broke. §4 of the design doc
// states it in bold — *"a failing stage must not abort the launch"* — and the orchestrator
// used to return 1 on EVERY non-zero status from the stage, on a comment that asserted as
// fact that "the exit code says only whether the human asked it to".
//
// It does not. `deps.Run` is also non-zero when the stage NEVER RAN: sudo refusing
// authorization, sandbox-exec rejecting the profile, /bin/bash missing — every one of them
// on the design doc's "what a Mac has to settle" list. On those paths a workspace that merely
// DECLARES mise_tools could not launch at all, and the message blamed the user.
//
// The two tests below are the two branches, and they are written against the marker rather
// than against a returncode because the marker is the only thing that distinguishes them:
// the script writes it, so it exists if and only if the script ran.

// stageFailureDeps returns deps whose provisioning stage exits non-zero, with the startup
// log for `ws` containing `logBody` afterwards ("" = the stage wrote nothing at all).
func stageFailureDeps(t *testing.T, rec *[]string, ws, logBody string) Deps {
	t.Helper()
	d := mockDeps(rec)
	log := provision.StartupLog(ws)
	cleared := false
	inner := d.Run
	d.Run = func(argv []string) int {
		rc := inner(argv)
		if strings.Contains(strings.Join(argv, " "), "sandbox-exec") {
			return 7 // the stage body's own code, forwarded by the script's `exit "$_prc"`
		}
		return rc
	}
	d.RemoveFile = func(p string) bool {
		if p == log {
			cleared = true
		}
		return true
	}
	// An absent log is ("", true) — knowledge, not a failure — exactly as readFileReal
	// reports it; only an unreadable one is ("", false), which no branch here produces.
	d.ReadFile = func(p string) (string, bool) {
		if p != log || !cleared {
			return "", false
		}
		return logBody, true
	}
	return d
}

// Branch one: the stage RAN, its body failed, and a human answered `n`. The marker is in
// the log, the veto is explicit, and the launch must stop.
func TestAnExplicitVetoStillAbortsTheLaunch(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	var rec []string
	d := stageFailureDeps(t, &rec, ws,
		"=== yolo provisioning ===\n"+provision.FailedMarker+" (exit 7)\n")
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts(ws)
	opts.Config = provisionCfg()

	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Fatalf("rc = %d, want 1 — a human answering `n` to the stage's prompt must stop "+
			"the launch, which is the one thing a non-zero status from the script means\n%s",
			rc, buf.String())
	}
	for _, line := range rec {
		if strings.HasPrefix(line, "proxy:") {
			t.Errorf("the agent launched after an explicit veto:\n%s", line)
		}
	}
}

// Branch two, and the regression: the stage NEVER RAN, so there is no marker. §4 says the
// launch continues. Reverting runProvisionStage to `if deps.Run(...) != 0 { return 1 }`
// fails here — the agent never launches and rc is 1.
func TestAStageThatNeverRanDoesNotAbortTheLaunch(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	var rec []string
	d := stageFailureDeps(t, &rec, ws, "") // nothing written: sudo/sandbox-exec/bash failed
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts(ws)
	opts.Config = provisionCfg()

	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("rc = %d, want 42 (the agent's own exit) — §4: a failing stage must not "+
			"abort the launch, and a stage that never ran is not even a failing one\n%s",
			rc, buf.String())
	}
	launched := false
	for _, line := range rec {
		if strings.HasPrefix(line, "proxy:") {
			launched = true
		}
	}
	if !launched {
		t.Errorf("the agent never launched:\n%s", strings.Join(rec, "\n"))
	}
	// The silence is the other half of the defect: the user must be told the tools are
	// absent, since nothing else on this path will say so.
	if !strings.Contains(buf.String(), "never ran") {
		t.Errorf("the launch continued silently — the declared tools are absent and "+
			"nothing said so:\n%s", buf.String())
	}
}

// The conservative direction, stated as a test so a later "simplification" cannot quietly
// flip it: when the log cannot be cleared or cannot be read, the stage's failure is
// UNCLASSIFIABLE, and the launch must honor a possible veto rather than assume there was
// none. Aborting a launch that should have continued is recoverable; ignoring a human's
// explicit `n` is not.
func TestAnUnreadableLogIsTreatedAsAVeto(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	var rec []string
	d := stageFailureDeps(t, &rec, ws, "")
	d.RemoveFile = func(string) bool { return false } // e.g. the sidecar is not writable
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts(ws)
	opts.Config = provisionCfg()

	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Fatalf("rc = %d, want 1 — an unclassifiable stage failure must not be read as "+
			"'the human said nothing'\n%s", rc, buf.String())
	}
}
