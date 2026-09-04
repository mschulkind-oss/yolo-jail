package macosuser

import (
	"strings"
	"testing"

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
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostPackRoot, "", jsonx.NewOrderedMap(), nil, nil)
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
	for _, c := range plan.StageCommands {
		if len(c) > 0 && c[0] == rmBin {
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
