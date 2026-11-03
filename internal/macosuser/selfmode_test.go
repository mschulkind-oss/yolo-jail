package macosuser

import (
	"bytes"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// selfmode_test.go covers the run-as-you plan (macos_user_mode "self", slice 6 of
// docs/design/macos-user-model-plan.md): no account, no sudo, no grants, the deny-by-default
// profile, the workspace home, a confined bootstrap.

func selfRunPlan(t *testing.T, ws string) RunPlan {
	t.Helper()
	return selfRunPlanWith(t, ws, HostContext{})
}

func selfRunPlanWith(t *testing.T, ws string, hostCtx HostContext) RunPlan {
	t.Helper()
	return BuildRunPlanWithDaemons(ws, jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, hostCtx, jsonx.NewOrderedMap(), nil, nil,
		JailDaemons{}, FloorStage{}, PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/"})
}

func TestSelfModePlanRunsNoSudoAndGrantsNothing(t *testing.T) {
	plan := selfRunPlan(t, "/Users/you/code/alpha")
	argvs := append([][]string{plan.LaunchArgv, plan.BootstrapArgv, plan.ProvisionArgv}, plan.StageCommands...)
	for _, a := range argvs {
		joined := strings.Join(a, " ")
		if len(a) > 0 && a[0] == "/usr/bin/sudo" {
			t.Errorf("self mode crosses into an account: %s", joined)
		}
		if strings.Contains(joined, "chmod +a") || strings.Contains(joined, "_yolojail") {
			t.Errorf("self mode grants or names the account: %s", joined)
		}
	}
	if problems := PlanInvariants(plan); len(problems) != 0 {
		t.Errorf("self-mode plan has problems:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestSelfModeAcceptsAWorkspaceInTheHome(t *testing.T) {
	if plan := selfRunPlan(t, "/Users/you/code/alpha"); plan.OffendingHomeSet {
		t.Errorf("self mode refuses a workspace in the home (%s)", plan.OffendingHome)
	}
}

func TestSelfModeUsesTheDenyDefaultProfileAndTheWorkspaceHome(t *testing.T) {
	plan := selfRunPlan(t, "/Users/you/code/alpha")
	if !strings.Contains(plan.Seatbelt, "(deny default)") || strings.Contains(plan.Seatbelt, "(allow default)") {
		t.Error("self mode does not run under the deny-default profile")
	}
	if !containsArg(plan.LaunchArgv, "HOME=/Users/you/code/alpha/.yolo/home") {
		t.Errorf("launch HOME is not the workspace home: %v", plan.LaunchArgv)
	}
	if !containsArgPair(plan.BootstrapArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		t.Errorf("the bootstrap is not confined (MMP-D9): %v", plan.BootstrapArgv)
	}
}

// THE ACCOUNT MODE TAKES THE SAME PROFILE AND A HOME PER WORKSPACE (slice 12a, MMP-D25): the zero
// mode's plan installs the deny-by-default profile, runs its bootstrap confined, sets HOME to the
// workspace's own home, and still crosses through the sudo rule as the sandbox account. Two
// workspaces' plans share no HOME. Fails if BuildRunPlanWithStages sends the account mode back to
// the allow-default profile, the account's home or an unconfined bootstrap.
func TestAccountModePlanTakesTheDenyDefaultProfileAndTheWorkspaceHome(t *testing.T) {
	plan := testRunPlan(t)
	if plan.RunAs.Mode != ModeAccount {
		t.Fatalf("testRunPlan is not an account-mode plan: %v", plan.RunAs.Mode)
	}
	if !strings.Contains(plan.Seatbelt, "\n(deny default)\n") || strings.Contains(plan.Seatbelt, "(allow default)") {
		t.Error("the account mode does not run under the deny-default profile")
	}
	home := paths.WorkspaceHomeState("/Users/Shared/proj")
	if !containsArg(plan.LaunchArgv, "HOME="+home) {
		t.Errorf("launch HOME is not the workspace home %s: %v", home, plan.LaunchArgv)
	}
	if !containsArgPair(plan.BootstrapArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		t.Errorf("the account-mode bootstrap is not confined (MMP-D9): %v", plan.BootstrapArgv)
	}
	for name, argv := range map[string][]string{"launch": plan.LaunchArgv, "bootstrap": plan.BootstrapArgv} {
		if len(argv) < 4 || argv[0] != "/usr/bin/sudo" || !containsArg(argv, "--user="+SandboxUser) {
			t.Errorf("the account-mode %s argv does not cross as %s through the rule: %v", name, SandboxUser, argv)
		}
	}
	if len(plan.EnvFileGrantCommands) == 0 {
		t.Error("the account-mode plan grants the sandbox account no read on its session env file")
	}
	if !strings.Contains(plan.Seatbelt, "(subpath "+sbplStr(home)+")") {
		t.Errorf("the profile does not open the workspace home %s", home)
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("the account-mode plan violates its invariants:\n%s", strings.Join(problems, "\n"))
	}
	other := BuildRunPlan("/Users/Shared/other", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	if other.RunAs.Homes.Home == plan.RunAs.Homes.Home {
		t.Errorf("two workspaces' account-mode plans share HOME %s", plan.RunAs.Homes.Home)
	}
	if strings.Contains(other.Seatbelt, sbplStr(home)) {
		t.Errorf("another workspace's profile names this workspace's home %s", home)
	}
}

// THE ACCOUNT-MODE TWIN OF TestSelfModeBootstrapIsToldTheHomes: HOME is the sidecar (which selects
// the workspace-home layout), and the account's own home is the machine root, the shared store
// and the real home at once, with the mise store in it. Fails if the bootstrap env's homes go back
// to self mode only: the bootstrap would refuse the layout (darwinWorkspaceHomeProblem).
func TestAccountModeBootstrapIsToldTheHomes(t *testing.T) {
	ws := "/Users/Shared/proj"
	plan := testRunPlan(t)
	h := plan.RunAs.Homes
	if h != (Homes{Home: paths.WorkspaceHomeState(ws), Machine: SandboxHome(), Shared: SandboxHome(), Real: SandboxHome()}) {
		t.Fatalf("the account-mode plan's homes are %+v", h)
	}
	for _, want := range []string{
		"HOME=" + paths.WorkspaceHomeState(ws),
		entrypoint.DarwinHomeSidecarEnv + "=" + paths.WorkspaceHomeState(ws),
		entrypoint.DarwinMachineHomeEnv + "=" + SandboxHome(),
		entrypoint.DarwinSharedHomeEnv + "=" + SandboxHome(),
		paths.DarwinRealHomeEnv + "=" + SandboxHome(),
		"MISE_DATA_DIR=" + SandboxMiseData(SandboxHome()),
	} {
		if !containsArg(plan.BootstrapArgv, want) {
			t.Errorf("the bootstrap env lacks %s: %v", want, plan.BootstrapArgv)
		}
	}
	if !containsArg(plan.LaunchArgv, "USER="+SandboxUser) {
		t.Errorf("the launch runs with another USER than the account's: %v", plan.LaunchArgv)
	}
	if !containsArg(plan.LaunchArgv, "MISE_DATA_DIR="+SandboxMiseData(SandboxHome())) {
		t.Errorf("the launch's mise store is not the machine root's: %v", plan.LaunchArgv)
	}
}

// THE ACCOUNT-MODE HALF OF PlanInvariants fails each plan that lost a piece of MMP-D25, and is
// silent for a run-as-you plan. A mutation test, so deleting accountModeProblems' call in
// PlanInvariants fails it.
func TestAccountModeInvariantsCatchAnOldShapedPlan(t *testing.T) {
	good := testRunPlan(t)
	for name, tc := range map[string]struct {
		mutate func(*RunPlan)
		want   string
	}{
		"the account's home": {func(p *RunPlan) {
			p.RunAs.Homes = accountHomes("")
		}, "two workspaces would share one home"},
		"HOME off the argv": {func(p *RunPlan) {
			p.LaunchArgv = replaceArg(p.LaunchArgv, "HOME="+p.RunAs.Homes.Home, "HOME="+SandboxHome())
		}, "does not set HOME="},
		"the allow-default profile": {func(p *RunPlan) {
			p.Seatbelt = "(version 1)\n(allow default)\n"
		}, "does not install the deny-by-default"},
		"an unconfined bootstrap": {func(p *RunPlan) {
			p.BootstrapArgv = replaceArg(p.BootstrapArgv, "/usr/bin/sandbox-exec", "/usr/bin/true")
		}, "the account-mode bootstrap does not run under"},
		"no env-file grant": {func(p *RunPlan) {
			p.EnvFileGrantCommands = nil
		}, "grants the sandbox account no read"},
	} {
		t.Run(name, func(t *testing.T) {
			p := good
			p.LaunchArgv = append([]string(nil), good.LaunchArgv...)
			p.BootstrapArgv = append([]string(nil), good.BootstrapArgv...)
			tc.mutate(&p)
			if probs := strings.Join(PlanInvariants(p), "\n"); !strings.Contains(probs, tc.want) {
				t.Errorf("PlanInvariants did not say %q:\n%s", tc.want, probs)
			}
		})
	}
	if probs := accountModeProblems(selfRunPlan(t, "/Users/you/code/alpha")); len(probs) > 0 {
		t.Errorf("the account-mode half spoke for a run-as-you plan: %v", probs)
	}
}

// replaceArg returns argv with every word equal to old replaced by new.
func replaceArg(argv []string, old, new string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a == old {
			a = new
		}
		out[i] = a
	}
	return out
}

// THE BOOTSTRAP IS TOLD THE RUN-AS-YOU HOMES: HOME is the sidecar itself (which selects the
// workspace-home layout), the machine root and shared store it links into, the mise store in
// the machine root (MMP-D6), the real home its scope and identity checks read, and the USER the
// launch runs as. Fails if the self branch of the bootstrap env is deleted: the bootstrap would
// then refuse the layout (darwinWorkspaceHomeProblem) or judge scope against the account.
func TestSelfModeBootstrapIsToldTheHomes(t *testing.T) {
	ws := "/Users/you/code/alpha"
	plan := selfRunPlan(t, ws)
	h := LaunchHomes(ModeSelf, ws, "/Users/you")
	for _, want := range []string{
		"HOME=" + h.Home,
		entrypoint.DarwinHomeSidecarEnv + "=" + paths.WorkspaceHomeState(ws),
		entrypoint.DarwinMachineHomeEnv + "=" + h.Machine,
		entrypoint.DarwinSharedHomeEnv + "=" + h.Shared,
		paths.DarwinRealHomeEnv + "=/Users/you",
		"MISE_DATA_DIR=" + SandboxMiseData(h.Machine),
	} {
		if !containsArg(plan.BootstrapArgv, want) {
			t.Errorf("the bootstrap env lacks %s: %v", want, plan.BootstrapArgv)
		}
	}
	if !containsArg(plan.LaunchArgv, "USER=you") {
		t.Errorf("the launch runs with another USER than yours: %v", plan.LaunchArgv)
	}
	if !containsArg(plan.LaunchArgv, "MISE_DATA_DIR="+SandboxMiseData(h.Machine)) {
		t.Errorf("the launch's mise store is not the machine root's: %v", plan.LaunchArgv)
	}
}

// THE PROFILE A SELF PLAN INSTALLS opens what the run-as-you homes need — the workspace, HOME,
// the machine root, your temp dir — and splices the shared caches (MMP-D12), whose links the
// bootstrap is told to lay. Fails if the self branch stops passing the aliases or the homes.
func TestSelfModeProfileOpensTheHomesAndTheCacheAliases(t *testing.T) {
	ws := "/Users/you/code/alpha"
	h := LaunchHomes(ModeSelf, ws, "/Users/you")
	alias := CacheAlias{Tool: "pip", Link: filepath.Join(h.Machine, "Library", "Caches", "pip"),
		Target: "/Users/you/Library/Caches/pip"}
	plan := selfRunPlanWith(t, ws, HostContext{CacheAliases: []CacheAlias{alias}})
	for _, want := range []string{
		`(subpath "` + ws + `")`, `(subpath "` + h.Home + `")`, `(subpath "` + h.Machine + `")`,
		`(subpath "/private/var/folders/ab/xyz/T")`, "#seatbelt-test-id:nix-daemon-allow#",
		"#seatbelt-test-id:" + CacheAliasAllowID + "#", `(subpath "` + alias.Target + `")`,
	} {
		if !strings.Contains(plan.Seatbelt, want) {
			t.Errorf("the self-mode profile lacks %s", want)
		}
	}
	wire, ok := argvEnvValue(plan.BootstrapArgv, entrypoint.DarwinCacheAliasesEnv)
	if !ok || !strings.Contains(wire, alias.Link) || !strings.Contains(wire, alias.Target) {
		t.Errorf("the bootstrap is not told to lay the alias link (%q, %v)", wire, ok)
	}
	if problems := PlanInvariants(plan); len(problems) != 0 {
		t.Errorf("self-mode plan with an alias has problems:\n  %s", strings.Join(problems, "\n  "))
	}
}

// AN ALIAS MISSING EITHER HALF IS A PLAN PROBLEM: the profile's allow or the bootstrap's link.
func TestSelfModeInvariantsCatchAHalfWiredCacheAlias(t *testing.T) {
	ws := "/Users/you/code/alpha"
	h := LaunchHomes(ModeSelf, ws, "/Users/you")
	alias := CacheAlias{Tool: "pip", Link: filepath.Join(h.Machine, "Library", "Caches", "pip"),
		Target: "/Users/you/Library/Caches/pip"}
	base := selfRunPlanWith(t, ws, HostContext{CacheAliases: []CacheAlias{alias}})

	noLink := base
	noLink.BootstrapArgv = slices.DeleteFunc(slices.Clone(base.BootstrapArgv), func(w string) bool {
		return strings.HasPrefix(w, entrypoint.DarwinCacheAliasesEnv+"=")
	})
	if !hasProblem(PlanInvariants(noLink), "not named to the bootstrap") {
		t.Errorf("an alias the bootstrap is not told about passed: %v", PlanInvariants(noLink))
	}
	noAllow := base
	noAllow.Seatbelt = strings.ReplaceAll(base.Seatbelt, "#seatbelt-test-id:"+CacheAliasAllowID+"#", "")
	if !hasProblem(PlanInvariants(noAllow), "does not open") {
		t.Errorf("an alias the profile does not open passed: %v", PlanInvariants(noAllow))
	}
}

// A self plan with no home named is refused by the invariants rather than rendering a machine
// root relative to the cwd.
func TestSelfModeWithoutAHostHomeIsRefused(t *testing.T) {
	plan := BuildRunPlanWithDaemons("/Users/you/code/alpha", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
		JailDaemons{}, FloorStage{}, PlanSession{Mode: ModeSelf})
	if !hasProblem(PlanInvariants(plan), "no absolute home") {
		t.Errorf("a self plan with no home passed: %v", PlanInvariants(plan))
	}
}

// THE INVARIANTS CATCH A SELF PLAN THAT LOST ITS SELF HALF: an account crossing, the old profile,
// an unconfined bootstrap, a grant. Each mutation is what deleting one line of the self branch
// would produce.
func TestSelfModeInvariantsCatchAnAccountShapedPlan(t *testing.T) {
	base := selfRunPlan(t, "/Users/you/code/alpha")
	for name, tc := range map[string]struct {
		mutate func(*RunPlan)
		want   string
	}{
		"sudo crossing": {func(p *RunPlan) {
			p.LaunchArgv = append([]string{sudoBin, "-n", "--user=" + SandboxUser}, p.LaunchArgv...)
		}, "does not cross"},
		"old profile":            {func(p *RunPlan) { p.Seatbelt = "(version 1)\n(allow default)\n" }, "deny-by-default"},
		"unconfined bootstrap":   {func(p *RunPlan) { p.BootstrapArgv = DarwinBootstrapArgv(p.RunAs, p.StagedYolo, "", nil, "") }, "MMP-D9"},
		"an access entry staged": {func(p *RunPlan) { p.StageCommands = append(p.StageCommands, []string{chmodBin, "+a", "x", "/y"}) }, "grants an access entry"},
	} {
		p := base
		tc.mutate(&p)
		if !hasProblem(PlanInvariants(p), tc.want) {
			t.Errorf("%s: no problem containing %q in %v", name, tc.want, PlanInvariants(p))
		}
	}
}

// IN THE RUN-AS-YOU MODE A CONTEXT SOURCE IN YOUR HOME IS DELIVERED (rule 5 is the account's):
// the sandbox is you, and the profile names the source. yolo's own state dir under your home is
// still refused, which the account mode kept out by the home rule.
func TestSelfModeSitesAContextSourceInTheHome(t *testing.T) {
	ws := "/Users/Shared/yolo/proj"
	src := []ContextLink{{Dest: "/ctx/notes", Source: "/Users/you/notes", Dir: true}}
	if r := SiteContextLinks(DarwinContextSitingFor(ModeAccount, "/Users/you"), ws, src, nil); len(r) == 0 {
		t.Fatal("the account mode delivered a source inside a home")
	}
	if r := SiteContextLinks(DarwinContextSitingFor(ModeSelf, "/Users/you"), ws, src, nil); len(r) != 0 {
		t.Errorf("the run-as-you mode refused a source in your home: %v", r[0].Reason)
	}
	state := []ContextLink{{Dest: "/ctx/state", Source: "/Users/you/.local/share/yolo-jail", Dir: true}}
	if r := SiteContextLinks(DarwinContextSitingFor(ModeSelf, "/Users/you"), ws, state, nil); len(r) == 0 {
		t.Error("the run-as-you mode delivered yolo's own state dir as a context source")
	}
}

// selfModeDeps is mockDeps on a Mac where `yolo macos-setup` never ran: no sandbox account, no
// run-as rule. A run-as-you launch needs neither.
func selfModeDeps(rec *[]string) Deps {
	d := mockDeps(rec)
	d.SandboxUserExists = func() bool { return false }
	d.RunAsGrant = func() bool { return false }
	d.PathIsDir = func(p string) bool { return p != SandboxHome() }
	return d
}

func selfModeOpts(ws string) Options {
	o := newOpts(ws)
	o.Mode, o.HostHome, o.HostUser = ModeSelf, "/Users/you", "you"
	return o
}

// THE RUN-AS-YOU LAUNCH NEEDS NO ACCOUNT: on a Mac with no sandbox account and no run-as rule it
// runs to the session, runs no sudo, and says first that the agent runs as you. Fails if
// RunMacosUser's self branch is deleted: the account preconditions would refuse this Mac.
func TestSelfModeNeedsNoAccount(t *testing.T) {
	var rec []string
	d := selfModeDeps(&rec)
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, selfModeOpts("/Users/you/code/alpha")); rc != 42 {
		t.Fatalf("rc = %d, want the session's 42\n%s", rc, out.String())
	}
	for _, r := range rec {
		if strings.Contains(r, sudoBin) || strings.Contains(r, "chmod +a") {
			t.Errorf("the run-as-you launch ran %s", r)
		}
	}
	if !strings.Contains(out.String(), "macos-user runs the agent as you") ||
		!strings.Contains(out.String(), "deny-by-default Seatbelt profile") {
		t.Errorf("the launch did not disclose that the agent runs as you:\n%s", out.String())
	}
}

// AN ABSENT STAGING ROOT IS MADE BY THE LAUNCH, as you, with no sudo (MMP-D8): /Users/Shared is
// 1777. A foreign one still refuses.
func TestSelfModeCreatesAnAbsentStagingRoot(t *testing.T) {
	var rec []string
	d := selfModeDeps(&rec)
	made := false
	d.StagingRootFacts = func(string) StagingRootFacts {
		if !made {
			return StagingRootFacts{Exists: false}
		}
		return StagingRootFacts{Exists: true, IsDir: true, UID: 501, Mode: 0o755}
	}
	run := d.Run
	d.Run = func(argv []string) int {
		if len(argv) == 4 && argv[0] == mkdirBin && argv[3] == StagingRoot() {
			made = true
		}
		return run(argv)
	}
	if rc := RunMacosUser(d, selfModeOpts("/Users/you/code/alpha")); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	want := "run:" + strings.Join([]string{mkdirBin, "-m", categoryDirMode, StagingRoot()}, " ")
	if !slices.Contains(rec, want) {
		t.Errorf("the launch did not make the staging root (%s):\n%s", want, strings.Join(rec, "\n"))
	}

	d.StagingRootFacts = func(string) StagingRootFacts {
		return StagingRootFacts{Exists: true, IsDir: true, UID: 777, Mode: 0o755}
	}
	var out bytes.Buffer
	d.Out = &out
	if rc := RunMacosUser(d, selfModeOpts("/Users/you/code/alpha")); rc != 1 ||
		!strings.Contains(out.String(), "belongs to another account") {
		t.Errorf("a foreign staging root did not refuse (rc %d):\n%s", rc, out.String())
	}
}

// `yolo check` IN THE RUN-AS-YOU MODE grades the rows its launch asks and no others: on a Mac
// with no account and no staging root, every row holds, and none names the account or setup.
func TestSelfModeCheckReportsOnlyItsOwnPreconditions(t *testing.T) {
	d := selfModeDeps(nil)
	d.StagingRootFacts = func(string) StagingRootFacts { return StagingRootFacts{Exists: false} }
	results := CheckLaunchPreconditionsFor(ModeSelf, d.launchProbes(), "/Users/you/code/alpha")
	if len(results) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range results {
		if !slices.Contains(selfModePreconditionIDs, r.ID) {
			t.Errorf("the run-as-you report grades %s, which its launch never asks", r.ID)
		}
		if !r.Checked || !r.Held {
			t.Errorf("%s does not hold on a Mac a run-as-you launch runs on", r.ID)
		}
	}
	if acct := CheckLaunchPreconditionsFor(ModeAccount, d.launchProbes(), "/Users/you/code/alpha"); len(acct) <= len(results) {
		t.Error("the account report lost its account rows")
	}
}

// A RUN-AS-YOU LAUNCH WITH A JAIL DAEMON STARTS IT ON A MAC WITHOUT THE ACCOUNT: the daemon env
// file is written as you and granted to nobody. Every claude launch brings the wire bridge, a
// jail daemon, so a grant naming the second account here stopped `yolo -- claude` at "Could not
// grant _yolojail read on …daemons.env". Run refuses every `chmod +a` the way a Mac without the
// account does. Fails if daemonEnvFilePlan stops stripping its grants in self mode.
func TestSelfModeStartsJailDaemonsWithoutTheAccount(t *testing.T) {
	ws := "/Users/you/code/alpha"
	var rec []string
	d := selfModeDeps(&rec)
	run := d.Run
	d.Run = func(argv []string) int {
		if len(argv) >= 2 && argv[0] == chmodBin && argv[1] == "+a" {
			rec = append(rec, "refused:"+strings.Join(argv, " "))
			return 1
		}
		return run(argv)
	}
	fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var out bytes.Buffer
	d.Out = &out
	o := selfModeOpts(ws)
	o.JailDaemons = selfModeWireBridgeDaemons("")
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d, want the session's 42\n%s", rc, out.String())
	}
	started := false
	for _, r := range rec {
		started = started || strings.HasPrefix(r, "start:")
		if strings.Contains(r, "chmod +a") || strings.Contains(r, SandboxUser) {
			t.Errorf("the run-as-you daemon start ran %s", r)
		}
	}
	if !started {
		t.Errorf("the supervisor was not started:\n%s", strings.Join(rec, "\n"))
	}
}

// AND THE PLAN SAYS SO: a self-mode plan carrying a daemon env file grants it nothing, and the
// invariants read the daemon file's commands too.
func TestSelfModeInvariantsCatchADaemonEnvFileGrant(t *testing.T) {
	ws := "/Users/you/code/alpha"
	plan := BuildRunPlanWithDaemons(ws, jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
		selfModeWireBridgeDaemons("/opt/yolo/bin/darwin-arm64"), FloorStage{},
		PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/"})
	if plan.DaemonEnvFile == "" {
		t.Fatal("fixture plan has no daemon env file")
	}
	if g := plan.daemonEnvFilePlan(); len(g.grant) != 0 {
		t.Errorf("the self-mode daemon env file is granted: %v", g.grant)
	}
	if problems := selfModeProblems(plan); len(problems) != 0 {
		t.Errorf("a self-mode plan with a daemon has problems:\n  %s", strings.Join(problems, "\n  "))
	}
}

// THE SELF-MODE PROFILE READS ONLY THIS SESSION'S STAGED PATHS: its env file, its guest bin,
// its context copy, and never the staging root whole, which holds every other session's env
// file and every other workspace's context copy. Fails if the plan stops naming StagedReads.
func TestSelfModeProfileReadsOnlyThisSessionsStagedPaths(t *testing.T) {
	plan := BuildRunPlanWithDaemons("/Users/you/code/alpha", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
		selfModeWireBridgeDaemons("/opt/yolo/bin/darwin-arm64"), FloorStage{},
		PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/"})
	block := profileBlock(plan.Seatbelt, "staging-root-read-allow")
	if strings.Contains(block, "(subpath "+sbplStr(plan.StagedDir)+")") {
		t.Errorf("the self-mode profile reads the whole staging root:\n%s", block)
	}
	for _, own := range []string{plan.EnvFile, plan.DaemonEnvFile, plan.SessionGuestBinDir, plan.ContextDir} {
		if own == "" {
			t.Fatalf("fixture plan lacks a staged path (%q)", own)
		}
		if !strings.Contains(block, "(subpath "+sbplStr(own)+")") {
			t.Errorf("the self-mode profile does not read %s:\n%s", own, block)
		}
	}
	for _, other := range []string{SandboxEnvFile("yolo-other-1.x", ""), StagedCtxParent("")} {
		if strings.Contains(block, "(subpath "+sbplStr(other)+")") {
			t.Errorf("the self-mode profile reads %s:\n%s", other, block)
		}
	}
	if !strings.Contains(block, "(literal "+sbplStr(pathParent(plan.EnvFile))+")") {
		t.Errorf("the env dir above the session's file gets no metadata:\n%s", block)
	}
}

// selfModeWireBridgeDaemons is a daemon payload with no host endpoint beside it, the wire
// bridge's shape: a run-as-you plan reads every endpoint's port, which a fixture path has not.
func selfModeWireBridgeDaemons(src string) JailDaemons {
	env := jsonx.NewOrderedMap()
	env.Set(jailDaemonsEnv, `[{"name":"wire-bridge","cmd":["yolo-jaild","wire-bridge"],"restart":"on-failure"}]`)
	return JailDaemons{Env: env, GuestBinSource: src}
}

// THE HOME-CONTENT DENIES STAND ASIDE FOR THE BOOTSTRAP ALONE. The run-as-you bootstrap runs under
// the session profile (MMP-D9) and is what delivers the staged skills and briefings, so both G14
// forms are gated on HomeContentDeliveryParam and only the bootstrap is started with it. Before
// the gate the deny refused the bootstrap's own mkdir and rename, and every run-as-you launch
// stopped at install_home_overlay. Every other argv in the plan must leave the deny standing.
func TestSelfModeBootstrapAloneMayDeliverHomeContent(t *testing.T) {
	plan := BuildRunPlanWithDaemons("/Users/you/code/alpha", jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{Tree: "/staged/overlay", Dests: []string{".claude/skills"},
			WorkspaceDirs: []string{".claude"}}, HostContext{}, jsonx.NewOrderedMap(), nil, nil,
		selfModeWireBridgeDaemons("/opt/yolo/bin/darwin-arm64"), FloorStage{Unmet: []string{"node"}},
		PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/"})
	// The negative loop below is vacuous for an argv the plan never built.
	if len(plan.JailDaemonArgv) == 0 || len(plan.ProvisionArgv) == 0 {
		t.Fatalf("the plan built no supervisor or provisioning argv to check: %v / %v",
			plan.JailDaemonArgv, plan.ProvisionArgv)
	}
	gate := `(if (not (param "` + HomeContentDeliveryParam + `"))` + "\n"
	for _, id := range []string{"home-content-write-deny", "home-content-anchor-deny"} {
		if !strings.Contains(plan.Seatbelt, gate+";; #seatbelt-test-id:"+id+"#\n") {
			t.Errorf("%s is not gated on %s, so the bootstrap cannot deliver what it protects:\n%s",
				id, HomeContentDeliveryParam, plan.Seatbelt)
		}
	}
	lift := HomeContentDeliveryParam + "=1"
	if !containsArgRun(plan.BootstrapArgv, []string{"/usr/bin/sandbox-exec", "-f", plan.ProfilePath, "-D", lift}) {
		t.Errorf("the bootstrap is not started with -D %s: %v", lift, plan.BootstrapArgv)
	}
	v := reflect.ValueOf(plan)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == "BootstrapArgv" {
			continue
		}
		var argvs [][]string
		switch f := v.Field(i).Interface().(type) {
		case []string:
			argvs = [][]string{f}
		case [][]string:
			argvs = f
		}
		for _, a := range argvs {
			if slices.Contains(a, lift) {
				t.Errorf("%s lifts the home-content deny, which only the bootstrap may: %v", name, a)
			}
		}
	}
}
