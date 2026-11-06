package run

// recoveryhatches_test.go pins the launch's RECOVERY-HATCH OUTPUT
// (docs/reference/patched-forks.md §8.5, §8.7; the maintainer's output ruling of 2026-10-16) at Run's own
// entry point: the missing-build refusal's whole text, its color through the launch's gate, the
// advance's Recovery list printed as is, patchrecovery.SkipExtensionsEnv leaving a build uncounted,
// and what a set hatch reaches at a fresh launch, an attach and in a jail.

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// missingTreeLaunch is a fresh launch whose patched extension, which pack agentpack loads, has no
// build: its build jail refused to start, for readOnlyCause's reason.
func missingTreeLaunch(t *testing.T, mutate func(*Options)) ([]string, string) {
	t.Helper()
	treeLaunchHome(t, true)
	return fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "its build jail refused to start on the host — the next " +
				"fresh launch tries again", Cause: readOnlyCause()}}
		}
		if mutate != nil {
			mutate(o)
		}
	})
}

// THE MISSING-BUILD REFUSAL'S WHOLE TEXT, color off: one ERROR line, the cause's facts, the Retry
// commands and the Start anyway list, each set off by a blank line, and nothing else. Red with
// Run's refuseMissingBuilds call deleted, or with any line of the ruled shape changed.
func TestTheMissingBuildRefusalGolden(t *testing.T) {
	argv, printed := missingTreeLaunch(t, nil)
	if argv != nil {
		t.Fatalf("the launch started its jail:\n%s", printed)
	}
	want := "\nERROR: 1 patched extension pack agentpack loads has no build on this machine\n" +
		"  extension treepack/tree-ext: its build jail refused to start on the host:\n" +
		"      writing tool's automode file (~/.tool/ext/automode/settings.json), which pack treepack declares, failed:\n" +
		"        ~/.tool/ext/automode is mounted read-only in that jail\n" +
		"    This is a bug in yolo, not in pack treepack: the build jail refused a config your own launch accepts.\n" +
		"    Report it at " + entrypoint.IssuesURL + ", with /ws/.yolo/build-treepack--tree-ext-0123abcd.log; once it is fixed, retry.\n" +
		"\n" +
		"Retry:\n" +
		"  yolo capture treepack/tree-ext\n" +
		"\n" +
		"Start anyway:\n" +
		"  YOLO_SKIP_EXTENSIONS=treepack/tree-ext yolo    start agentpack without it\n"
	if !strings.HasSuffix(printed, want) {
		t.Errorf("the refusal is not the golden text, ending the launch's output.\ngot:\n%s\nwant suffix:\n%s", printed, want)
	}
}

// COLOR ON, through the launch's gate (Options.pr): the ERROR line is bold red and every command —
// the Retry line and the hatch — bold cyan; NO_COLOR turns all of it off. Red with the refusal
// printing its commands through a writer that ignores the gate.
func TestTheMissingBuildRefusalColorsItsCommands(t *testing.T) {
	_, printed := missingTreeLaunch(t, func(o *Options) {
		o.Color = true
		o.IsTTYStderr = func() bool { return true }
	})
	for _, w := range []string{
		richtext.Render("[bold red]ERROR: 1 patched extension pack agentpack loads has no build on this machine[/bold red]", true),
		richtext.Render("  [bold cyan]yolo capture treepack/tree-ext[/bold cyan]", true),
		richtext.Render("  [bold cyan]YOLO_SKIP_EXTENSIONS=treepack/tree-ext yolo[/bold cyan]", true),
		richtext.Render("[bold]Start anyway:[/bold]", true),
	} {
		if !strings.Contains(printed, w) {
			t.Errorf("color on: the refusal lacks %q:\n%q", w, printed)
		}
	}
	_, printed = missingTreeLaunch(t, func(o *Options) {
		o.Color = true
		o.IsTTYStderr = func() bool { return true }
		withEnv(o, "NO_COLOR", "1")
	})
	if i := strings.Index(printed, "ERROR: 1 patched"); i < 0 || strings.Contains(printed[i:], "\x1b[") {
		t.Errorf("NO_COLOR: the refusal is colored, or missing:\n%q", printed)
	}
}

// THE ADVANCE'S RECOVERY LIST is printed as is, in patchrecovery.Scoped's order, in place of the
// fallback hatch. Red with missingRecovery ignoring a build's Recovery.
func TestTheMissingBuildRefusalPrintsTheAdvancesRecovery(t *testing.T) {
	treeLaunchHome(t, true)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "its build of v1.0.0 (0123abcd) failed on the host (exit 2)",
				Recovery: []patchrecovery.Hatch{
					{Env: patchrecovery.SkipPatchesEnv, Value: treeKey, Command: "yolo", Label: "run upstream without the patches"},
					{Env: patchrecovery.AllowOlderUpstreamEnv, Value: treeKey, Command: "yolo", Label: "build on an older release"},
				}}}
		}
	})
	want := "\nStart anyway:\n" +
		"  YOLO_ALLOW_OLDER_UPSTREAM=treepack/tree-ext yolo    build on an older release\n" +
		"  YOLO_SKIP_PATCHES=treepack/tree-ext yolo            run upstream without the patches\n"
	if !strings.HasSuffix(printed, want) || strings.Contains(printed, patchrecovery.SkipExtensionsEnv+"=") {
		t.Errorf("the refusal does not list the advance's hatches alone, in order:\n%s", printed)
	}
}

// YOLO_SKIP_EXTENSIONS COVERING THE EXTENSION leaves it out of the count: nothing is refused. Red with
// missingBuilds not reading the hatch.
func TestASkippedExtensionIsNotAMissingBuild(t *testing.T) {
	for _, value := range []string{"1", treeKey, "treepack", "tree-ext"} {
		_, printed := missingTreeLaunch(t, func(o *Options) { withEnv(o, patchrecovery.SkipExtensionsEnv, value) })
		if strings.Contains(printed, "has no build on this machine") {
			t.Errorf("%s=%s: the skipped extension was still counted:\n%s", patchrecovery.SkipExtensionsEnv, value, printed)
		}
	}
	_, printed := missingTreeLaunch(t, func(o *Options) { withEnv(o, patchrecovery.SkipExtensionsEnv, "otherpack") })
	if !strings.Contains(printed, "ERROR: 1 patched extension pack agentpack loads has no build on this machine") {
		t.Errorf("a name covering another extension skipped this one:\n%s", printed)
	}
}

// A PATCH FAILURE'S BLOCK prints the advance's Recovery as is, and a covering name continues in one
// line. Red with forkPatchBypass ignoring the delivery's Recovery.
func TestAPatchFailurePrintsTheAdvancesRecovery(t *testing.T) {
	patchedLaunchHome(t)
	recovery := []patchrecovery.Hatch{
		{Env: patchrecovery.AllowPatchFailuresEnv, Value: "forkpack/tool", Command: "yolo", Label: "run v1.1.0 + 1 patch"},
		{Env: patchrecovery.SkipPatchesEnv, Value: "forkpack/tool", Command: "yolo", Label: "run upstream without the patches"},
	}
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "k-good", Runs: "v1.1.0 (0a1b2c3d) + 1 patch",
				PatchFailure: pfFailure(), Recovery: recovery}}
		}
	})
	want := "\nStart anyway:\n" +
		"  YOLO_ALLOW_PATCH_FAILURES=forkpack/tool yolo    run v1.1.0 + 1 patch\n" +
		"  YOLO_SKIP_PATCHES=forkpack/tool yolo            run upstream without the patches\n"
	if !strings.Contains(printed, want) {
		t.Errorf("the block does not print the advance's hatches as is:\n%s", printed)
	}
}

// A HATCH NAME COVERING NO SELECTED PATCHED BUILD is one warning at a fresh launch, never a
// refusal; a covering one says nothing. Red with Run's noteRecoveryHatches call deleted.
func TestAnUnmatchedHatchNameWarnsAtAFreshLaunch(t *testing.T) {
	patchedLaunchHome(t)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "k-good"}}
		}
		withEnv(o, patchrecovery.SkipPatchesEnv, "typo, tool")
	})
	if argv == nil {
		t.Fatalf("an unmatched hatch name refused the launch:\n%s", printed)
	}
	want := "Warning: YOLO_SKIP_PATCHES names typo, which matches no patched build here (patched builds: forkpack/tool)\n"
	if strings.Count(printed, want) != 1 {
		t.Errorf("the unmatched name is not warned once:\n%s", printed)
	}
}

// ON AN ATTACH a set hatch does nothing, said in one line, except the two with their own lines.
// Red with attachExisting's noteRecoveryHatches call deleted.
func TestASetHatchSaysItDoesNothingOnAnAttach(t *testing.T) {
	patchedLaunchHome(t)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		cname := yoloruntime.FromWorkspace(o.Workspace)
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		withEnv(o, patchrecovery.SkipPatchesEnv, "typo")
		withEnv(o, patchrecovery.SkipExtensionsEnv, "1")
		writeAttachFixtureLegacyHTTPEvidence(t, o, cname)
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach:\n%s", printed)
	}
	if !strings.Contains(printed, "YOLO_SKIP_PATCHES has no effect on an attach; it applies at the next fresh launch\n") ||
		strings.Contains(printed, "YOLO_SKIP_EXTENSIONS has no effect") || strings.Contains(printed, "matches no patched build") {
		t.Errorf("the attach does not say what the hatch reaches, once:\n%s", printed)
	}
}

// IN A JAIL a set hatch does nothing, said in one line. Red with noteRecoveryHatches not asking
// whether it runs in a jail.
func TestASetHatchSaysItDoesNothingInAJail(t *testing.T) {
	patchedLaunchHome(t)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		withEnv(o, patchrecovery.AllowOlderUpstreamEnv, "1")
	})
	if !strings.Contains(printed, "YOLO_ALLOW_OLDER_UPSTREAM has no effect in a jail\n") {
		t.Errorf("the in-jail launch does not say the hatch does nothing there:\n%s", printed)
	}
}
