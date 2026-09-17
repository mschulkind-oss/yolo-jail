package macosuser

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// launchwriter_test.go pins the seam that puts this backend's half of the launch stream
// into <workspace>/.yolo/launch.log: the run pipeline publishes the writer its tee is
// installed on, and RealDeps resolves Out from it
// (docs/plans/handoff-macos-user-open-threads.md §7).
//
// The END-TO-END pin — a line this backend printed, in the file, after a real Run — is
// TestTheMacosUserBackendPrintsIntoTheLaunchLog in internal/cli/run, because that is
// where the tee and the pipeline are. These two hold the halves that package cannot see:
// that Out is resolved rather than hardcoded, and that a caller with no launch behind it
// still gets the process's own stdout.

// TestRealDepsPrintsThroughThePublishedLaunchWriter fails the moment `Out: LaunchWriter()`
// goes back to `Out: os.Stdout` — the state every macos-user launch shipped in until
// 2026-09-17, in which the dry-run plan, the Seatbelt profile, the three argvs and every
// setup line existed only in a scrollback.
//
// It asserts through the PRINTER rather than on the field alone: Out is the field, and
// `out.print` is the act — a Deps whose writer is right while the printing bypasses it
// would be exactly as absent from the log.
func TestRealDepsPrintsThroughThePublishedLaunchWriter(t *testing.T) {
	var buf bytes.Buffer
	undo := SetLaunchWriter(&buf)
	defer undo()

	deps := RealDeps(nil, nil, false)
	if deps.Out != &buf {
		t.Fatalf("RealDeps().Out is %T, not the writer the launch published: this backend's "+
			"disclosures do not reach launch.log", deps.Out)
	}
	printer{w: deps.Out, color: deps.Color}.print("[bold]sandbox profile installed[/bold]")
	if got := buf.String(); !strings.Contains(got, "sandbox profile installed") {
		t.Errorf("the backend's own printer did not reach the published writer: %q", got)
	}
}

// TestNoPublishedWriterLeavesTheMacosCommandsOnTheProcess is the trap §7 names, asserted
// rather than assumed: `yolo macos-setup`, `macos-teardown`, `macos-unshare` and
// `macos-fix-permissions` build their Deps from this same RealDeps with no pipeline
// behind them (internal/cli/commands.go), so the fallback is their entire output.
//
// It also pins the UNDO, which is what keeps the publication scoped to one launch: a
// writer left published outlives the log file it tees into.
func TestNoPublishedWriterLeavesTheMacosCommandsOnTheProcess(t *testing.T) {
	if got := LaunchWriter(); got != os.Stdout {
		t.Fatalf("a launch writer is published with no launch running (%T), so this test "+
			"cannot tell the fallback from a leak", got)
	}
	undo := SetLaunchWriter(&bytes.Buffer{})
	undo()
	if deps := RealDeps(nil, nil, false); deps.Out != os.Stdout {
		t.Errorf("after the undo, RealDeps().Out is %T: a `yolo macos-*` command would print "+
			"into a finished launch's log instead of the terminal", deps.Out)
	}
}
