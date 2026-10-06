package macosuser

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
		"", "/Users/Shared/yolo/ws", "", "", nil)
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

// AR-L4 (docs/reference/agent-program-runtimes.md) on this backend: the stage body RUNS its
// bootstrap after a failed `mise install`, which used to skip it through the `&&` join and so
// check no Node floor. The body's status is the refusal when the bootstrap refused, and
// `mise install`'s failure otherwise, so the failure still degrades as it did. The body runs
// under bash with a fake mise and a fake bootstrap, in the subshell provision.Script puts it in.
func TestAFailedMiseInstallStillRunsThisBackendsBootstrap(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	for _, tc := range []struct {
		name        string
		bootstrapRC int
		wantRC      int
	}{
		{"the bootstrap refuses", provision.RefusedStatus, provision.RefusedStatus},
		{"the bootstrap succeeds", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ran := filepath.Join(dir, "bootstrap-ran")
			script := filepath.Join(dir, "yolo-bootstrap.sh")
			for path, body := range map[string]string{
				filepath.Join(dir, "mise"): "#!/bin/sh\n[ \"$1\" = install ] && exit 1\nexit 0\n",
				script:                     "#!/bin/sh\ntouch '" + ran + "'\nexit " + strconv.Itoa(tc.bootstrapRC) + "\n",
			} {
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-c", "("+ProvisionSetup(script)+")")
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			err := cmd.Run()
			rc := 0
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(ran); err != nil {
				t.Fatalf("a failed `mise install` skipped the bootstrap: %v", err)
			}
			if rc != tc.wantRC {
				t.Errorf("stage body exit = %d, want %d", rc, tc.wantRC)
			}
		})
	}
}

// floorPackRoot stages, as the run pipeline stages a launch's packs, one pack whose program
// declares floor, and returns the tree's root (Options.HostPackRoot).
func floorPackRoot(t *testing.T, floor string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "pi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "pi", "contributes": [{"kind": "program", "bin": "sh", "via": "npm", ` +
		`"package": "pi-pkg", "node_floor": "` + floor + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func readinessPackRoot(t *testing.T) string {
	return readinessPackRootWithWorkspaceState(t, "")
}

func readinessPackRootWithWorkspaceState(t *testing.T, stateDir string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "readyfixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := ""
	if stateDir != "" {
		state = `{"kind":"state","at":"` + stateDir + `","scope":"workspace"},`
	}
	manifest := `{"name":"readyfixture","contributes":[` + state + `{"kind":"program","bin":"readyfixture-bin","via":"npm","package":"readyfixture-pkg"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// JR-D2's call-site gate: a program-declaring selected pack whose real install bin is absent
// must cause the production plan builder to admit the same confined stage used for mise tools.
func TestBuildPlanStartsStageForAnAbsentSelectedProgram(t *testing.T) {
	deps := mockDeps(nil)
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostPackRoot = readinessPackRoot(t)
	plan := buildPlan(deps, opts, mockDarwin())
	if len(plan.ProvisionArgv) == 0 {
		t.Fatal("the plan skipped the confined stage although the selected program was absent")
	}
	if !plan.ProvisionPrograms.Needed() || len(plan.ProvisionPrograms.Missing) != 1 ||
		!strings.Contains(plan.ProvisionPrograms.Missing[0], "readyfixture-bin") {
		t.Fatalf("program readiness decision = %+v, want the absent declared program", plan.ProvisionPrograms)
	}
	if !strings.Contains(strings.Join(plan.ProvisionArgv, " "), entrypoint.DarwinBootstrapScriptPath(paths.WorkspaceHomeState(opts.Workspace))) {
		t.Errorf("the admitted stage does not execute the generated readiness bootstrap: %v", plan.ProvisionArgv)
	}
}

func TestBuildPlanStartsStageWhenProgramReadinessIsUnknown(t *testing.T) {
	root := t.TempDir()
	broken := filepath.Join(root, "broken-pack")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "pack.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := mockDeps(nil)
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostPackRoot = root
	plan := buildPlan(deps, opts, mockDarwin())
	if len(plan.ProvisionArgv) == 0 || plan.ProvisionPrograms.Unknown == "" {
		t.Fatalf("unknown program readiness skipped the confined stage: decision=%+v argv=%v",
			plan.ProvisionPrograms, plan.ProvisionArgv)
	}
}

// The account home's default npm-prefix symlink still resolves to workspace A, but readiness
// for cold workspace B must inspect B's physical surface instead of following that stale link.
func TestProgramReadinessDoesNotReuseThePreviousWorkspaceHome(t *testing.T) {
	accountHome := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	oldBin := filepath.Join(workspaceA, "npm-global", "bin", "readyfixture-bin")
	if err := os.MkdirAll(filepath.Dir(oldBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBin, []byte("#!/bin/sh\\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspaceA, "npm-global"), filepath.Join(accountHome, ".npm-global")); err != nil {
		t.Fatal(err)
	}

	root := readinessPackRoot(t)
	decision := programReadinessStageFor(root, accountHome, filepath.Join(workspaceB, "project"),
		workspaceB, "/usr/bin:/bin", config.MergeMiseTools(jsonx.NewOrderedMap()), jsonx.NewOrderedMap())
	if !decision.Needed() || len(decision.Missing) != 1 {
		t.Fatalf("a binary in prior workspace A made cold workspace B look ready: %+v", decision)
	}
}

// Match the physical home and sidecar roots supplied by production, including on macOS where
// t.TempDir returns a path beneath the /var symlink. Keep intentional home aliases separate.
func readinessPhysicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProgramReadinessMapsConfiguredPrefixesThroughTheIncomingHomeLayout(t *testing.T) {
	accountHome := readinessPhysicalTempDir(t)
	workspaceA := readinessPhysicalTempDir(t)
	workspaceB := readinessPhysicalTempDir(t)
	packRoot := readinessPackRootWithWorkspaceState(t, ".claude")
	layout := entrypoint.DeriveDarwinHomeLayout(accountHome, paths.WorkspaceHomeState(workspaceA),
		[]string{".claude"}, nil)
	if err := layout.Apply(); err != nil {
		t.Fatal(err)
	}

	accountHomeAlias := filepath.Join(t.TempDir(), "account-home")
	if err := os.Symlink(accountHome, accountHomeAlias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		homePath string
		subtree  string
	}{
		{name: ".config", homePath: filepath.Join(accountHome, ".config"), subtree: "config"},
		{name: ".yolo/bin", homePath: filepath.Join(accountHome, ".yolo", "bin"), subtree: "yolo-bin"},
		{name: "selected pack state", homePath: filepath.Join(accountHome, ".claude"), subtree: "claude"},
		{name: "home-file redirect", homePath: filepath.Join(accountHome, ".claude.json"), subtree: filepath.Join("claude", "claude.json")},
		{name: "account-home symlink alias", homePath: filepath.Join(accountHomeAlias, ".config"), subtree: "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldBin := filepath.Join(paths.WorkspaceHomeState(workspaceA), tc.subtree, "npm", "bin", "readyfixture-bin")
			if err := os.MkdirAll(filepath.Dir(oldBin), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(oldBin, []byte("#!/bin/sh\\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			sandboxEnv := jsonx.NewOrderedMap()
			sandboxEnv.Set("NPM_CONFIG_PREFIX", filepath.Join(tc.homePath, "npm"))
			decision := programReadinessStageFor(packRoot, accountHome, workspaceB,
				paths.WorkspaceHomeState(workspaceB), "/usr/bin:/bin",
				config.MergeMiseTools(jsonx.NewOrderedMap()), sandboxEnv)
			if !decision.Needed() || len(decision.Missing) != 1 {
				t.Fatalf("the configured prefix followed workspace A's stale account-home link: %+v", decision)
			}
		})
	}

	unselectedTarget := filepath.Join(workspaceA, "unselected", "npm")
	if err := os.MkdirAll(unselectedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unselectedTarget, filepath.Join(accountHome, ".unselected")); err != nil {
		t.Fatal(err)
	}
	sandboxEnv := jsonx.NewOrderedMap()
	sandboxEnv.Set("NPM_CONFIG_PREFIX", filepath.Join(accountHome, ".unselected", "npm"))
	decision := programReadinessStageFor(packRoot, accountHome, workspaceB,
		paths.WorkspaceHomeState(workspaceB), "/usr/bin:/bin", config.MergeMiseTools(jsonx.NewOrderedMap()), sandboxEnv)
	if !decision.Needed() || decision.Unknown == "" {
		t.Fatalf("an account-home symlink outside the established incoming layout was treated as known: %+v", decision)
	}
}

func TestBuildPlanDoesNotAdmitProgramReadinessWhenTheExistingOffSwitchIsSet(t *testing.T) {
	deps := mockDeps(nil)
	deps.Getenv = func(key string) string {
		if key == paths.NoProgramReadinessEnv {
			return "1"
		}
		return ""
	}
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostPackRoot = readinessPackRoot(t)
	plan := buildPlan(deps, opts, mockDarwin())
	if len(plan.ProvisionArgv) != 0 {
		t.Fatalf("the existing readiness off-switch did not preserve test isolation; stage argv: %v", plan.ProvisionArgv)
	}
	if !containsArg(plan.BootstrapArgv, paths.NoProgramReadinessEnv+"=1") {
		t.Fatalf("the off-switch did not cross into the Darwin bootstrap's env -i contract: %v", plan.BootstrapArgv)
	}
}

func TestPrintPlanDoesNotClaimProgramsPresentWhenReadinessIsDisabled(t *testing.T) {
	deps := mockDeps(nil)
	deps.Getenv = func(key string) string {
		if key == paths.NoProgramReadinessEnv {
			return "1"
		}
		return ""
	}
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostPackRoot = readinessPackRoot(t)
	plan := buildPlan(deps, opts, mockDarwin())
	var out strings.Builder
	PrintPlan(&out, plan, nil)
	if strings.Contains(out.String(), "every selected program is present") {
		t.Fatalf("dry-run claims the absent selected program is present despite the off-switch:\\n%s", out.String())
	}
	if !strings.Contains(out.String(), paths.NoProgramReadinessEnv) {
		t.Fatalf("dry-run does not disclose that program readiness was disabled:\\n%s", out.String())
	}
}

func TestProgramReadinessRetainsDisabledDispositionWithoutPackRoot(t *testing.T) {
	sandboxEnv := jsonx.NewOrderedMap()
	sandboxEnv.Set(paths.NoProgramReadinessEnv, "1")
	decision := programReadinessStageFor("", t.TempDir(), t.TempDir(), t.TempDir(), "/usr/bin:/bin",
		config.MergeMiseTools(jsonx.NewOrderedMap()), sandboxEnv)
	if !decision.Disabled || decision.Needed() {
		t.Fatalf("readiness disabled without a staged pack root = %+v, want disabled and no admission", decision)
	}
}

func TestReadinessOffSwitchDoesNotSuppressTheExistingMiseStage(t *testing.T) {
	deps := mockDeps(nil)
	deps.Getenv = func(key string) string {
		if key == paths.NoProgramReadinessEnv {
			return "1"
		}
		return ""
	}
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = provisionCfg()
	opts.HostPackRoot = readinessPackRoot(t)
	plan := buildPlan(deps, opts, mockDarwin())
	if len(plan.ProvisionArgv) == 0 || !strings.Contains(strings.Join(plan.ProvisionArgv, " "), "mise install") {
		t.Fatalf("the existing mise provisioning stage was suppressed by the program-readiness switch: %v", plan.ProvisionArgv)
	}
	if !containsArg(plan.BootstrapArgv, paths.NoProgramReadinessEnv+"=1") {
		t.Fatalf("the bootstrap lost the readiness switch while preserving mise work: %v", plan.BootstrapArgv)
	}
}

func TestPrintPlanExplainsSkippedNodeFloorWithTheCorrectCondition(t *testing.T) {
	plan := BuildRunPlanWithStages("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), nil, nil,
		"/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), mockDarwin(),
		nil, JailDaemons{}, FloorStage{}, ProgramReadinessStage{}, PlanSession{})
	var out strings.Builder
	PrintPlan(&out, plan, nil)
	if !strings.Contains(out.String(), "no declared Node floor the host could not show met") {
		t.Fatalf("the skipped-stage reason reverses the Node-floor condition:\n%s", out.String())
	}
}

// A selected program install already present at its declared prefix is no reason by itself to
// start the stage. The host-side probe uses an isolated fixture home, never the real sandbox home.
func TestProgramReadinessStageSkipsAProgramTheHostProvesPresent(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	physicalHome := filepath.Join(workspace, ".yolo", "home")
	bin := filepath.Join(physicalHome, "npm-global", "bin", "readyfixture-bin")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := readinessPackRoot(t)
	mise := config.MergeMiseTools(jsonx.NewOrderedMap())
	decision := programReadinessStageFor(root, home, workspace, physicalHome, "/usr/bin:/bin",
		mise, jsonx.NewOrderedMap())
	if decision.Needed() {
		t.Errorf("a host-proven program was reported missing: %+v", decision)
	}
}

func TestMissingProgramsBypassReachesTheBootstrap(t *testing.T) {
	deps := mockDeps(nil)
	deps.Getenv = func(name string) string {
		if name == paths.AllowMissingProgramsEnv {
			return "1"
		}
		return ""
	}
	plan := buildPlan(deps, newOpts("/Users/Shared/yolo/proj"), mockDarwin())
	if !strings.Contains(strings.Join(plan.BootstrapArgv, " "), paths.AllowMissingProgramsEnv+"=1") {
		t.Fatalf("the host's existing missing-programs bypass did not reach readiness in the bootstrap:\n%v",
			plan.BootstrapArgv)
	}
}

// AR-L3 (docs/reference/agent-program-runtimes.md): with no `mise_tools`, a Node floor a selected
// pack declares starts the provisioning stage unless the host shows it met. Before it, this
// backend started the stage for `mise_tools` alone, so such a workspace got neither the floor's
// install nor its refusal. Driven through buildPlan, the orchestrator's composition RunMacosUser
// launches from, so dropping the host check there fails this test. The check is asked of the
// sandbox's own PATH (the one the bootstrap is told) and home, and every doubt starts the stage.
func TestADeclaredNodeFloorStartsTheStageUnlessTheHostShowsItMet(t *testing.T) {
	root := floorPackRoot(t, "22.19")
	for _, tc := range []struct {
		name      string
		wired     bool // whether deps carry a host check at all
		met       bool // its answer
		wantStage bool
		wantUnmet string
	}{
		{"the host shows it met", true, true, false, "[]"},
		{"the host cannot show it met", true, false, true, "[22.19]"},
		{"no host check wired", false, false, true, "[22.19]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := mockDeps(nil)
			var askedFloor, askedPath, askedHome string
			if tc.wired {
				deps.NodeFloorMet = func(floor, loginPath, home string) bool {
					askedFloor, askedPath, askedHome = floor, loginPath, home
					return tc.met
				}
			}
			opts := newOpts("/Users/Shared/yolo/proj")
			opts.HostPackRoot = root
			plan := buildPlan(deps, opts, mockDarwin())
			if got := len(plan.ProvisionArgv) > 0; got != tc.wantStage {
				t.Fatalf("stage started = %v, want %v (floors %+v)", got, tc.wantStage, plan.ProvisionFloors)
			}
			if got := fmt.Sprint(plan.ProvisionFloors.Unmet); got != tc.wantUnmet {
				t.Errorf("unmet floors = %s, want %s", got, tc.wantUnmet)
			}
			if !tc.wired {
				return
			}
			if askedFloor != "22.19" {
				t.Errorf("the host was asked about floor %q, want the declared 22.19", askedFloor)
			}
			if want, _ := argvEnvValue(plan.BootstrapArgv, entrypoint.DarwinLoginPathEnv); askedPath != want {
				t.Errorf("the host check read PATH %q, but the sandbox's is %q", askedPath, want)
			}
			if askedHome != SandboxHome() {
				t.Errorf("the host check excluded home %q, want the sandbox's %q", askedHome, SandboxHome())
			}
		})
	}
}

// An unreadable staged tree is a host that cannot answer, and it starts the stage, which reads
// the tree again from inside; the dry run names why.
func TestAnUnreadablePackTreeStartsTheStage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "pack.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := mockDeps(nil)
	deps.NodeFloorMet = func(string, string, string) bool { return true }
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostPackRoot = root
	plan := buildPlan(deps, opts, mockDarwin())
	if len(plan.ProvisionArgv) == 0 || plan.ProvisionFloors.Unknown == "" {
		t.Fatalf("an unreadable pack tree must start the stage; floors %+v", plan.ProvisionFloors)
	}
	var out bytes.Buffer
	PrintPlan(&out, plan, nil)
	if !strings.Contains(out.String(), "runs for: the staged packs' Node floors could not be read") {
		t.Errorf("the dry run does not say why the stage runs:\n%s", out.String())
	}
}

// The production deps wire the host check, so the rule above is live outside tests.
func TestRealDepsWireTheHostNodeFloorCheck(t *testing.T) {
	if RealDeps(nil, nil, false).NodeFloorMet == nil {
		t.Fatal("RealDeps leaves NodeFloorMet nil, so every declared floor starts a stage")
	}
}

// provisionPlan builds a plan whose config asks for the stage.
func provisionPlan(t *testing.T) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/yolo/proj", provisionCfg(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo-jail/bin/yolo", "", HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), mockDarwin(), nil)
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

// A REFUSAL (provision.RefusedStatus, docs/reference/agent-program-runtimes.md OQ-AR3): the stage
// ran — the marker is in the log — and refused the launch without asking anyone, because a
// selected pack declares a Node floor nothing satisfies. The agent must not launch, and the
// console must call it a refusal: "Provisioning was aborted" names a veto nobody gave.
func TestARefusingStageStopsTheLaunchAndSaysSo(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	var rec []string
	d := stageFailureDeps(t, &rec, ws,
		"=== yolo provisioning ===\n"+provision.FailedMarker+" (exit "+strconv.Itoa(provision.RefusedStatus)+")\n")
	failing := d.Run
	d.Run = func(argv []string) int {
		if rc := failing(argv); rc != 7 {
			return rc
		}
		return provision.RefusedStatus // the script's `exit "$_prc"` on a refusal
	}
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts(ws)
	opts.Config = provisionCfg()

	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Fatalf("rc = %d, want 1 — a refusing stage must stop the launch\n%s", rc, buf.String())
	}
	for _, line := range rec {
		if strings.HasPrefix(line, "proxy:") {
			t.Errorf("the agent launched after the stage refused:\n%s", line)
		}
	}
	if !strings.Contains(buf.String(), "refused the launch") {
		t.Errorf("the console does not name the refusal:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "was aborted") {
		t.Errorf("a refusal was reported as a human's veto:\n%s", buf.String())
	}
}

// TestTheProvisioningStageHonorsNoColor: the stage's console failure line is red unless
// the host's NO_COLOR is set, which the plan reads from the env the sandbox runs in —
// driven from the host environment through buildPlan, so the forwarding
// (MacosSandboxEnv) and the decision (BuildRunPlan) are both on the path. The unset run
// is the control that keeps the veto from passing on a script that never colored.
func TestTheProvisioningStageHonorsNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor  string
		wantANSI bool
	}{{"", true}, {"1", false}} {
		d := mockDeps(nil)
		d.Getenv = func(k string) string {
			if k == "NO_COLOR" {
				return tc.noColor
			}
			return ""
		}
		opts := newOpts("/Users/Shared/yolo/proj")
		opts.Config = provisionCfg()
		plan := buildPlan(d, opts, mockDarwin())
		if len(plan.ProvisionArgv) == 0 {
			t.Fatal("the fixture asks for the stage, and the plan carries none")
		}
		stage := strings.Join(plan.ProvisionArgv, " ")
		if !strings.Contains(stage, "✗ Provisioning failed") {
			t.Fatalf("the stage no longer carries its failure line:\n%s", stage)
		}
		if hasANSI := strings.Contains(stage, `\033[`); hasANSI != tc.wantANSI {
			t.Errorf("host NO_COLOR=%q: the stage's console line carries an escape = %v, want %v",
				tc.noColor, hasANSI, tc.wantANSI)
		}
	}
}
