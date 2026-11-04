package run

// missingbuilds_test.go pins THE LAUNCH'S REFUSAL OF A MISSING PATCHED BUILD (missingbuilds.go;
// docs/reference/pi-extensions.md PPX-D40, patched-forks.md PF-D77) at its call site, from Run's
// own entry point: a fresh launch whose patched extension a selected agent pack loads, or whose
// patched fork, has no build refuses before the image step, whatever its command, naming the cause
// once, who can fix it and the ways back; YOLO_ALLOW_MISSING_PROGRAMS=1 goes on with a warning; a
// build that serves refuses nothing; and a notch that builds nothing refuses nothing.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// allowMissingPrograms sets the bypass in o's environment, as the user's shell would.
func allowMissingPrograms(o *Options) {
	prev := o.Getenv
	o.Getenv = func(k string) string {
		if k == paths.AllowMissingProgramsEnv {
			return "1"
		}
		return prev(k)
	}
}

// readOnlyCause is the cause the maintainer's first patched launch met, as the build act hands it.
func readOnlyCause() *entrypoint.BuildCause {
	return &entrypoint.BuildCause{
		Lines: []string{"writing tool's automode file (~/.tool/ext/automode/settings.json), which pack treepack declares, failed:",
			"  ~/.tool/ext/automode is mounted read-only in that jail"},
		YoloBug: true, Packs: []string{"treepack"}, Log: "/ws/.yolo/build-treepack--tree-ext-0123abcd.log"}
}

// A MISSING PATCHED EXTENSION REFUSES THE LAUNCH BEFORE THE IMAGE, WHATEVER THE COMMAND: the
// program its owner installs, a shell, no command at all. Red with Run's refuseMissingBuilds call
// deleted, which boots the jail.
func TestAMissingPatchedExtensionRefusesTheLaunchBeforeTheImage(t *testing.T) {
	for _, args := range [][]string{{"tool", "-p", "hi"}, {"bash"}, nil} {
		treeLaunchHome(t, true)
		imaged := false
		argv, printed := fakePodmanLaunch(t, func(o *Options) {
			o.Args = args
			o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
				return map[string]TreeDelivery{treeKey: {Reason: "its build jail refused to start on the host — the next " +
					"fresh launch tries again", Cause: readOnlyCause()}}
			}
			o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
				imaged = true
				return image.LoadResult{OK: true, Ref: goldenImageRef}
			}
		})
		if argv != nil || imaged {
			t.Errorf("args %q: the launch went on to the image (%v) or the container (%v):\n%s", args, imaged, argv != nil, printed)
		}
		for _, w := range []string{
			"ERROR: 1 patched extension pack agentpack loads has no build on this machine\n",
			"\n  extension treepack/tree-ext: its build jail refused to start on the host:\n",
			"\n      writing tool's automode file (~/.tool/ext/automode/settings.json), which pack treepack declares, failed:\n",
			"\n        ~/.tool/ext/automode is mounted read-only in that jail\n",
			"This is a bug in yolo, not in pack treepack: the build jail refused a config your own launch accepts.",
			"Report it at " + entrypoint.IssuesURL + ", with /ws/.yolo/build-treepack--tree-ext-0123abcd.log; once it is " +
				"fixed, retry.",
			"\n\nRetry:\n  yolo capture treepack/tree-ext\n",
			"\n\nStart anyway:\n  " + patchrecovery.SkipExtensionsEnv + "=treepack/tree-ext yolo    start agentpack without it\n",
		} {
			if !strings.Contains(printed, w) {
				t.Errorf("args %q: the refusal lacks %q:\n%s", args, w, printed)
			}
		}
		if strings.Contains(printed, "Fix what it names") || strings.Contains(printed, " / ") {
			t.Errorf("args %q: a yolo bug is put to the user to fix, or lines are joined:\n%s", args, printed)
		}
	}
}

// THE BYPASS (JR-D3's variable, PPX-D40): the same account is a warning, and the launch goes on to
// its container, whose owner's launchers stop. Red with the bypass's read deleted.
func TestTheBypassLaunchesWithoutAMissingBuild(t *testing.T) {
	treeLaunchHome(t, true)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		allowMissingPrograms(o)
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "its build jail refused to start on the host", Cause: readOnlyCause()}}
		}
	})
	if argv == nil {
		t.Fatalf("the bypass did not start the jail:\n%s", printed)
	}
	if !strings.Contains(printed, "Warning: 1 patched extension pack agentpack loads has no build on this machine; "+
		"starting without it ("+paths.AllowMissingProgramsEnv+")\n") || strings.Contains(printed, "ERROR: 1 patched") ||
		strings.Contains(printed, patchrecovery.StartAnyway) {
		t.Errorf("the bypass does not say what it continues without:\n%s", printed)
	}
	if d := patchedTreesInArgv(t, argv)[treeKey]; !d.Stop || d.Cause == nil || !d.Cause.YoloBug {
		t.Errorf("the jail is handed %+v, want its owner stopped with the cause", d)
	}
}

// A MISSING PATCHED FORK REFUSES THE LAUNCH TOO (PF-D77), and a reason with no cause in plain words
// is said as it is, on its own line. Red with missingBuilds' fork half deleted.
func TestAMissingPatchedForkRefusesTheLaunch(t *testing.T) {
	patchedLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "upstream v1.1.0 does not take fork forkpack/tool's " +
				"patch series — `yolo pack status` prints the git commands that rebase the series onto the upstream it does not fit"}}
		}
	})
	if argv != nil {
		t.Fatalf("a launch with no build of its patched fork started its jail:\n%s", printed)
	}
	for _, w := range []string{
		"ERROR: 1 selected patched fork has no build on this machine\n",
		"\n  fork forkpack/tool: upstream v1.1.0 does not take fork forkpack/tool's patch series\n",
		"\n  Rebase: yolo pack series check\n",
		"\n\nRetry:\n  yolo capture tool\n",
		"\n\nStart anyway:\n  YOLO_ALLOW_MISSING_PROGRAMS=1 yolo    start without it\n",
	} {
		if !strings.Contains(printed, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, printed)
		}
	}
	// The other backends are macos-user's ways out (PF-D97), never a container launch's.
	if strings.Contains(printed, "YOLO_RUNTIME=") {
		t.Errorf("a podman launch's refusal names another runtime:\n%s", printed)
	}
}

// A MISSING FORK WHOSE CAUSE IS A PATCH FAILURE (PF-D81, PF-D83): the launch's error block comes
// first, its hatch list offering the missing-program hatch since no build is here to run; the
// refusal points at it rather than saying the target again, and offers neither the patch failure's
// own hatch, which has no build to run, nor a second rebase pointer. Red with missingBuilds' reason
// for a patch failure deleted, or with the block offering YOLO_ALLOW_PATCH_FAILURES.
func TestAMissingForksPatchFailureNamesItsOwnBypass(t *testing.T) {
	patchedLaunchHome(t)
	commit := "c5c0f6bd0123456789abcdef0123456789abcdef"
	pf := &packsrc.PatchFailure{Owner: "forkpack/tool", Target: packsrc.ListEntry{Commit: commit, Tag: "v1.2.0"},
		Kind: "conflict", Member: "0001-ten.patch", Paths: []string{"f.txt"}}
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: pf.Error(), PatchFailure: pf}}
		}
	})
	if argv != nil {
		t.Fatalf("a launch with no build of its patched fork started its jail:\n%s", printed)
	}
	for _, w := range []string{
		"ERROR: fork forkpack/tool: patch series does not apply to upstream v1.2.0 (c5c0f6bd)\n",
		"\nStart anyway:\n  YOLO_ALLOW_MISSING_PROGRAMS=1 yolo    start without it\n",
	} {
		if !strings.Contains(printed, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, printed)
		}
	}
	// One block and one hatch list: no second ERROR for the same fork.
	if n, m := strings.Count(printed, "ERROR: "), strings.Count(printed, "Start anyway:"); n != 1 || m != 1 {
		t.Errorf("the launch says %d ERROR lines and %d hatch lists, want one of each:\n%s", n, m, printed)
	}
	if strings.Contains(printed, paths.AllowPatchFailuresEnv) || strings.Contains(printed, "Rebase: yolo pack series check") {
		t.Errorf("the launch offers a hatch with no build to run, or a second rebase pointer:\n%s", printed)
	}
	// The block's header and its Repair recipe name the commit — the rebase, then the export's test,
	// --base and range — and nothing else repeats it.
	if n := strings.Count(printed, "c5c0f6bd"); n != 5 {
		t.Errorf("the launch says the commit %d times, want five (the block's header and its recipe):\n%s", n, printed)
	}
}

// WITH THE MISSING-PROGRAM HATCH SET the same launch goes on without the fork: its error block, then
// the one warning line, and no hatch list offering the hatch already set.
func TestAMissingForksPatchFailureWithItsHatchSetListsNothing(t *testing.T) {
	patchedLaunchHome(t)
	pf := &packsrc.PatchFailure{Owner: "forkpack/tool", Target: packsrc.ListEntry{Commit: strings.Repeat("c", 40), Tag: "v1.2.0"},
		Kind: "conflict", Member: "0001-ten.patch", Paths: []string{"f.txt"}}
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = func(key string) string {
			switch key {
			case "YOLO_SETUP":
				return testSetupForRuntime("podman")
			case paths.AllowMissingProgramsEnv:
				return "1"
			}
			return ""
		}
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: pf.Error(), PatchFailure: pf}}
		}
	})
	if argv == nil || strings.Contains(printed, "Start anyway:") ||
		!strings.Contains(printed, "starting without it ("+paths.AllowMissingProgramsEnv+")") {
		t.Errorf("the hatch-set launch did not start, or listed a hatch (started %v):\n%s", argv != nil, printed)
	}
}

// A MISSING BUILD WITH ANOTHER CAUSE does not name the patch failure's bypass.
func TestAMissingBuildWithoutAPatchFailureNamesNoPatchBypass(t *testing.T) {
	patchedLaunchHome(t)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "fork forkpack/tool's build of v1.1.0 failed"}}
		}
	})
	if !strings.Contains(printed, "ERROR: 1 selected patched fork has no build on this machine") ||
		strings.Contains(printed, paths.AllowPatchFailuresEnv) {
		t.Errorf("a build failure's refusal names the patch-failure bypass, or refuses nothing:\n%s", printed)
	}
}

// ONE CAUSE, ONCE (PPX-D42): two extensions one cause left without a build are said in one group,
// the cause's lines once; and an extension no agent pack loads refuses nothing, its cause said as a
// warning. Red with printMissingGroups' grouping deleted.
func TestOneCauseIsSaidOnceForEveryBuildItLeftWithout(t *testing.T) {
	hostLauncher(t) // a host launch: run inside a jail, YOLO_VERSION would make it an in-jail one
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { return nil }
	o.patchedTrees = []packload.Fork{
		{Pack: "treepack", Bin: "a", Into: ".tool/ext/a", Owner: "agentpack", ListedInJail: true},
		{Pack: "treepack", Bin: "b", Into: ".tool/ext/b", Owner: "agentpack", ListedInJail: true},
		{Pack: "treepack", Bin: "c", Into: ".tool/ext/c"},
	}
	reason := "its build jail refused to start on the host — the next fresh launch tries again"
	o.treeDelivered = map[string]TreeDelivery{
		"treepack/a": {Reason: reason, Cause: readOnlyCause()},
		"treepack/b": {Reason: reason, Cause: readOnlyCause()},
		"treepack/c": {Reason: reason, Cause: readOnlyCause()},
	}
	if !o.refuseMissingBuilds("podman") {
		t.Fatalf("two needed extensions with no build refused nothing:\n%s", stderr.String())
	}
	got := stderr.String()
	if n := strings.Count(got, "~/.tool/ext/automode is mounted read-only in that jail"); n != 2 {
		t.Errorf("the cause is said %d times, want once for the needed group and once for the unneeded:\n%s", n, got)
	}
	for _, w := range []string{
		"Warning: 1 patched build no selected agent pack loads has no build on this machine\n  extension treepack/c: ",
		"ERROR: 2 patched extensions pack agentpack loads have no build on this machine\n" +
			"  extensions treepack/a and treepack/b: their build jail refused to start on the host:\n",
		"once it is fixed, `yolo capture treepack/c`.\n",
		"\n\nRetry:\n  yolo capture treepack/a\n  yolo capture treepack/b\n",
		"\n\nStart anyway:\n  " + patchrecovery.SkipExtensionsEnv + "=treepack/a,treepack/b yolo    start agentpack without them\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, got)
		}
	}
}

// NOT WHERE NOTHING IS BUILT (PPX-D40's reading): below Apple Container's read-only floor no tree
// was built, so none failed, and the launch keeps its warning line; on podman the same answer
// refuses. Red if missingBuilds stops asking whether this notch builds.
func TestANotchThatBuildsNoTreeRefusesNothing(t *testing.T) {
	hostLauncher(t) // a host launch: run inside a jail, YOLO_VERSION would make it an in-jail one
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { return nil }
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto, Owner: "agentpack", ListedInJail: true}}
	o.treeDelivered = map[string]TreeDelivery{treeKey: {Reason: "no build, and this runtime builds none"}}
	if o.refuseMissingBuilds("container") || stderr.Len() != 0 {
		t.Errorf("below the floor the launch refused, or said so:\n%s", stderr.String())
	}
	if !o.refuseMissingBuilds("podman") {
		t.Error("on podman the same answer refused nothing")
	}
	// ABOVE THE FLOOR, Apple Container builds and refuses, and a build jail's stop names what that
	// runtime cannot do beside a running jail (PF-D21).
	stderr.Reset()
	o.acVersion = &acVersionProbe{v: "1.1.0", ok: true}
	o.treeDelivered = map[string]TreeDelivery{treeKey: {Reason: "its build jail exited before its build line ran on the host",
		Cause: &entrypoint.BuildCause{Lines: []string{"Error: the runtime would not start it"}}}}
	if !o.refuseMissingBuilds("container") || !strings.Contains(stderr.String(),
		"Apple Container: a build jail cannot start beside a running jail") {
		t.Errorf("above the floor the launch did not refuse, or name Apple Container's limit:\n%s", stderr.String())
	}
}

// A PATCHED FORK THAT BUILDS FOR NONE OF THE JAIL'S PLATFORM refuses nothing: no build was tried, so
// none failed (the header's "not where no tree or fork is built"), and its launcher keeps saying why.
// Red with missingBuilds counting a fork its platforms exclude.
func TestAPatchedForkForNoneOfThisPlatformRefusesNothing(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { return nil }
	fork := packload.Fork{Pack: "forkpack", Base: "toolpack", Bin: "tool", Patches: "patches",
		Platforms: []string{"darwin/arm64"}}
	o.forkPinned = []packload.ForkPin{{Fork: fork}}
	o.forkDelivered = o.forkDeliveriesFor("podman")
	if d := o.forkDelivered["tool"]; d.Key != "" || d.Reason == "" {
		t.Fatalf("the fork is handed %+v, want its platform reason", d)
	}
	if o.refuseMissingBuilds("podman") || stderr.Len() != 0 {
		t.Errorf("a fork no build was tried for refused the launch, or warned:\n%s", stderr.String())
	}
	// The same fork with this platform among its own, and no build, refuses.
	fork.Platforms = append(fork.Platforms, "linux")
	o.forkPinned = []packload.ForkPin{{Fork: fork}}
	o.forkDelivered = map[string]entrypoint.ForkDelivery{"tool": {Reason: "its build of v1 failed on the host (exit 2)"}}
	if !o.refuseMissingBuilds("podman") {
		t.Errorf("a fork for this platform with no build refused nothing:\n%s", stderr.String())
	}
}

// AN UNNEEDED MISSING BUILD ON APPLE CONTAINER names that runtime's limit too (PF-D21): the warning
// is the only place its cause is said. Red with the hint printed only for builds the launch needs.
func TestAnUnneededMissingBuildOnAppleContainerNamesItsLimit(t *testing.T) {
	hostLauncher(t) // a host launch: run inside a jail, YOLO_VERSION would make it an in-jail one
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { return nil }
	o.acVersion = &acVersionProbe{v: "1.1.0", ok: true}
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto}}
	o.treeDelivered = map[string]TreeDelivery{treeKey: {Reason: "its build jail exited before its build line ran on the host",
		Cause: &entrypoint.BuildCause{Lines: []string{"Error: the runtime would not start it"}}}}
	if o.refuseMissingBuilds("container") {
		t.Fatalf("a build no agent pack loads refused the launch:\n%s", stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "Warning: 1 patched build no selected agent pack loads") ||
		!strings.Contains(got, "Apple Container: a build jail cannot start beside a running jail") {
		t.Errorf("the warning does not name Apple Container's limit:\n%s", got)
	}
}

// A BUILD THE ACT LEFT UNSAID, with no cause in plain words — a failed build, a series no upstream
// takes — is said by the launch even when no agent pack loads it: the act printed nothing of it.
// Red with refuseMissingBuilds reading the cause alone for its warning.
func TestAnUnsaidBuildNoPackLoadsIsWarned(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // this predicate describes a host-only build act
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { return nil }
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto}}
	reason := "extension treepack/tree-ext's build of v1.0.0 (0123abcd) failed on the host (exit 2) — the next fresh " +
		"launch tries again, or `yolo capture treepack/tree-ext` now"
	o.treeDelivered = map[string]TreeDelivery{treeKey: {Reason: reason, Unsaid: true}}
	if o.refuseMissingBuilds("podman") {
		t.Fatalf("a build no agent pack loads refused the launch:\n%s", stderr.String())
	}
	if got := stderr.String(); strings.Count(got, "failed on the host (exit 2)") != 1 {
		t.Errorf("the unsaid build's reason is not said once:\n%s", got)
	}
}

// THE POOL'S MISSING BUILD REACHES THE REFUSAL ONCE: a launch wired as the CLI wires it, its whole
// fork-build slot one act (BuildSlot) and no half's own, whose pooled build failed and left its
// reason to the launch (Unsaid), refuses before the image step and says that reason once. Red with
// missingBuilds reading only the halves' acts, which a BuildSlot launch's refusal then never sees.
func TestAPooledBuildsFailureReachesTheRefusalOnce(t *testing.T) {
	treeLaunchHome(t, true)
	reason := "extension " + treeKey + "'s build of v1.0.0 (0123abcd) failed on the host (exit 2) — the next fresh " +
		"launch tries again, or `yolo capture " + treeKey + "` now"
	imaged := false
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees, o.BuildForks = nil, nil
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			if req.Trees == nil || len(req.Trees.Trees) != 1 {
				t.Errorf("the slot was handed %+v", req.Trees)
			}
			return nil, map[string]TreeDelivery{treeKey: {Reason: reason, Unsaid: true}}
		}
		o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
			imaged = true
			return image.LoadResult{OK: true, Ref: goldenImageRef}
		}
	})
	if argv != nil || imaged {
		t.Errorf("the launch went on to the image (%v) or the container (%v):\n%s", imaged, argv != nil, printed)
	}
	if !strings.Contains(printed, "ERROR: 1 patched extension pack agentpack loads has no build on this machine\n") {
		t.Errorf("the launch did not refuse:\n%s", printed)
	}
	if n := strings.Count(printed, "failed on the host (exit 2)"); n != 1 {
		t.Errorf("the launch says the pooled build's reason %d times, want once:\n%s", n, printed)
	}
	// The reason names its build already, so the label is not said twice before it.
	if !strings.Contains(printed, "\n  extension "+treeKey+"'s build of v1.0.0") ||
		strings.Contains(printed, treeKey+": extension "+treeKey) {
		t.Errorf("the refusal does not say the reason as it is, its build named once:\n%s", printed)
	}
}

// A FAILED BUILD'S REFUSAL NAMES ITS LOG even when the build's error says " — " itself: the reason
// carries the error and the log's path in parentheses, and the refusal's headline is cut at the dash
// after them (missingHeadline, entrypoint.CutReason), never at one inside them. Red with
// missingHeadline cutting at the first dash.
func TestAFailedBuildsRefusalKeepsItsLogPastADashInItsError(t *testing.T) {
	treeLaunchHome(t, true)
	const log = "/ws/.yolo/build-treepack--tree-ext.log"
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "extension " + treeKey + "'s build of v1.0.0 failed on the " +
				"host (extension " + treeKey + "'s build left no tree at ~/.local/share/yolo-tree/tree-ext — nothing " +
				"was stored; its log: " + log + ") — the next fresh launch tries again", Unsaid: true}}
		}
	})
	if argv != nil {
		t.Fatalf("a launch with no build of its patched extension started its jail:\n%s", printed)
	}
	if !strings.Contains(printed, "nothing was stored; its log: "+log+")\n") {
		t.Errorf("the refusal's headline drops the build's log:\n%s", printed)
	}
	if strings.Contains(printed, "the next fresh launch tries again") {
		t.Errorf("the refusal's headline keeps the act that tries again, which Retry names:\n%s", printed)
	}
}
