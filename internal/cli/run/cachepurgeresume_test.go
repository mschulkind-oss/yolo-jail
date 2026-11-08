package run

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// agedCacheFile writes rel under the shared cache, 60 days old.
func agedCacheFile(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(paths.GlobalStorage(), "cache", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("xyz"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTheCachePassResumesFromItsSavedPlace is the call-site pin for the cursor: the slot reads
// the saved place of each walk and starts there, so a removal cursor on pip leaves uv (which sorts
// first, and which the cycle already covered) alone and reaches pip; a pass that finishes both
// cycles clears the cursors and stamps the debounce. It fails if the slot stops passing either
// cursor (uv's file goes, or the measurement counts it) or stops saving the progress.
func TestTheCachePassResumesFromItsSavedPlace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	uv := agedCacheFile(t, "uv/old")
	pip := agedCacheFile(t, "pip/old")
	progressPath := filepath.Join(paths.BuildDir(), prune.CachePurgeProgressName)
	prune.SaveCachePurgeProgress(progressPath, prune.CachePurgeProgress{
		Measure:      prune.CachePurgeCursor{Sub: "pip"},
		MeasureBytes: 1000, MeasureFiles: 10, // what earlier passes of this cycle measured
		Apply: prune.CachePurgeCursor{Sub: "pip"},
	})

	var c reclaimConsent
	c.grant(cachePurgeClass, true)
	o.measureAndPurgeCache(c, nil)

	if _, err := os.Stat(uv); err != nil {
		t.Errorf("uv/old was removed: the removal restarted at the first subdir instead of resuming at pip")
	}
	if _, err := os.Stat(pip); !os.IsNotExist(err) {
		t.Errorf("pip/old survived: the resumed removal did not reach pip")
	}
	if m := LastOfferMeasurement(cachePurgeClass); m.Partial || m.Bytes != 1003 {
		t.Errorf("measurement = %+v, want the cycle's 1000 B so far plus pip's 3 B, complete", m)
	}
	if p := prune.LoadCachePurgeProgress(progressPath); !p.Measure.IsStart() || !p.Apply.IsStart() || p.MeasureBytes != 0 {
		t.Errorf("progress after a finished cycle = %+v, want both cursors back at the start", p)
	}
	if due, _ := o.classDebounce(CacheHousekeepingClass); due {
		t.Errorf("a pass that finished both cycles left the class due")
	}
}

// TestTheCacheDebounceWaitsForTheWholeCycle: a pass the budget cuts short saves where it stopped
// and leaves the class due, so the next launch continues the cycle; the pass that finishes it
// stamps the debounce. Without the cursor the second pass would start again at uv.
func TestTheCacheDebounceWaitsForTheWholeCycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	agedCacheFile(t, "uv/old")
	agedCacheFile(t, "pex/old")
	progressPath := filepath.Join(paths.BuildDir(), prune.CachePurgeProgressName)

	orig := cacheWalkBudget
	cacheWalkBudget = -time.Second // every deadline already past: the pass is cut at once
	o.measureAndPurgeCache(reclaimConsent{}, nil)
	cacheWalkBudget = orig

	if p := prune.LoadCachePurgeProgress(progressPath); p.Measure.Sub != prune.CachePurgeDefaultSubdirs[0] {
		t.Errorf("a cut pass saved %+v, want its place in the first subdir", p)
	}
	if due, _ := o.classDebounce(CacheHousekeepingClass); !due {
		t.Fatalf("a cut pass stamped the debounce")
	}
	if m := LastOfferMeasurement(cachePurgeClass); !m.Partial {
		t.Errorf("a cut pass recorded a complete measurement")
	}
	log, _ := os.ReadFile(filepath.Join(o.Workspace, ".yolo", "logs", housekeepingLogName))
	if !strings.Contains(string(log), "left in this cycle: uv (the rest of it)") ||
		!strings.Contains(string(log), "nce") {
		t.Errorf("the log does not say what is left of the cycle:\n%s", log)
	}

	o.measureAndPurgeCache(reclaimConsent{}, nil)
	if due, _ := o.classDebounce(CacheHousekeepingClass); due {
		t.Errorf("the pass that finished the cycle did not stamp the debounce")
	}
	if m := LastOfferMeasurement(cachePurgeClass); m.Partial || m.Bytes != 6 {
		t.Errorf("measurement = %+v, want both files' 6 B, complete", m)
	}
}

// TestTheContainerSlotRunsTheCopilotPkgReaper is the call-site pin for the Copilot self-update
// store: the container slot runs the shared cache's with its runtime, under the guard; the
// macos-user slot runs its own store's, judged by its sessions, and not the container one, whose
// veto needs a container runtime. prune's own tests prove the rule; nothing there notices a slot
// that stopped calling it.
func TestTheContainerSlotRunsTheCopilotPkgReaper(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(afterFunc(string(src), "func (o *Options) runHousekeeping("), "o.reapCopilotPkgVersions(rt, guard)") {
		t.Errorf("the container slot no longer runs reapCopilotPkgVersions with its runtime under the guard")
	}
	mu := afterFunc(string(src), "func (o *Options) runMacosUserHousekeeping(")
	if strings.Contains(mu, "reapCopilotPkgVersions(") {
		t.Errorf("the macos-user slot runs reapCopilotPkgVersions, whose veto needs a container runtime")
	}
	if !strings.Contains(mu, "o.reapMacosUserCopilotPkgVersions(guard)") {
		t.Errorf("the macos-user slot no longer runs reapMacosUserCopilotPkgVersions under the guard")
	}
}

// TestTheCopilotPkgClassIsSkippedOnAppleContainer: yolo reads no Apple Container start time, so
// the class removes nothing, asks nothing, and notes the documented gap once a day rather than a
// decline at every launch.
func TestTheCopilotPkgClassIsSkippedOnAppleContainer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	o.Getenv = func(string) string { return "" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		t.Errorf("the skipped class asked the runtime: %v", argv)
		return ExecResult{}
	}
	pkg := copilotStore(t)
	o.reapCopilotPkgVersions("container", nil)
	o.reapCopilotPkgVersions("container", nil)
	if _, err := os.Stat(filepath.Join(pkg, "1.0.1")); err != nil {
		t.Errorf("1.0.1 was removed on Apple Container")
	}
	if due, _ := o.classDebounce(CopilotPkgHousekeepingClass); due {
		t.Errorf("the skip did not stamp the class, so it would be noted at every launch")
	}
	log, _ := os.ReadFile(filepath.Join(o.Workspace, ".yolo", "logs", housekeepingLogName))
	if n := strings.Count(string(log), "Parity gap: Apple Container"); n != 1 || strings.Contains(string(log), "declined") {
		t.Errorf("want the gap named once and no decline, got %d notes:\n%s", n, log)
	}
}

// TestTheMacosUserCopilotPkgReaperJudgesItsOwnStoreBySessions: the macos-user class removes a long
// superseded version from macos-user's own store (the run-as-you machine tier), never the shared
// cache's Linux store, asks no runtime, keeps the version while a live session started before the
// superseding extraction, and stamps its own class, not the container one.
func TestTheMacosUserCopilotPkgReaperJudgesItsOwnStoreBySessions(t *testing.T) {
	for _, sessionEarly := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		o := goldenOptions(t.TempDir(), home)
		o.Now = time.Now
		o.Getenv = func(string) string { return "" }
		o.MacosUserHostHome = home
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			t.Errorf("the macos-user class asked a runtime: %v", argv)
			return ExecResult{}
		}
		shared := copilotStore(t)
		own := filepath.Join(prune.CopilotPkgCacheMacosUser(paths.GlobalStorageUnder(home)), "copilot", "pkg", "darwin-arm64")
		if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(shared, own); err != nil {
			t.Fatal(err)
		}
		shared = copilotStore(t) // the shared cache's Linux store, which this class must not judge
		base := t.TempDir()
		o.IsMacOS = false
		old := paths.HostSingletonDir
		paths.HostSingletonDir = base
		t.Cleanup(func() { paths.HostSingletonDir = old })
		if sessionEarly {
			plantLiveMacosUserSession(t, base, time.Now().Add(-90*24*time.Hour))
		}
		o.reapMacosUserCopilotPkgVersions(nil)
		_, err := os.Stat(filepath.Join(own, "1.0.1"))
		if gone := os.IsNotExist(err); gone == sessionEarly {
			t.Errorf("session before the superseding version=%v: 1.0.1 removed=%v", sessionEarly, gone)
		}
		if _, err := os.Stat(filepath.Join(shared, "1.0.1")); err != nil {
			t.Errorf("the macos-user class removed from the shared cache's store")
		}
		if due, _ := o.classDebounce(CopilotPkgMacosUserHousekeepingClass); due {
			t.Errorf("session early=%v: the class was not stamped", sessionEarly)
		}
		if due, _ := o.classDebounce(CopilotPkgHousekeepingClass); !due {
			t.Errorf("the macos-user class stamped the container class")
		}
	}
}

// plantLiveMacosUserSession makes a live macos-user session dir under base, its dir, record and
// lock all dated started, its lock held for the rest of the test.
func plantLiveMacosUserSession(t *testing.T, base string, started time.Time) {
	t.Helper()
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix("yolo-ws"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteSessionRecord(dir, runtime.SessionRecord{Notch: runtime.NotchMacosUser}); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, paths.HostServicesSessionLockName)
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{lock, filepath.Join(dir, runtime.SessionRecordName), dir} {
		if err := os.Chtimes(p, started, started); err != nil {
			t.Fatal(err)
		}
	}
}

// copilotStore writes four complete Copilot versions extracted 60 days ago and returns the
// platform directory.
func copilotStore(t *testing.T) string {
	t.Helper()
	pkg := filepath.Join(paths.GlobalStorage(), "cache", "copilot", "pkg", "linux-x64")
	past := time.Now().Add(-60 * 24 * time.Hour)
	for _, v := range []string{"1.0.4", "1.0.3", "1.0.2", "1.0.1"} {
		dir := filepath.Join(pkg, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(dir, ".extraction-complete")
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(marker, past, past)
	}
	return pkg
}

// TestTheCopilotPkgReaperRemovesSupersededVersionsOncePerDay: with podman listing no running jail,
// the slot's class removes a version past the newest three that a newer one long superseded, and
// is debounced; the opt-out skips it.
func TestTheCopilotPkgReaperRemovesSupersededVersionsOncePerDay(t *testing.T) {
	for _, optOut := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		o := goldenOptions(t.TempDir(), home)
		o.Now = time.Now
		o.Getenv = func(k string) string {
			if optOut && k == autoReapOptOutEnv {
				return "1"
			}
			return ""
		}
		o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
		pkg := copilotStore(t)
		o.reapCopilotPkgVersions("podman", nil)
		_, err := os.Stat(filepath.Join(pkg, "1.0.1"))
		if gone := os.IsNotExist(err); gone == optOut {
			t.Errorf("optOut=%v: 1.0.1 removed=%v", optOut, gone)
		}
		if _, err := os.Stat(filepath.Join(pkg, "1.0.2")); err != nil {
			t.Errorf("optOut=%v: one of the newest three was removed", optOut)
		}
		if due, _ := o.classDebounce(CopilotPkgHousekeepingClass); due != optOut {
			t.Errorf("optOut=%v: due=%v after the pass", optOut, due)
		}
	}
}

// TestTheCopilotPkgReaperDeclinesWithoutARuntimeAnswer: a runtime that cannot list its running
// jails' start times removes nothing, says why with a next step, and leaves the class due.
func TestTheCopilotPkgReaperDeclinesWithoutARuntimeAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	o.Getenv = func(string) string { return "" }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 125} }
	pkg := copilotStore(t)
	o.reapCopilotPkgVersions("podman", nil)
	if _, err := os.Stat(filepath.Join(pkg, "1.0.1")); err != nil {
		t.Errorf("1.0.1 was removed although the running jails could not be listed")
	}
	if due, _ := o.classDebounce(CopilotPkgHousekeepingClass); !due {
		t.Errorf("a declined pass stamped the debounce")
	}
	log, _ := os.ReadFile(filepath.Join(o.Workspace, ".yolo", "logs", housekeepingLogName))
	if !strings.Contains(string(log), "copilot: declined") || !strings.Contains(string(log), "yolo prune --apply") {
		t.Errorf("the decline is not noted with a next step:\n%s", log)
	}
}

// TestAFailedRemovalLeavesTheCacheClassDue: a consented removal that could not remove something
// notes it and leaves the class due. Skipped as root (disk-footprint.md's backlog names it).
func TestAFailedRemovalLeavesTheCacheClassDue(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only directory's entries, so the failure cannot be staged")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	// An interrupted removal's tombstone, which the pass finishes whatever its age, holding a
	// directory it cannot empty.
	ro := filepath.Dir(agedCacheFile(t, "uv/archive-v0/.yolo-purge-abc/pkg/a.py"))
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	var c reclaimConsent
	c.grant(cachePurgeClass, false)
	o.measureAndPurgeCache(c, nil)
	log, _ := os.ReadFile(filepath.Join(o.Workspace, ".yolo", "logs", housekeepingLogName))
	if !strings.Contains(string(log), "could not finish removing") {
		t.Fatalf("the failure was not noted:\n%s", log)
	}
	if due, _ := o.classDebounce(CacheHousekeepingClass); !due {
		t.Errorf("a removal that failed stamped the debounce, so the note's \"the next launch retries\" is false")
	}
}

// TestWithConsentTheRemovalCycleStampsTheDebounce is the alternation the old rule allowed: with
// consent, a pass whose measurement is cut but whose removal finishes its cycle stamps the class,
// rather than waiting for both walks to finish on the same pass, which they might never do.
func TestWithConsentTheRemovalCycleStampsTheDebounce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	agedCacheFile(t, "uv/old")
	pip := agedCacheFile(t, "pip/old")
	progressPath := filepath.Join(paths.BuildDir(), prune.CachePurgeProgressName)
	prune.SaveCachePurgeProgress(progressPath, prune.CachePurgeProgress{Apply: prune.CachePurgeCursor{Sub: "pip"}})

	orig := cacheWalkDeadline
	cacheWalkDeadline = func(walk string) time.Time {
		if walk == "measure" {
			return time.Now().Add(-time.Second) // cut at once
		}
		return time.Now().Add(time.Hour)
	}
	defer func() { cacheWalkDeadline = orig }()

	var c reclaimConsent
	c.grant(cachePurgeClass, false)
	o.measureAndPurgeCache(c, nil)

	if _, err := os.Stat(pip); !os.IsNotExist(err) {
		t.Fatalf("the removal did not finish its cycle at pip")
	}
	if m := LastOfferMeasurement(cachePurgeClass); !m.Partial {
		t.Errorf("the cut measurement was recorded complete")
	}
	if due, _ := o.classDebounce(CacheHousekeepingClass); due {
		t.Errorf("with consent, a finished removal cycle left the class due: the two walks could alternate for ever")
	}
}
