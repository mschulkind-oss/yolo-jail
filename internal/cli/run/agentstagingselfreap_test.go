package run

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// TestALaunchNeverReapsItsOwnStagingDir is the regression for a launch that
// deleted its own packs staging dir and then failed mounting it.
//
// THE SHAPE, because it is the interesting part: stagePacks writes
// AGENTS_DIR/<cname>/packs early in Run; the container is not created until the
// very end; the housekeeping slot sits between them. So at the instant the sweep
// judged orphans, the launching jail was neither live (no container yet) nor
// tracked (no tracking file yet) — and its own freshly staged directory looked
// exactly like an abandoned one. Measured on a real host as
// `Error: statfs …/agents/<cname>/packs: no such file or directory`.
//
// The age floor cannot cover this and it is worth knowing why: it reads
// AGENTS_DIR/<cname>'s mtime, while staging creates the `packs` CHILD. A
// relaunched workspace therefore presents a weeks-old parent with a seconds-old
// child, which is what the fixture below reproduces.
func TestALaunchNeverReapsItsOwnStagingDir(t *testing.T) {
	agents := t.TempDir()
	containers := t.TempDir()

	const launching = "yolo-ws-deadbeef"
	staged := filepath.Join(agents, launching, "packs")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "marker"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The parent is OLD — a dir left by a previous session — while `packs` inside
	// it is new. This is the state a relaunch actually presents.
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(agents, launching), old, old); err != nil {
		t.Fatal(err)
	}

	// Nothing is live and nothing is tracked: precisely the launch's own window.
	known := prune.TrackedContainerNames(containers)
	known[launching] = struct{}{}

	_, dirs, names := prune.PruneOrphanAgentStaging(agents, known, true, time.Hour, true, time.Now())

	if _, err := os.Stat(filepath.Join(staged, "marker")); err != nil {
		t.Fatalf("the launching jail's staged packs were reaped by its own launch: %v\n"+
			"reaped %d dir(s): %v", err, dirs, names)
	}
}

// TestReapSmallClassesSparesTheLaunchingJail is THE call-site test, and the three
// above are not substitutes for it.
//
// Those construct the `known` set themselves and call PruneOrphanAgentStaging
// directly, so they stay green while the housekeeping caller builds `known`
// wrongly — which is exactly the shape AGENTS.md warns about and exactly the bug
// that shipped: the function's contract was right and one of its two callers did
// not honor it. Verified by mutation: reverting the caller to live-only left all
// three passing.
//
// This one drives reapSmallAutomaticClasses and looks at the filesystem.
func TestReapSmallClassesSparesTheLaunchingJail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// This suite runs INSIDE a jail, where reapSmallAutomaticClasses returns
	// immediately — the automatic classes are the host's job. Without this the test
	// exercises nothing and the "spared" assertion passes for the wrong reason,
	// which is what it did on first run.
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(autoReapOptOutEnv, "")

	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	// The runtime answers, and reports NO live containers: the launching jail's
	// container does not exist yet, which is the whole window.
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0, Stdout: ""}
	}

	const launching = "yolo-ws-feedface"
	agents := filepath.Join(home, ".local", "share", "yolo-jail", "agents")
	staged := filepath.Join(agents, launching, "packs")
	orphan := filepath.Join(agents, "yolo-gone-11111111")
	for _, d := range []string{staged, orphan} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staged, "marker"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both parents are OLD, as a relaunched workspace's really is.
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, d := range []string{filepath.Join(agents, launching), orphan} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}

	o.reapSmallAutomaticClasses("podman", launching)

	if _, err := os.Stat(filepath.Join(staged, "marker")); err != nil {
		t.Errorf("the launch reaped the packs it had just staged: %v\n"+
			"stagePacks writes this dir early, the container is created last, and this slot "+
			"runs in between — so the launching cname must be in the known set", err)
	}
	// Not vacuous: the same pass must still reap a real orphan.
	if _, err := os.Stat(orphan); err == nil {
		t.Error("a genuinely orphaned staging dir survived, so this sweep has stopped working " +
			"and the assertion above would pass for the wrong reason")
	}
}

// TestAStoppedButTrackedJailKeepsItsStaging pins the half of
// PruneOrphanAgentStaging's contract that the automatic call site silently did not
// honor: "a name is an orphan only when it is neither live nor tracked". `yolo
// prune` built its known set as live ∪ tracked; the launch-path sweep used live
// alone, making the automatic pass strictly more destructive than the manual one.
func TestAStoppedButTrackedJailKeepsItsStaging(t *testing.T) {
	agents := t.TempDir()
	containers := t.TempDir()

	const stopped = "yolo-other-cafe1234"
	dir := filepath.Join(agents, stopped)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	// Stopped, but still tracked — its briefing is reusable on restart.
	if err := os.WriteFile(filepath.Join(containers, stopped), []byte("/ws"), 0o644); err != nil {
		t.Fatal(err)
	}

	known := prune.TrackedContainerNames(containers)
	// No live names at all, which is the point.
	_, dirs, names := prune.PruneOrphanAgentStaging(agents, known, true, time.Hour, true, time.Now())

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("a stopped-but-TRACKED jail lost its staging: %v (reaped %d: %v)", err, dirs, names)
	}
}

// TestATrulyOrphanedStagingDirIsStillReaped is the other polarity. Without it the
// two tests above could be satisfied by never reaping anything, which would make
// them a pair of very confident no-ops.
func TestATrulyOrphanedStagingDirIsStillReaped(t *testing.T) {
	agents := t.TempDir()
	containers := t.TempDir()

	orphan := filepath.Join(agents, "yolo-gone-00000000")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	known := prune.TrackedContainerNames(containers)
	known["yolo-ws-deadbeef"] = struct{}{} // a different launch
	_, dirs, _ := prune.PruneOrphanAgentStaging(agents, known, true, time.Hour, true, time.Now())

	if dirs != 1 {
		t.Errorf("want the unknown, aged, untracked dir reaped; reaped %d", dirs)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Error("the orphan survived — this sweep still has to do its job")
	}
}
