package run

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAnotherLaunchsSweepCannotReapThisLaunchsStaging is the regression for the half of
// the self-reap bug that adding the LAUNCHING cname to the known set could not reach.
//
// THE SHAPE: stagePacks writes AGENTS_DIR/<cname>/packs early in Run, and the dir is only
// protected BY NAME once runtimeWriteTracking records it — after the nix build, the
// briefing refresh, and AUTO-CAPTURE. A capture is a full in-process Run of its own
// (internal/cli.runCaptureJail), so its housekeeping slot sweeps with the CAPTURE jail's
// cname as `launchingCname`, and the outer jail is neither live, nor tracked, nor that
// name. Measured on a real host as four "declares a `files` tree that is not in its staged
// content" warnings followed by
// `Error: statfs …/agents/yolo-yolo-jail-887995ca/packs: no such file or directory`.
//
// What closes it is the AGE FLOOR, which already claims to cover "a jail mid-startup whose
// container/tracking record hasn't landed yet" and could not, because it reads
// AGENTS_DIR/<cname>'s mtime while staging creates the `packs` CHILD — so on a relaunch
// (where that child already exists) MkdirAll is a no-op and the parent keeps a weeks-old
// mtime. The fixture below builds exactly that state.
//
// ⚠ THIS IS A CALL-SITE TEST ON PURPOSE. It drives the real stagePacks and the real
// reapSmallAutomaticClasses and then looks at the filesystem; a test that asserted
// touchAgentStagingDir sets an mtime would stay green with the call deleted from
// stagePacks, which is the shape AGENTS.md warns about and the shape the previous fix's
// first three tests accidentally took.
func TestAnotherLaunchsSweepCannotReapThisLaunchsStaging(t *testing.T) {
	home := packHome(t)
	// This suite runs INSIDE a jail, where reapSmallAutomaticClasses returns immediately —
	// the automatic classes are the host's job. Without this the sweep does nothing and the
	// "spared" assertion passes for the wrong reason.
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(autoReapOptOutEnv, "")

	const outer = "yolo-yolo-jail-887995ca"
	const capture = "yolo-capture-0f0f0f0f"
	agents := filepath.Join(home, ".local", "share", "yolo-jail", "agents")

	// The RELAUNCH state: `packs` already exists, so stagePacks' MkdirAll is a no-op and
	// nothing in the staging path would otherwise move the parent's mtime.
	if err := os.MkdirAll(filepath.Join(agents, outer, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A genuine orphan, aged the same, so the assertions below cannot be satisfied by a
	// sweep that has simply stopped working.
	orphan := filepath.Join(agents, "yolo-gone-22222222")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, d := range []string{filepath.Join(agents, outer), orphan} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}

	// The OUTER launch stages. Nothing else about it has happened yet: no container, no
	// tracking file.
	outerOpts := &Options{Workspace: t.TempDir()}
	if _, _, _, err := outerOpts.stagePacks(outer); err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	marker := filepath.Join(agents, outer, "packs", "marker")
	if err := os.WriteFile(marker, []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The CAPTURE sub-launch's housekeeping slot, which knows only its own name.
	capOpts := &Options{}
	fillDefaults(capOpts)
	capOpts.Now = time.Now
	// The runtime answers and reports NO live containers: neither jail's container exists.
	capOpts.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0, Stdout: ""}
	}
	capOpts.reapSmallAutomaticClasses("podman", capture)

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a concurrent launch's sweep reaped the packs this launch had just staged: %v\n"+
			"the launching-cname set cannot cover a sub-launch, so stagePacks must stamp "+
			"AGENTS_DIR/<cname>'s own mtime and let the age floor do it", err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Error("a genuinely orphaned staging dir survived, so this sweep has stopped working " +
			"and the assertion above would pass for the wrong reason")
	}
}
