package macosuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The macos-user half of B-0: the staged pack tree has to (a) be copied somewhere the
// sandbox uid can read, (b) be named to the bootstrap as YOLO_PACK_ROOT, and (c) be
// root-owned, for the same reason the container mounts /ctx/packs :ro — a pack manifest
// is an INPUT to composition, so an agent that could rewrite one could grant its own
// pack a host file on the next launch.
//
// All three are asserted through the PLAN, which is the whole point of the plan being a
// pure value: the Mac-side execution is unverifiable from Linux, but everything that
// DECIDES what will be executed is not.

const hostStaged = "/Users/matt/.local/share/yolo-jail/agents/proj/packs"

func planWithPacks(t *testing.T, hostPackRoot string) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostPackRoot, HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil)
}

func TestTwoLaunchPackTreesHaveDifferentGuestRoots(t *testing.T) {
	a := planWithPacks(t, "/host/pack-trees/20261008T120000Z-1111111111")
	b := planWithPacks(t, "/host/pack-trees/20261008T120000Z-2222222222")
	if a.Cname != b.Cname {
		t.Fatal("fixture must represent the same workspace")
	}
	if a.PackRoot == b.PackRoot {
		t.Fatalf("second launch replaces first launch's pack root: %s", a.PackRoot)
	}
	for _, p := range []RunPlan{a, b} {
		if !containsArg(p.BootstrapArgv, "YOLO_PACK_ROOT="+p.PackRoot) {
			t.Fatal("bootstrap lost its own tree")
		}
		if !SandboxEnvFileSets(p.EnvFileContent, "YOLO_PACK_ROOT", p.PackRoot) {
			t.Fatal("session lost its own tree")
		}
		if problems := PlanInvariants(p); len(problems) != 0 {
			t.Fatalf("invalid plan: %v", problems)
		}
	}
}

// TestRunPlanStagesAndNamesThePackRoot: the end-to-end shape of the fix, at the plan
// level. Before it, a macos-user launch had NO pack root anywhere in it — no stage
// command, no env var — and reported a successful bootstrap regardless.
func TestRunPlanStagesAndNamesThePackRoot(t *testing.T) {
	plan := planWithPacks(t, hostStaged)

	want := StagedPackTreeRoot(cnameFor("/Users/Shared/yolo/proj"), hostStaged, "")
	if plan.PackRoot != want {
		t.Fatalf("plan.PackRoot = %q, want %q", plan.PackRoot, want)
	}
	if !strings.HasPrefix(plan.PackRoot, stateDir+"/") {
		t.Errorf("pack root %q is not under the root-owned state dir %q — the sandbox "+
			"could rewrite a manifest it renders from", plan.PackRoot, stateDir)
	}
	var sawCopy, sawChmod, sawReserve bool
	for _, c := range plan.StageCommands {
		switch {
		case len(c) == 4 && c[0] == cpBin && c[1] == "-R" && strings.TrimSuffix(c[2], string(os.PathSeparator)+".") == hostStaged && c[3] == plan.PackRoot:
			sawCopy = true
		case len(c) == 4 && c[0] == chmodBin && c[1] == "-R" && c[2] == "a+rX" && c[3] == plan.PackRoot:
			sawChmod = true
		case isPackTreeReservationCommand(c, plan.PackRoot):
			sawReserve = true
		}
	}
	if !sawCopy || !sawChmod || !sawReserve {
		t.Errorf("the pack tree is not exclusively copied into %s and made readable (copy=%v chmod=%v reserve=%v): %v",
			plan.PackRoot, sawCopy, sawChmod, sawReserve, plan.StageCommands)
	}
	// And the bootstrap is told where it is — the container's YOLO_PACK_ROOT contract,
	// which LoadJailPacks reads on both backends.
	if !containsArg(plan.BootstrapArgv, "YOLO_PACK_ROOT="+plan.PackRoot) {
		t.Errorf("YOLO_PACK_ROOT never reached the bootstrap argv: %v", plan.BootstrapArgv)
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a correct plan reports invariant violations: %v", probs)
	}
}

// TestRunPlanWithoutPacksNamesNoPackRoot: a launch that staged nothing must say so by
// ABSENCE rather than by pointing the bootstrap at a directory nothing created. A
// YOLO_PACK_ROOT naming a non-existent dir would read as "no packs" anyway — the
// difference is that this way the plan output distinguishes the two states.
func TestRunPlanWithoutPacksNamesNoPackRoot(t *testing.T) {
	plan := planWithPacks(t, "")

	if plan.PackRoot != "" {
		t.Errorf("plan.PackRoot = %q with no host tree staged, want empty", plan.PackRoot)
	}
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_PACK_ROOT=") {
			t.Errorf("bootstrap argv names a pack root nothing staged: %q", a)
		}
	}
	// Pack staging is the rm-then-mv under the PACKS leaf; the context dir is staged on every
	// launch (CX-D4), so an rm elsewhere is not this test's subject.
	packsLeaf := filepath.Dir(StagedPackRoot(plan.Cname, ""))
	for _, c := range plan.StageCommands {
		if len(c) > 2 && c[0] == rmBin && strings.HasPrefix(c[2], packsLeaf+"/") {
			t.Errorf("a pack-less launch still runs pack staging: %v", c)
		}
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a pack-less plan reports invariant violations: %v", probs)
	}
}

// TestPlanInvariantCatchesAnUnannouncedPackRoot is the guard on the guard: the plan
// invariants must FAIL a plan whose pack tree is staged but never named to the
// bootstrap. That is the exact silent shape B-0 had — everything present except the one
// link that makes it reachable — so it is the mutation the invariant exists to catch.
func TestPlanInvariantCatchesAnUnannouncedPackRoot(t *testing.T) {
	plan := planWithPacks(t, hostStaged)

	// Strip the env pair from the bootstrap argv, leaving the staging in place.
	var stripped []string
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_PACK_ROOT=") {
			continue
		}
		stripped = append(stripped, a)
	}
	plan.BootstrapArgv = stripped

	probs := PlanInvariants(plan)
	if len(probs) == 0 {
		t.Fatal("a plan that stages packs but never tells the bootstrap where they are " +
			"passed every invariant — the bootstrap would render zero pack surfaces and " +
			"report success")
	}
	if !strings.Contains(strings.Join(probs, " "), "YOLO_PACK_ROOT") {
		t.Errorf("the violation does not name the missing variable: %v", probs)
	}
}

// TestPlanInvariantCatchesAnUnstagedPackRoot is the other half: an env var naming a tree
// no command copies. Equally silent, and equally empty to the bootstrap.
func TestPlanInvariantCatchesAnUnstagedPackRoot(t *testing.T) {
	plan := planWithPacks(t, hostStaged)

	// Drop the pack content copy while leaving reservation and permissions in place.
	var kept [][]string
	for _, c := range plan.StageCommands {
		if len(c) == 4 && c[0] == cpBin && c[1] == "-R" && c[3] == plan.PackRoot {
			continue
		}
		kept = append(kept, c)
	}
	plan.StageCommands = kept

	probs := PlanInvariants(plan)
	if len(probs) == 0 {
		t.Fatal("a plan naming a pack root nothing stages passed every invariant")
	}
	if !strings.Contains(strings.Join(probs, " "), "nothing stages the pack tree") {
		t.Errorf("the violation does not name the missing staging: %v", probs)
	}
}

// TestStagePackCommandsReserveWithoutReplacing: every pack stage owns one destination derived
// from the existing host tree leaf. It reserves that path exclusively before copying the
// source contents and makes the finished copy readable without granting guest writes.
func TestStagePackCommandsReserveWithoutReplacing(t *testing.T) {
	cmds := StagePackCommands(hostStaged, "proj", "")
	dst := StagedPackTreeRoot("proj", hostStaged, "")
	if len(cmds) != 4 || !isPackTreeReservationCommand(cmds[1], dst) ||
		!stagesPackTreeAt(cmds, dst) {
		t.Fatalf("stage commands do not exclusively reserve, copy and protect %s: %v", dst, cmds)
	}
	for _, c := range cmds {
		if c[0] == rmBin || c[0] == mvBin {
			t.Errorf("pack staging must not remove or replace an existing guest tree: %v", c)
		}
		for _, a := range c[1:] {
			if strings.HasPrefix(a, "/") && !strings.HasPrefix(a, stateDir+"/") && a != hostStaged+"/." {
				t.Errorf("stage command touches %q, outside the state dir %q and host source %q: %v",
					a, stateDir, hostStaged, c)
			}
		}
	}
	if got := StagePackCommands("", "proj", ""); got != nil {
		t.Errorf("no host tree should stage nothing, got %v", got)
	}
	if got := StagedPackTreeRoot("proj", "", ""); got != "" {
		t.Errorf("an empty host tree names guest artifacts: %q", got)
	}
}

// THE SESSION NAMES THE STAGED TREE AND THE WORKSPACE, the two facts an in-sandbox `yolo
// programs` or `yolo pack update` reads this jail by — exactly when the launch staged packs,
// and neither when it did not, the bootstrap's own absence rule.
func TestTheSessionNamesThePackRootAndWorkspaceWhenPacksAreStaged(t *testing.T) {
	plan := planWithPacks(t, hostStaged)
	if !SandboxEnvFileSets(plan.EnvFileContent, "YOLO_PACK_ROOT", plan.PackRoot) {
		t.Errorf("the session env file does not export YOLO_PACK_ROOT=%s:\n%s", plan.PackRoot, plan.EnvFileContent)
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, "YOLO_DARWIN_WORKSPACE", plan.Workspace) {
		t.Errorf("the session env file does not export YOLO_DARWIN_WORKSPACE=%s:\n%s", plan.Workspace, plan.EnvFileContent)
	}
	if probs := PlanInvariants(plan); len(probs) != 0 {
		t.Errorf("a plan naming both to the session reports invariant violations: %v", probs)
	}

	none := planWithPacks(t, "")
	for _, k := range []string{"YOLO_PACK_ROOT", "YOLO_DARWIN_WORKSPACE"} {
		if v, ok := sandboxEnvFileValue(none.EnvFileContent, k); ok {
			t.Errorf("a pack-less launch names %s=%q to the session, a tree nothing staged", k, v)
		}
	}
}

// AND THE IN-SANDBOX READER TAKES THEM AS THE BOOTSTRAP DID: the session's environment, read
// back through entrypoint.JailEnvFromOS (the reader `yolo programs` uses), resolves the plan's
// workspace — where the receipts are — and its staged pack tree, with the darwin seams set.
func TestTheSessionEnvReadsBackAsTheBootstrapsEnv(t *testing.T) {
	plan := planWithPacks(t, hostStaged)
	for _, k := range []string{"YOLO_PACK_ROOT", "YOLO_DARWIN_WORKSPACE", "YOLO_WORKSPACE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for _, k := range []string{"YOLO_PACK_ROOT", "YOLO_DARWIN_WORKSPACE"} {
		v, _ := sandboxEnvFileValue(plan.EnvFileContent, k)
		t.Setenv(k, v)
	}
	e := entrypoint.JailEnvFromOS()
	if e.WorkspaceDir() != plan.Workspace {
		t.Errorf("an in-sandbox verb resolves the workspace %q, want the plan's %q", e.WorkspaceDir(), plan.Workspace)
	}
	if e.Getenv("YOLO_PACK_ROOT") != plan.PackRoot {
		t.Errorf("an in-sandbox verb reads the pack root %q, want %q", e.Getenv("YOLO_PACK_ROOT"), plan.PackRoot)
	}
	if !e.SkipMCPPresets || e.ShimBinPath() != "/usr/bin" {
		t.Errorf("the in-sandbox Env is not the darwin translation (SkipMCPPresets=%v, shim bin %q)",
			e.SkipMCPPresets, e.ShimBinPath())
	}
}

// userConfig points the user config at a temp home holding body, so a plan reads the user
// scope this test states rather than the machine's.
func userConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bootstrapRelaysAutoprune reports whether a bootstrap argv carries the autoprune switch.
func bootstrapRelaysAutoprune(argv []string) bool {
	return containsArg(argv, entrypoint.OrphanAutopruneEnv+"=1")
}

// programs.autoprune REACHES A LAUNCH'S BOOTSTRAP FROM THE USER CONFIG, and from nowhere else:
// not from the workspace config an agent can edit, not into the agent's own environment, and
// never into a capture, whose staging home has nothing for a removal to be for. The container
// launch's rule (internal/cli/run's assembleRunCmd), on this backend.
func TestAutopruneReachesTheLaunchBootstrapFromTheUserConfigOnly(t *testing.T) {
	userConfig(t, `{"programs": {"autoprune": true}}`)
	plan := planWithPacks(t, hostStaged)
	if !bootstrapRelaysAutoprune(plan.BootstrapArgv) {
		t.Errorf("the user turned programs.autoprune on and the bootstrap argv lacks %s=1: %v",
			entrypoint.OrphanAutopruneEnv, plan.BootstrapArgv)
	}
	if _, ok := sandboxEnvFileValue(plan.EnvFileContent, entrypoint.OrphanAutopruneEnv); ok {
		t.Errorf("the switch reached the agent's session env file:\n%s", plan.EnvFileContent)
	}
	if capture := BuildCapturePlan(testCaptureOptions()); argvMentions(capture.BootstrapArgv, entrypoint.OrphanAutopruneEnv) {
		t.Errorf("a capture's bootstrap carries the autoprune switch: %v", capture.BootstrapArgv)
	}

	userConfig(t, `{}`)
	cfg, err := jsonx.Decode([]byte(`{"programs": {"autoprune": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := BuildRunPlan("/Users/Shared/yolo/proj", cfg.(*jsonx.OrderedMap), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil)
	if argvMentions(ws.BootstrapArgv, entrypoint.OrphanAutopruneEnv) {
		t.Errorf("a WORKSPACE config turned autoprune on: %v", ws.BootstrapArgv)
	}
	if off := planWithPacks(t, hostStaged); argvMentions(off.BootstrapArgv, entrypoint.OrphanAutopruneEnv) {
		t.Errorf("autoprune off, and the bootstrap argv still carries the switch: %v", off.BootstrapArgv)
	}
}

// AND NOT FROM THE SESSION ENV FILE EITHER, across the launch-to-bootstrap crossing. The file
// carries env_sources, the workspace's included, and an agent can write a dotenv file the
// workspace already lists, with no config prompt; launchEnv layers it last. So a user config
// that leaves autoprune off, and an env_sources value that turns it on, must leave the
// bootstrap's Env with it OFF: no relay on the argv (above), and none taken from the file
// (entrypoint's hydrate_session_env takes no YOLO_ name). The same for a pack tree: a launch
// that staged none must not have the bootstrap load one the file names.
func TestAnEnvSourcesValueCannotTurnOnAutopruneAtTheBootstrap(t *testing.T) {
	userConfig(t, `{}`)
	env := jsonx.NewOrderedMap()
	env.Set(entrypoint.OrphanAutopruneEnv, "1")
	env.Set("YOLO_PACK_ROOT", "/Users/Shared/yolo/proj/agent-chosen-packs")
	env.Set("GITHUB_TOKEN", "ghp-not-a-real-token")
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo-jail/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil)
	if argvMentions(plan.BootstrapArgv, entrypoint.OrphanAutopruneEnv) {
		t.Fatalf("the bootstrap argv carries the autoprune switch with the user config off: %v",
			plan.BootstrapArgv)
	}
	if _, ok := sandboxEnvFileValue(plan.EnvFileContent, entrypoint.OrphanAutopruneEnv); !ok {
		t.Fatalf("the fixture does not reach the session env file, so nothing below is a result:\n%s",
			plan.EnvFileContent)
	}

	vars := bootstrapVars(t, plan.BootstrapArgv)
	file := filepath.Join(t.TempDir(), "session.env")
	if err := os.WriteFile(file, []byte(plan.EnvFileContent), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	vars[SandboxEnvFileEnv] = file // the plan's path is /var/yolo-jail; the bytes are the plan's
	vars["JAIL_HOME"], vars["HOME"] = home, home
	vars["YOLO_DARWIN_WORKSPACE"] = t.TempDir()
	vars["MISE_DATA_DIR"] = t.TempDir()
	delete(vars, entrypoint.DarwinHomeSidecarEnv)

	e := entrypoint.DarwinEnvFrom(vars, home)
	var term strings.Builder
	e.Stderr = &term
	_ = entrypoint.RunDarwinBootstrap(e, entrypoint.DarwinBootstrapOptions{})
	for _, k := range []string{entrypoint.OrphanAutopruneEnv, "YOLO_PACK_ROOT"} {
		if got := e.Getenv(k); got != "" {
			t.Errorf("%s = %q in the bootstrap's Env, taken from the agent's session env file\n%s",
				k, got, term.String())
		}
	}
	if e.Getenv("GITHUB_TOKEN") != "ghp-not-a-real-token" {
		t.Errorf("the bootstrap stopped reading ordinary env_sources values from the file\n%s", term.String())
	}
}
