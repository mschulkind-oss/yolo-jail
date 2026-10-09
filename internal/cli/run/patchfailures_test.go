package run

// patchfailures_test.go pins A JAIL LAUNCH'S PATCH FAILURE (patchfailures.go; docs/design/
// patched-forks.md PF-D81, PF-D83) at its call site, from Run's own entry point: a selected patched
// fork or a loaded patched extension whose series does not apply refuses a fresh launch even when an
// intact admitted build of it would serve, its error block first and copyable; the patch failure's
// own bypass runs that build for the launch, said; the missing-program hatch does not waive it.

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const pfCommit = "c5c0f6bd0123456789abcdef0123456789abcdef"

func pfFailure() *packsrc.PatchFailure {
	return &packsrc.PatchFailure{Owner: "forkpack/tool", Target: packsrc.ListEntry{Commit: pfCommit, Tag: "v1.2.0"},
		Kind: "conflict", Member: "0001-ten.patch", Paths: []string{"f.txt"}}
}

// withEnv sets one variable in o's environment, as the user's shell would.
func withEnv(o *Options, key, value string) {
	prev := o.Getenv
	o.Getenv = func(k string) string {
		if k == key {
			return value
		}
		return prev(k)
	}
}

// servingForkWithFailure is the slot's answer for a patched fork whose newer upstream does not take
// the series while an intact admitted build serves.
func servingForkWithFailure(o *Options) {
	o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
		return map[string]entrypoint.ForkDelivery{"tool": {Key: "k-good", Runs: "v1.1.0 (0a1b2c3d) + 1 patch",
			PatchFailure: pfFailure()}}
	}
}

// THE REFUSAL, BEFORE THE IMAGE, WHATEVER THE COMMAND: an older build that serves does not turn a
// patch failure into a warning (PF-D81). Red with Run's refusePatchFailures call deleted, which boots
// the jail on the older build.
func TestAPatchFailureWithABuildToRunRefusesTheLaunch(t *testing.T) {
	for _, args := range [][]string{{"tool"}, {"bash"}, nil} {
		patchedLaunchHome(t)
		imaged := false
		argv, printed := fakePodmanLaunch(t, func(o *Options) {
			o.Args = args
			servingForkWithFailure(o)
			o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
				imaged = true
				return image.LoadResult{OK: true, Ref: goldenImageRef}
			}
		})
		if argv != nil || imaged {
			t.Fatalf("args %q: the launch went on to the image (%v) or the container (%v):\n%s", args, imaged, argv != nil, printed)
		}
		for _, w := range []string{
			"ERROR: fork forkpack/tool: patch application failed at upstream v1.2.0 (" + pfCommit + ")\n",
			"  Patch: 0001-ten.patch\n  Conflict: f.txt\n  Operation stopped; no older fit or base will be built.\n",
			"  Repair: yolo pack rebase forkpack/tool --onto " + pfCommit + "\n",
			"  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo\n",
			"(runs the intact admitted build v1.1.0 (0a1b2c3d) + 1 patch for this one run",
			"Refusing to launch: the patch series of fork forkpack/tool does not apply (the ERROR above)",
		} {
			if !strings.Contains(printed, w) {
				t.Errorf("args %q: the launch lacks %q:\n%s", args, w, printed)
			}
		}
		if strings.Contains(printed, "CONTINUING") {
			t.Errorf("args %q: the refused launch says it continues:\n%s", args, printed)
		}
	}
}

// THE BYPASS runs the intact admitted build for this one launch and says so under the block. Red with
// the bypass's read deleted, or its CONTINUING line.
func TestThePatchFailureBypassRunsTheAdmittedBuild(t *testing.T) {
	patchedLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		servingForkWithFailure(o)
		withEnv(o, paths.AllowPatchFailuresEnv, "1")
	})
	if argv == nil {
		t.Fatalf("the bypass did not start the jail:\n%s", printed)
	}
	if !strings.Contains(printed, "ERROR: fork forkpack/tool: patch application failed") ||
		!strings.Contains(printed, "CONTINUING: YOLO_ALLOW_PATCH_FAILURES=1 is set — this launch runs the intact "+
			"admitted build v1.1.0 (0a1b2c3d) + 1 patch of fork forkpack/tool") ||
		strings.Contains(printed, "Refusing to launch") {
		t.Errorf("the bypass does not say the failure and what it runs:\n%s", printed)
	}
	if strings.Index(printed, "ERROR: fork forkpack/tool") > strings.Index(printed, "CONTINUING: YOLO_ALLOW_PATCH") {
		t.Errorf("the CONTINUING line precedes the error it continues past:\n%s", printed)
	}
}

// THE MISSING-PROGRAM HATCH DOES NOT WAIVE IT (PF-D83): a build of it is here, so leaving it out is not
// what the block offers; the refusal says why. Red with the refusal reading that hatch.
func TestTheMissingProgramHatchDoesNotWaiveAPatchFailureWithABuild(t *testing.T) {
	patchedLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		servingForkWithFailure(o)
		allowMissingPrograms(o)
	})
	if argv != nil {
		t.Fatalf("the missing-program hatch started a launch whose patch series does not apply:\n%s", printed)
	}
	if !strings.Contains(printed, paths.AllowMissingProgramsEnv+" does not leave it out: an intact build of it is on "+
		"this machine, and only "+paths.AllowPatchFailuresEnv+"=1 runs it.") {
		t.Errorf("the refusal does not say why the missing-program hatch does not apply:\n%s", printed)
	}
}

// AN OLDER RECORDED GOOD BUILD, of a recipe the series no longer is, is offered through cached-good
// recovery (PF-D82), not the patch failure's bypass, which has no build of the series to run. Red
// with forkPatchBypass ignoring CachedGood.
func TestAPatchFailureWithOnlyAnOlderGoodBuildOffersCachedGood(t *testing.T) {
	patchedLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: pfFailure().Error(), PatchFailure: pfFailure(),
				CachedGood: "v1.0.0 (99aa88bb) + 1 patch"}}
		}
	})
	if argv != nil {
		t.Fatalf("a launch with no build of its series started its jail:\n%s", printed)
	}
	if !strings.Contains(printed, "  Bypass: YOLO_USE_CACHED_GOOD=forkpack/tool yolo\n") ||
		!strings.Contains(printed, "(runs the older admitted build v1.0.0 (99aa88bb) + 1 patch for this one fresh launch") ||
		strings.Contains(printed, "Bypass: YOLO_ALLOW_PATCH_FAILURES") {
		t.Errorf("the block does not offer cached-good recovery alone:\n%s", printed)
	}
}

// A PATCHED EXTENSION a selected agent pack loads is refused the same way; one no agent pack loads in
// a jail stops nothing, its block saying so. Red with launchPatchFailures' tree half deleted.
func TestAPatchedExtensionsPatchFailureRefusesOnlyWhereItIsLoaded(t *testing.T) {
	for _, listed := range []bool{true, false} {
		treeLaunchHome(t, listed)
		argv, printed := fakePodmanLaunch(t, func(o *Options) {
			o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
				dir := PatchedTreeCopyDir(r.CopyRoot, treeKey)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				pf := pfFailure()
				pf.Owner = treeKey
				return map[string]TreeDelivery{treeKey: {Dir: dir, Entry: "e1", Commit: strings.Repeat("a", 40), Patches: 1,
					PatchFailure: pf}}
			}
		})
		if refused := argv == nil; refused != listed {
			t.Errorf("listed %v: refused %v:\n%s", listed, refused, printed)
		}
		if !strings.Contains(printed, "ERROR: extension "+treeKey+": patch application failed at upstream v1.2.0") {
			t.Errorf("listed %v: no error block:\n%s", listed, printed)
		}
		if !listed && !strings.Contains(printed, "Bypass: none needed — no selected agent pack loads it here") {
			t.Errorf("an extension nothing loads does not say the launch goes on:\n%s", printed)
		}
		if listed && !strings.Contains(printed, "Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo\n") {
			t.Errorf("a loaded extension's block does not offer its bypass:\n%s", printed)
		}
	}
}
