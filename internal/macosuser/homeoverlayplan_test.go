package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The macos-user half of content delivery, asserted through the PLAN — the same
// shape and the same reason as packroot_test.go: the Mac-side execution is
// unverifiable from Linux, but everything that DECIDES what will be executed is a
// pure value.
//
// THIS FILE EXISTS BECAUSE THE FEATURE SHIPPED WITHOUT IT. The composed overlay had
// three tests, all on the tree BUILDER (internal/cli/run), and nothing at all pinning
// that the tree reaches the run plan — so the staging commands and the env var could
// both have been deleted with a green suite. That is the "pins the callee while the
// call site is unpinned" shape AGENTS.md names, in code written by someone citing it.
// Found in review by a second agent, not by the suite.

func planWithOverlay(t *testing.T, hostOverlay string) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", hostOverlay,
		jsonx.NewOrderedMap(), nil, nil)
}

const hostOverlayTree = "/Users/matt/.local/share/yolo-jail/agents/proj/home-overlay"

// A composed overlay must be STAGED and NAMED — copied somewhere the sandbox uid can
// read, and pointed at by the bootstrap env. Missing either half means the agent
// starts with no AGENTS.md and no skills, which is the state this feature ended.
func TestRunPlanStagesAndNamesTheHomeOverlay(t *testing.T) {
	plan := planWithOverlay(t, hostOverlayTree)

	want := StagedHomeOverlay(cnameFor("/Users/Shared/yolo/proj"), "")
	var staged bool
	for _, cmd := range plan.StageCommands {
		if strings.Join(cmd, " ") == cpBin+" -R "+hostOverlayTree+" "+want+".new" {
			staged = true
		}
	}
	if !staged {
		t.Errorf("no command copies the composed overlay to %s:\n%v", want, plan.StageCommands)
	}
	if !anyHasPrefix(plan.BootstrapArgv, "YOLO_DARWIN_HOME_OVERLAY=") {
		t.Errorf("the bootstrap is never told where the overlay is:\n%v", plan.BootstrapArgv)
	}
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_DARWIN_HOME_OVERLAY=") &&
			strings.TrimPrefix(a, "YOLO_DARWIN_HOME_OVERLAY=") != want {
			t.Errorf("bootstrap points at %q, want the STAGED copy %q — naming the host "+
				"path would send the sandbox somewhere it cannot read", a, want)
		}
	}
	// Root-owned, for the same reason the pack root is: the overlay carries skills and
	// briefing prose the agent FOLLOWS, so a sandbox-writable copy would let a jail
	// rewrite its own instructions between launches.
	if !strings.HasPrefix(want, stateDir+"/") {
		t.Errorf("overlay %q is not under the root-owned state dir %q", want, stateDir)
	}
}

// No overlay → no staging command and no env var. Absence is how a launch with
// nothing to deliver says so; a variable naming a directory that was never staged
// would make an empty delivery indistinguishable from a broken one.
func TestRunPlanOmitsTheOverlayWhenThereIsNothingToDeliver(t *testing.T) {
	plan := planWithOverlay(t, "")

	for _, cmd := range plan.StageCommands {
		if strings.Contains(strings.Join(cmd, " "), homeOverlayLeaf) {
			t.Errorf("staged an overlay that does not exist: %v", cmd)
		}
	}
	if anyHasPrefix(plan.BootstrapArgv, "YOLO_DARWIN_HOME_OVERLAY=") {
		t.Errorf("named an overlay that does not exist:\n%v", plan.BootstrapArgv)
	}
}

// The overlay is staged with the same replace-not-merge shape as the packs: a
// destination that LEFT the config must stop being delivered, and a merge would keep
// serving a removed pack's skills forever.
func TestHomeOverlayStagingReplacesRatherThanMerges(t *testing.T) {
	cmds := StageHomeOverlayCommands(hostOverlayTree, "proj", "")
	joined := make([]string, 0, len(cmds))
	for _, c := range cmds {
		joined = append(joined, strings.Join(c, " "))
	}
	all := strings.Join(joined, "\n")
	dst := StagedHomeOverlay("proj", "")
	if !strings.Contains(all, rmBin+" -rf "+dst+"\n") {
		t.Errorf("the destination is not removed before the move — a removed pack's "+
			"content would survive:\n%s", all)
	}
	if !strings.Contains(all, mvBin+" -f "+dst+".new "+dst) {
		t.Errorf("the staged tree is not moved into place atomically:\n%s", all)
	}
	// World-readable, or the sandbox uid cannot read what root just staged.
	if !strings.Contains(all, chmodBin+" -R a+rX "+dst+".new") {
		t.Errorf("the staged overlay is never made readable to the sandbox:\n%s", all)
	}
}
