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

// TestRunPlanStagesAndNamesThePackRoot: the end-to-end shape of the fix, at the plan
// level. Before it, a macos-user launch had NO pack root anywhere in it — no stage
// command, no env var — and reported a successful bootstrap regardless.
func TestRunPlanStagesAndNamesThePackRoot(t *testing.T) {
	plan := planWithPacks(t, hostStaged)

	want := StagedPackRoot(cnameFor("/Users/Shared/yolo/proj"), "")
	if plan.PackRoot != want {
		t.Fatalf("plan.PackRoot = %q, want %q", plan.PackRoot, want)
	}
	if !strings.HasPrefix(plan.PackRoot, stateDir+"/") {
		t.Errorf("pack root %q is not under the root-owned state dir %q — the sandbox "+
			"could rewrite a manifest it renders from", plan.PackRoot, stateDir)
	}
	// The tree is copied from the host staging root, made world-readable, and moved
	// into place (the sandbox uid is not the invoking user, so a+rX is load-bearing).
	var sawCopy, sawChmod, sawMove bool
	for _, c := range plan.StageCommands {
		switch {
		case len(c) >= 4 && c[0] == cpBin && c[2] == hostStaged:
			sawCopy = true
		case len(c) >= 3 && c[0] == chmodBin && c[2] == "a+rX":
			sawChmod = true
		case len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == plan.PackRoot:
			sawMove = true
		}
	}
	if !sawCopy || !sawMove {
		t.Errorf("the pack tree is never staged into %s (copy=%v move=%v): %v",
			plan.PackRoot, sawCopy, sawMove, plan.StageCommands)
	}
	if !sawChmod {
		t.Errorf("the staged pack tree is never made readable to the sandbox uid: %v",
			plan.StageCommands)
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

	// Drop the pack staging commands, leaving the env var in place.
	var kept [][]string
	for _, c := range plan.StageCommands {
		if len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == plan.PackRoot {
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

// TestStagePackCommandsReplaceRatherThanNest: `mv src dst` moves src INSIDE dst when dst
// is an existing directory, so a stage that skipped the destination removal would bury
// the pack tree one level deeper on every launch — and the bootstrap would find an empty
// root from the second launch onward. Pinned because the failure is invisible on a first
// run, which is the only run a Mac-side smoke test is likely to do.
func TestStagePackCommandsReplaceRatherThanNest(t *testing.T) {
	cmds := StagePackCommands(hostStaged, "proj", "")
	dst := StagedPackRoot("proj", "")

	removedDst, moved := -1, -1
	for i, c := range cmds {
		if len(c) >= 3 && c[0] == rmBin && c[2] == dst {
			removedDst = i
		}
		if len(c) >= 3 && c[0] == mvBin && c[len(c)-1] == dst {
			moved = i
		}
	}
	if removedDst < 0 || moved < 0 {
		t.Fatalf("stage commands do not remove-then-move the destination: %v", cmds)
	}
	if removedDst > moved {
		t.Errorf("the destination is removed AFTER the move (%d > %d) — the tree would "+
			"nest one level deeper each launch: %v", removedDst, moved, cmds)
	}
	// Everything it touches stays under the root-owned state dir; the two `rm -rf`s
	// are the reason that is asserted rather than assumed.
	for _, c := range cmds {
		for _, a := range c[1:] {
			if strings.HasPrefix(a, "/") && !strings.HasPrefix(a, stateDir+"/") && a != hostStaged {
				t.Errorf("stage command touches %q, outside the state dir %q: %v",
					a, stateDir, c)
			}
		}
	}
	if got := StagePackCommands("", "proj", ""); got != nil {
		t.Errorf("no host tree should stage nothing, got %v", got)
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
