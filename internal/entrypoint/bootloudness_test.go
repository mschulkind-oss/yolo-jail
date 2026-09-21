package entrypoint

// bootloudness_test.go pins the boot path's DISCLOSURES: every place the entrypoint takes a
// decision the user pays for — a timeout, a degradation, a discarded failure — and now says
// so. One test per production report, and each is written to FAIL IF THE REPORTING LINE IS
// DELETED rather than to assert the sentence a comment makes.
//
// Every assertion reads one of the two sinks this package already has (Env.Stderr, which
// attachBootLog tees into <workspace>/.yolo/boot.log, and Env.LogOnly, which is that file
// alone). Nothing here is behind a flag, an env dial or a level, because nothing in the
// production code is: OQ-RO3 forbids a quiet mode, so a disclosure that could be switched
// off would not be one.
//
// WHY THE FAILURES ARE PROVOKED THE WAY THEY ARE: these tests run as ROOT in the jail, where
// root can unlink, chmod and write essentially anything on a normal filesystem. So the
// portable ways to make a syscall actually fail are structural rather than permission-based —
// a non-empty directory passed to os.Remove (ENOTEMPTY), a self-referential symlink passed to
// os.WriteFile (ELOOP), a regular file where a directory is expected (ENOTDIR), and procfs,
// which refuses unlink even for uid 0 (MEASURED: `remove /proc/1/cmdline: operation not
// permitted` as uid 0). A test that needs a permission denial instead would silently SKIP in
// here, which is the one place this package's behaviour matters.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// testEnv is an Env whose two sinks are discarded, for the tests that care about a
// function's effects rather than what it said.
func testEnv(t *testing.T) *Env {
	t.Helper()
	e, _, _ := loudEnv(t)
	return e
}

// loudEnv returns an Env wired to in-memory sinks, plus the two buffers: the first is
// Stderr (the terminal, and therefore boot.log), the second is LogOnly (boot.log alone).
// Which buffer a line lands in is itself load-bearing — a degradation the user pays for
// belongs on the terminal, and the positive "this ran and found nothing" record does not.
func loudEnv(t *testing.T) (e *Env, stderr, logOnly *bytes.Buffer) {
	t.Helper()
	stderr = &bytes.Buffer{}
	logOnly = &bytes.Buffer{}
	e = NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	e.Stderr = stderr
	e.LogOnly = logOnly
	return e, stderr, logOnly
}

// mustContain fails with both the wanted fragment and the whole sink, because the usual
// failure here is a report that exists but no longer names the thing a reader needs.
func mustContain(t *testing.T, what string, got *bytes.Buffer, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got.String(), want) {
			t.Errorf("%s does not mention %q.\nGot:\n%s", what, want, got.String())
		}
	}
}

// fakeBin writes an executable script named name into its own temp dir and puts that dir
// on PATH for the test. It is how the bounded-subprocess reports are exercised without
// running any real tool — and in particular WITHOUT running an agent CLI: the `claude`
// case below is a two-line shell script that exits non-zero, never the agent.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

// ---------------------------------------------------------------------------
// runBoundedStep: the one reporting path every bounded boot subprocess takes.
// ---------------------------------------------------------------------------

// TestBoundedStepNamesWhatItWaitedForAndHowLong is the headline case. A timeout that says
// nothing is the defect this whole file exists for: the boot silently loses the step's
// effect AND spends the full bound doing it, so the user pays a wait they cannot attribute.
func TestBoundedStepNamesWhatItWaitedForAndHowLong(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	runBoundedStep(e, "the-step-under-test", 30*time.Millisecond, exec.Command("sleep", "30"))
	mustContain(t, "a timed-out bounded step", stderr,
		"the-step-under-test", // WHAT it was waiting for
		"timed out",
		"30ms", // HOW LONG it waited
	)
	if strings.Contains(stderr.String(), "ok in") {
		t.Error("a timed-out step reported success")
	}
}

// TestBoundedStepReportsAFailureAndThatTheBootContinues: a non-zero exit is a degradation,
// so it says both that it failed and what the boot does about it. Without the second half
// the line reads as fatal, which sends a reader looking for a refusal that never happened.
func TestBoundedStepReportsAFailureAndThatTheBootContinues(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	runBoundedStep(e, "the-failing-step", time.Minute, exec.Command("false"))
	mustContain(t, "a failed bounded step", stderr, "the-failing-step", "failed", "continues")
}

// TestBoundedStepNoticesASuccessThatTookRealTime is the ~10-second-silent-gap lesson applied
// to the boot path: a step that SUCCEEDS but takes seconds still cost the user those seconds,
// and an unattributed wait is indistinguishable from a hang.
func TestBoundedStepNoticesASuccessThatTookRealTime(t *testing.T) {
	prev := boundedStepSlowNotice
	boundedStepSlowNotice = time.Nanosecond
	t.Cleanup(func() { boundedStepSlowNotice = prev })

	e, stderr, _ := loudEnv(t)
	runBoundedStep(e, "the-slow-step", time.Minute, exec.Command("true"))
	mustContain(t, "a slow but successful bounded step", stderr, "the-slow-step", "took")
}

// TestBoundedStepKeepsAQuickSuccessOffTheTerminal is the other half of the same ruling: the
// positive record belongs in boot.log, so the log answers "did it happen?", and NOT on the
// terminal, where a line per healthy step is the noise OQ-RO3's readable launch protects.
func TestBoundedStepKeepsAQuickSuccessOffTheTerminal(t *testing.T) {
	e, stderr, logOnly := loudEnv(t)
	runBoundedStep(e, "the-quick-step", time.Minute, exec.Command("true"))
	if stderr.Len() != 0 {
		t.Errorf("a quick, successful bounded step wrote to the terminal: %q", stderr.String())
	}
	mustContain(t, "the boot log", logOnly, "the-quick-step", "ok in")
}

// ---------------------------------------------------------------------------
// The three call sites that route through it.
// ---------------------------------------------------------------------------

// TestLdCacheGenerationReportsItsOwnTimeout pins the FIRST of the two silent timeout
// branches this change removed. generateLdCache used to carry a private 30-second select
// that killed ldconfig and said nothing, so a jail whose ld.so.cache was never populated
// looked identical to one where it was — and the difference is every dlopen in the jail.
func TestLdCacheGenerationReportsItsOwnTimeout(t *testing.T) {
	// A shell SPIN rather than `sleep`, because fakeBin replaces PATH with its own temp
	// dir and a fake that shells out to an unfound `sleep` exits 127 instantly — which
	// lands in the FAILURE branch and silently tests the wrong report.
	fakeBin(t, "ldconfig", "while :; do :; done")
	prev := ldCacheTimeout
	ldCacheTimeout = 30 * time.Millisecond
	t.Cleanup(func() { ldCacheTimeout = prev })

	e, stderr, _ := loudEnv(t)
	generateLdCache(e)
	mustContain(t, "a timed-out ldconfig", stderr, "ldconfig", "timed out")
}

// TestLdCacheGenerationRecordsAnAbsentLdconfig: skipping because the tool is not there is
// not a failure, but it IS the answer to "why does this jail have no ld.so.cache", so it
// goes to the log rather than nowhere.
func TestLdCacheGenerationRecordsAnAbsentLdconfig(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	e, stderr, logOnly := loudEnv(t)
	generateLdCache(e)
	if stderr.Len() != 0 {
		t.Errorf("an absent ldconfig is not a degradation and must stay off the terminal: %q", stderr.String())
	}
	mustContain(t, "the boot log", logOnly, "ldconfig")
}

// TestRetiredMiseToolRemovalReportsAFailure pins the SECOND silent timeout branch.
//
// It calls miseUninstallTools rather than miseUninstallRetired only because a FAILING tool
// has to be supplied; the loop itself is live on every launch, which the companion test
// below pins so this one cannot quietly become a test of nothing.
func TestRetiredMiseToolRemovalReportsAFailure(t *testing.T) {
	fakeBin(t, "mise", "exit 7")
	e, stderr, _ := loudEnv(t)
	miseUninstallTools(e, []string{"retired-tool"})
	mustContain(t, "a failed mise uninstall", stderr, "retired-tool", "failed")
}

// TestRetiredMiseToolRemovalIsALivePerLaunchCost is the call-site half: the reporting test
// above hands the loop its own list, so on its own it would still pass if the production
// list emptied out and the two per-launch steps stopped existing. MEASURED in a nested jail
// on 2026-09-19: two tools, ~20ms each, both previously silent.
func TestRetiredMiseToolRemovalIsALivePerLaunchCost(t *testing.T) {
	if len(packload.RetireMiseTools(nil)) == 0 {
		t.Skip("the retired-tool list is empty, so the reporting above is no longer a " +
			"per-launch cost; delete both tests together rather than keeping a pin on " +
			"a loop that never runs")
	}
	// The list is non-empty, so boot.go's miseUninstallRetired really does run this many
	// bounded subprocesses on every launch and every attach. The report is what makes that
	// cost attributable.
	e, _, logOnly := loudEnv(t)
	fakeBin(t, "mise", "exit 0")
	miseUninstallRetired(e)
	if !strings.Contains(logOnly.String(), "retired tool") {
		t.Errorf("the per-launch retired-tool sweep left no record in the boot log:\n%s",
			logOnly.String())
	}
}

// ---------------------------------------------------------------------------
// Degradations that are not subprocesses.
// ---------------------------------------------------------------------------

// TestScratchPermissionsReportTheirFailureAndItsSymptom: /tmp not being 1777 does not break
// the boot, it breaks Chromium's sandboxed children hours later, which is unattributable
// from the symptom alone. Named through the scratchChmod seam because root cannot be made
// to fail a chmod of a directory it owns.
func TestScratchPermissionsReportTheirFailureAndItsSymptom(t *testing.T) {
	prev := scratchChmod
	scratchChmod = func(string, os.FileMode) error { return os.ErrPermission }
	t.Cleanup(func() { scratchChmod = prev })

	dir := t.TempDir()
	e, stderr, _ := loudEnv(t)
	configureScratchPermissionsDirs(e, dir)
	mustContain(t, "a failed scratch chmod", stderr, dir, "1777", "scratch")
}

// TestATimezoneAppliedOnlyHalfwayIsReported. The early returns above this write are the
// genuinely empty cases and stay silent; by the time the write runs, /etc/localtime already
// names the zone, so a lost /etc/timezone leaves the two readers of the timezone permanently
// disagreeing — with nothing anywhere saying why.
func TestATimezoneAppliedOnlyHalfwayIsReported(t *testing.T) {
	zoneDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(zoneDir, "Fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zoneDir, "Fake", "Zone"), []byte("TZif"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	// A NON-EMPTY DIRECTORY where the file goes: os.WriteFile then fails EISDIR even for
	// root, and the symlink above it still succeeds, which is exactly the half-applied
	// state the report is about.
	if err := os.MkdirAll(filepath.Join(runDir, "timezone", "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := tzRunDir
	tzRunDir = runDir
	t.Cleanup(func() { tzRunDir = prev })

	e, stderr, _ := loudEnv(t)
	e.Vars["TZ"] = "Fake/Zone"
	e.Vars["TZDIR"] = zoneDir
	configureTimezone(e)

	if _, err := os.Readlink(filepath.Join(runDir, "localtime")); err != nil {
		t.Fatalf("localtime was not linked, so this test is not reaching the half-applied "+
			"state it exists for: %v", err)
	}
	mustContain(t, "a half-applied timezone", stderr, "Fake/Zone", "timezone")
}

// TestADroppedStorePackageFarmThatCannotBeClearedIsReported is R2's inversion. The farm's
// bin dir sits IMMEDIATELY before /bin on BootPath, so a symlink surviving from a previous,
// opted-in entry shadows the baked binary of the same name — the jail then runs a closure
// this launch never asked for, and nothing said so.
func TestADroppedStorePackageFarmThatCannotBeClearedIsReported(t *testing.T) {
	root := t.TempDir()
	// A regular FILE where the bin dir belongs: ReadDir fails ENOTDIR, which is a clear
	// failure root cannot avoid.
	if err := os.WriteFile(storeBinDir(root), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e, stderr, _ := loudEnv(t)
	if err := buildStorePackageFarm(e, root, nil); err != nil {
		t.Fatalf("a farm that cannot be cleared must DEGRADE, not refuse the boot: %v", err)
	}
	mustContain(t, "an uncleared farm", stderr, storeBinDir(root), "PATH", "shadow")
}

// TestAStaleGeneratedClientThatSurvivesIsReported: this removal is the CUTOVER, not
// tidiness. ~/.local/bin precedes /bin, so a surviving script shadows the baked binary on
// every future launch — and for the retired loophole clients the symptom is the inverse of
// the truth: "not available" in a jail where the loophole is running fine.
func TestAStaleGeneratedClientThatSurvivesIsReported(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	// The shim-dir half of the sweep calls os.Remove with no IsRegular filter, so a
	// NON-EMPTY DIRECTORY named after a retired file fails ENOTEMPTY for root too.
	occupied := filepath.Join(e.BlockDir(), staleShimFiles[0])
	if err := os.MkdirAll(filepath.Join(occupied, "occupant"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStaleGeneratedClients(e); err != nil {
		t.Fatalf("the cutover sweep must not refuse a boot over a file it cannot unlink: %v", err)
	}
	mustContain(t, "a surviving retired bootstrap file", stderr, occupied, "PATH")
}

// TestARetiredGeneratedDirThatCannotBeEmptiedIsReported. One of those leftovers is a `grep`
// blocker, which starts intercepting again the moment its directory is back on a PATH.
func TestARetiredGeneratedDirThatCannotBeEmptiedIsReported(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	// procfs is the portable way to get an unlink failure as uid 0. /proc/self/fd holds a
	// handful of non-directory entries, every one of which refuses to be removed.
	if err := os.Symlink("/proc/self/fd", filepath.Join(e.Home, retiredGeneratedDirs[0])); err != nil {
		t.Fatal(err)
	}
	removeRetiredGeneratedDirs(e)
	mustContain(t, "an un-emptiable retired script dir", stderr,
		retiredGeneratedDirs[0], "intercept")
}

// TestAGitIdentityThatCouldNotBeRecordedIsReported. The symptom otherwise is `git commit`
// saying "Please tell me who you are", which reads as yolo never having been TOLD the
// identity rather than as yolo having been unable to record it — and the usual cause is a
// ~/.gitconfig mounted read-only, which no part of that message hints at.
func TestAGitIdentityThatCouldNotBeRecordedIsReported(t *testing.T) {
	fakeBin(t, "git", "exit 1")
	e, stderr, _ := loudEnv(t)
	e.Vars["YOLO_GIT_NAME"] = "Some One"
	e.Vars["YOLO_GIT_EMAIL"] = "some@one.example"
	configureGit(e)
	mustContain(t, "a failed git identity write", stderr, "user.name", "user.email", "read-only")
}

// TestAnAbsentGitIsRecordedButNotWarned: no git means there is no identity to set, which is
// an empty case rather than a degradation — but it is still the answer to "why does this
// jail have no git identity", so it belongs in the log.
func TestAnAbsentGitIsRecordedButNotWarned(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	e, stderr, logOnly := loudEnv(t)
	e.Vars["YOLO_GIT_NAME"] = "Some One"
	configureGit(e)
	if stderr.Len() != 0 {
		t.Errorf("an absent git is not a degradation: %q", stderr.String())
	}
	mustContain(t, "the boot log", logOnly, "git")
}

// ---------------------------------------------------------------------------
// The real defect: a failed credential copy that reported success.
// ---------------------------------------------------------------------------

// TestAFailedSharedCredentialCopyLeavesTheLocalFileAlone is a regression test for the worst
// shape in this class — a silent failure reported as the success it prevented.
//
// linkThroughShared set copied = true BEFORE knowing whether the write had happened, then
// removed the local credential and symlinked it at the still-empty shared path, logging
// "shared empty; copied local credential into shared". A fresh login was destroyed and the
// log said it had been preserved.
//
// The two provoked failures are the two writes that stand between reading the local file and
// deleting it.
func TestAFailedSharedCredentialCopyLeavesTheLocalFileAlone(t *testing.T) {
	const creds = `{"token":"the-only-copy"}`

	t.Run("the shared dir cannot be created", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "credentials.json")
		if err := os.WriteFile(link, []byte(creds), 0o600); err != nil {
			t.Fatal(err)
		}
		// A regular file where a parent directory belongs: MkdirAll fails ENOTDIR.
		if err := os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		shared := filepath.Join(dir, "blocker", "shared", "credentials.json")

		e, _, _ := loudEnv(t)
		decision, err := e.linkThroughShared(link, shared, shared)
		if err != nil {
			t.Fatalf("a failed copy must degrade, not fail the hook: %v", err)
		}
		assertCredentialSurvived(t, link, creds, decision, "shared dir")
	})

	t.Run("the shared file cannot be written", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "credentials.json")
		if err := os.WriteFile(link, []byte(creds), 0o600); err != nil {
			t.Fatal(err)
		}
		// A self-referential symlink: Stat fails (so the copy-into-empty branch is taken)
		// and WriteFile then fails ELOOP, for root as much as for anyone.
		shared := filepath.Join(dir, "loop")
		if err := os.Symlink("loop", shared); err != nil {
			t.Fatal(err)
		}

		e, _, _ := loudEnv(t)
		decision, err := e.linkThroughShared(link, shared, shared)
		if err != nil {
			t.Fatalf("a failed copy must degrade, not fail the hook: %v", err)
		}
		assertCredentialSurvived(t, link, creds, decision, "shared file")
	})
}

// assertCredentialSurvived checks the two halves that matter together: the bytes are still
// there AND the decision string says the copy did not happen. Either alone is passable while
// the defect is live — the destructive version returned a cheerful decision, and a decision
// check alone would not notice a future edit that deleted the file anyway.
func assertCredentialSurvived(t *testing.T, link, want, decision, which string) {
	t.Helper()
	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("the local credential was DESTROYED after the %s write failed: %v", which, err)
	}
	if string(got) != want {
		t.Errorf("the local credential was rewritten: got %q, want %q", got, want)
	}
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the local credential was replaced by a symlink to a shared file that was " +
			"never written — the credential is gone and the tool will read an empty file")
	}
	if strings.Contains(decision, "copied local credential into shared") {
		t.Errorf("the decision reports a copy that did not happen: %q", decision)
	}
	if !strings.Contains(decision, "left in place") {
		t.Errorf("the decision must say the local credential was left in place, got: %q", decision)
	}
}
