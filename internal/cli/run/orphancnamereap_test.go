package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// Every per-name file this package writes must parse as its cname's in the sweep's directory, or
// it leaks again with every sweep test green. A writer that changes its spelling, or a new
// per-name file, fails here.
//
// Both spellings of a jail name are pinned: the legacy one (the project key) and a setup's
// (runtime.JailName, named-setups-plan.md SUP-D2), whose files every launch now writes.
func TestEveryPerNameWriterParsesForTheOrphanSweep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, c := range []string{"yolo-some-workspace-0123abcd", "yolo-some-workspace-0123abcd-podman-self"} {
		everyPerNameWriterParses(t, c)
	}
}

func everyPerNameWriterParses(t *testing.T, c string) {
	t.Helper()
	dirs := prune.OrphanCnameDirsUnder(paths.GlobalStorage(), paths.BuildDir(), paths.ContainerDir())
	type spelled struct{ class, dir, path string }
	cases := []spelled{
		{prune.OrphanClassLocks, dirs.Locks, launchLockPath(c)},
		{prune.OrphanClassLocks, dirs.Locks, livenessLockPath(c)},
		{prune.OrphanClassLocks, dirs.Locks, sessionLockPath(c)},
		{prune.OrphanClassLocks, dirs.Locks, hostDaemonSpawnGuardPath(c)},
		{prune.OrphanClassOwners, dirs.Owners, ownerPIDFile(c)},
		{prune.OrphanClassLogs, dirs.Logs, keeperLogPath(c)},
		{prune.OrphanClassLogs, dirs.Logs, socatLogPath(c)},
		{prune.OrphanClassLogs, dirs.Logs, socatLogPath(c) + ".1"},
		{prune.OrphanClassLogs, dirs.Logs, socatLogPath(c) + ".lock"},
	}
	for _, notch := range append([]string{""}, macosUserKeeperNotches...) {
		key := keeperKey(c, notch)
		cases = append(cases,
			spelled{prune.OrphanClassLocks, dirs.Locks, livenessLockPath(key)},
			spelled{prune.OrphanClassLocks, dirs.Locks, arrivalLockPath(key)},
			spelled{prune.OrphanClassLocks, dirs.Locks, keyedSessionsDir(key)},
		)
	}
	for _, sp := range cases {
		if filepath.Dir(sp.path) != sp.dir {
			t.Errorf("%s is written outside the sweep's %s directory %s", sp.path, sp.class, sp.dir)
			continue
		}
		if got, ok := prune.OrphanCnameOf(sp.class, filepath.Base(sp.path)); !ok || got != c {
			t.Errorf("%s does not parse as %s's %s file (%q, %v): the orphan sweep would never remove it",
				filepath.Base(sp.path), c, sp.class, got, ok)
		}
	}
}

// orphanAt writes one aged per-name file set for cname under the HOME's global storage and
// returns its paths.
func orphanAt(t *testing.T, cname string, at time.Time) []string {
	t.Helper()
	files := []string{launchLockPath(cname), livenessLockPath(cname), sessionLockPath(cname), ownerPIDFile(cname),
		keeperLogPath(cname)}
	for _, p := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// The slot's wiring, by behavior: an orphan's per-name state is removed through the slot's
// guard, the launching jail's own name is kept though nothing lists or tracks it yet, and a
// runtime that cannot be asked declines unstamped.
func TestReapOrphanCnameStateDeletesThroughTheGuardAndSparesTheLaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Getenv = func(string) string { return "" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 2 && argv[1] == "ps" && argv[2] == "-a" {
			return ExecResult{Ran: true, RC: 0} // podman answers: no containers at all
		}
		return ExecResult{Ran: false}
	}
	old := time.Now().Add(-prune.OrphanCnameAgeFloor - 24*time.Hour)
	orphan := orphanAt(t, "yolo-orphan-11111111", old)
	launching := orphanAt(t, "yolo-launching-22222222", old)
	g := &recordingGuard{}
	o.reapOrphanCnameState("podman", "yolo-launching-22222222", g.guard())

	if g.calls != len(orphan) {
		t.Errorf("%d deletions went through the guard, want %d", g.calls, len(orphan))
	}
	for _, p := range orphan {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("orphan file %s kept (%v)", filepath.Base(p), err)
		}
	}
	for _, p := range launching {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("the launching jail's %s was removed (%v)", filepath.Base(p), err)
		}
	}
	if due, _ := o.classDebounce("orphan-cname-state"); due {
		t.Error("a completed pass did not stamp its debounce")
	}
}

func TestReapOrphanCnameStateDeclinesUnstampedWhenTheRuntimeCannotBeAsked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Getenv = func(string) string { return "" }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	orphan := orphanAt(t, "yolo-orphan-11111111", time.Now().Add(-prune.OrphanCnameAgeFloor-24*time.Hour))
	o.reapOrphanCnameState("podman", "", nil)
	for _, p := range orphan {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s removed under unknown liveness (%v)", filepath.Base(p), err)
		}
	}
	if due, _ := o.classDebounce("orphan-cname-state"); !due {
		t.Error("a declined pass stamped its debounce")
	}
}

// orphanSlotOptions is Options for one slot pass: podman answers "no containers", env is env.
func orphanSlotOptions(t *testing.T, env map[string]string) *Options {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Getenv = func(k string) string { return env[k] }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 2 && argv[1] == "ps" && argv[2] == "-a" {
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: false}
	}
	return o
}

func assertOrphanFiles(t *testing.T, files []string, wantGone bool) {
	t.Helper()
	for _, p := range files {
		_, err := os.Lstat(p)
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s: gone=%v, want %v (%v)", filepath.Base(p), gone, wantGone, err)
		}
	}
}

func agedOrphan(t *testing.T) []string {
	return orphanAt(t, "yolo-orphan-11111111", time.Now().Add(-prune.OrphanCnameAgeFloor-24*time.Hour))
}

// The opt-out gate: under YOLO_NO_AUTO_IMAGE_REAP nothing is removed and no stamp is written.
func TestReapOrphanCnameStateHonorsTheOptOut(t *testing.T) {
	o := orphanSlotOptions(t, map[string]string{autoReapOptOutEnv: "1"})
	files := agedOrphan(t)
	o.reapOrphanCnameState("podman", "", nil)
	assertOrphanFiles(t, files, false)
	if _, err := os.Stat(HousekeepingStampPath(orphanCnameClass)); !os.IsNotExist(err) {
		t.Errorf("the opted-out pass wrote its stamp (%v)", err)
	}
}

// IN A JAIL IT RUNS: the jail's store and the nested runtime are both the jail's own.
func TestReapOrphanCnameStateRunsInAJail(t *testing.T) {
	o := orphanSlotOptions(t, map[string]string{"YOLO_VERSION": "9.9.9"})
	files := agedOrphan(t)
	o.reapOrphanCnameState("podman", "", nil)
	assertOrphanFiles(t, files, true)
}

// A session listing that cannot be read declines the pass, unstamped.
func TestReapOrphanCnameStateDeclinesWhenSessionsCannotBeListed(t *testing.T) {
	o := orphanSlotOptions(t, nil)
	saved := orphanSessionKeys
	orphanSessionKeys = func(string) (map[string]bool, bool) { return nil, false }
	t.Cleanup(func() { orphanSessionKeys = saved })
	files := agedOrphan(t)
	o.reapOrphanCnameState("podman", "", nil)
	assertOrphanFiles(t, files, false)
	if due, _ := o.classDebounce(orphanCnameClass); !due {
		t.Error("a declined pass stamped its debounce")
	}
}

// The macos-user slot: with no container runtime on PATH the known set is the tracking files and
// sessions, and it sweeps; a runtime on PATH that cannot be asked declines; one that lists the
// name (stopped) keeps it.
func TestReapOrphanCnameStateMacosUser(t *testing.T) {
	t.Run("no runtime on PATH sweeps", func(t *testing.T) {
		o := orphanSlotOptions(t, nil)
		o.LookPath = func(string) (string, bool) { return "", false }
		o.Exec = func([]string, string, []string, time.Duration) ExecResult {
			t.Error("asked a runtime with none on PATH")
			return ExecResult{Ran: false}
		}
		files := agedOrphan(t)
		o.reapOrphanCnameStateMacosUser(nil)
		assertOrphanFiles(t, files, true)
	})
	t.Run("a runtime on PATH that cannot be asked declines", func(t *testing.T) {
		o := orphanSlotOptions(t, nil)
		o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
		o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
		files := agedOrphan(t)
		o.reapOrphanCnameStateMacosUser(nil)
		assertOrphanFiles(t, files, false)
	})
	t.Run("a runtime on PATH that lists the name keeps it", func(t *testing.T) {
		o := orphanSlotOptions(t, nil)
		o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
		o.Exec = func([]string, string, []string, time.Duration) ExecResult {
			return ExecResult{Ran: true, RC: 0, Stdout: "yolo-orphan-11111111\n"}
		}
		files := agedOrphan(t)
		o.reapOrphanCnameStateMacosUser(nil)
		assertOrphanFiles(t, files, false)
	})
}

// The macos-user slot's call site.
func TestMacosUserSlotRunsTheOrphanSweep(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := afterFunc(string(src), "func (o *Options) runMacosUserHousekeeping(")
	if !strings.Contains(fn, "o.reapOrphanCnameStateMacosUser(guard)") {
		t.Error("the macos-user slot no longer runs the orphaned per-jail state sweep")
	}
}
