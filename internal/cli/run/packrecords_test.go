package run

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// packrecords_test.go pins the launch SCOPE of the process-wide pack records, from Run's
// own entry point and through the one seam that makes Run re-entrant in one process.
//
// THE TEST DRIVES Run BECAUSE THE BUG WAS IN Run'S SHAPE, not in any record's own code.
// A unit test on packRecordScope alone stays green with the whole thing switched off —
// delete the two lines that install it in Run and every setter still behaves exactly as
// its own test says. That is the shape AGENTS.md names, so the assertion below is made
// from INSIDE the auto-capture seam, at the moment production resumes the parent launch.

// TestASubLaunchLeavesTheParentLaunchsPackRecordsAlone is the leak itself.
//
// Auto-capture runs the ORDINARY pipeline for its throwaway capture jail, in this process
// (internal/cli/capturehost.go's runCaptureJail, deliberately — "a capture jail must be
// the same jail a launch produces"). Its stagePacks therefore overwrites the process-wide
// records with the CAPTURE jail's staging root while the parent launch is still running,
// and the capture's own cleanup then deletes that root. Measured on a real host
// 2026-09-09: the parent launch resolved every pack loophole against
// `agents/yolo-agy-<hash>/packs/...`, a jail the user never launched, and printed twenty
// "loophole module dir ... is not a directory, so that loophole is NOT active" warnings.
//
// The sub-launch is spelled the way runCaptureJail spells it, and the parent's record is
// read twice: once before, once after. Nothing about the WARNING is asserted — that
// travels through internal/loopholes' package-level warnf straight to os.Stderr and is
// not reachable from here (docs/design/reference-mismatch-diagnostics.md step 1). The
// record is the cause, and the cause is what this measures.
func TestASubLaunchLeavesTheParentLaunchsPackRecordsAlone(t *testing.T) {
	home := packHome(t)
	// claude for the `via: "installer"` program that makes the trigger fire at all, and
	// for the loophole it contributes (claude-oauth-broker); journal because it ships a
	// loophole module and no CLI, so the record has a second entry from a second pack.
	writeUserPacks(t, home, `["claude", "journal"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.CapturesDir = func() string { return t.TempDir() }

	parentRoot := filepath.Join(paths.AgentsDir(), yoloruntime.FromWorkspace(ws), "packs")

	var beforeMods, afterMods []loopholes.PackModule
	var beforeSkills, afterSkills []jailcontent.PackSkillSource
	subRC, subs := 0, 0
	o.AutoCapture = func([]string, string) {
		beforeMods, beforeSkills = loopholes.PackModules(), jailcontent.PackSkillDirs()
		subs++
		// EXACTLY what runCaptureJail configures: the ordinary pipeline, a throwaway
		// workspace of its own, no capture store (the recursion guard), never attach.
		var subOut, subErr bytes.Buffer
		sub := dispatchOptions(t, t.TempDir(), "podman", &subOut, &subErr, nil)
		sub.CapturesDir = func() string { return "" }
		sub.NeverAttach = true
		sub.AcceptConfigChanges = true
		subRC = Run(*sub)
		afterMods, afterSkills = loopholes.PackModules(), jailcontent.PackSkillDirs()
	}

	Run(*o)

	if subs != 1 {
		t.Fatalf("the auto-capture seam ran %d times, want 1 — the launch never reached "+
			"the trigger, so nothing below measures anything\nstdout:\n%s\nstderr:\n%s",
			subs, stdout.String(), stderr.String())
	}
	_ = subRC // the sub-launch fails in runContainer under the stubbed Exec; irrelevant here.

	// The premise: the parent really did record something, and it really was its own.
	if len(beforeMods) == 0 {
		t.Fatalf("the parent launch recorded no pack loophole modules, so a leak could "+
			"not be observed\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, m := range beforeMods {
		if !strings.HasPrefix(m.Dir, parentRoot+string(os.PathSeparator)) {
			t.Fatalf("before the sub-launch the record already named %s, which is not "+
				"under this launch's staging root %s", m.Dir, parentRoot)
		}
	}

	// The leak: the record the parent goes on to resolve against.
	if !sameModules(beforeMods, afterMods) {
		t.Errorf("a sub-launch left its own pack loophole modules behind:\n  before: %s\n"+
			"   after: %s\nThe parent launch resolves loopholes against this record five "+
			"more times (briefing, broker gate, argv, runtime args, spawn), so every one "+
			"of them now names another jail's staging root.",
			moduleDirs(beforeMods), moduleDirs(afterMods))
	}
	// And the user-visible half: the sub-launch's own cleanup deletes its staging root,
	// so an inherited record names directories that are GONE.
	for _, m := range afterMods {
		if _, err := os.Stat(m.Dir); err != nil {
			t.Errorf("after the sub-launch the record names a module dir that does not "+
				"exist: %s (%v) — this is the twenty-line warning burst verbatim", m.Dir, err)
		}
	}

	// The same leak with a silent symptom: PrepareSkills reads this record, and
	// refreshJailBriefings runs AFTER auto-capture on the container path — so an
	// inherited value stages the parent jail's skills out of a deleted directory.
	if !sameSkillSources(beforeSkills, afterSkills) {
		t.Errorf("a sub-launch left its own pack skills sources behind:\n  before: %v\n"+
			"   after: %v\nrefreshJailBriefings runs after auto-capture, so the parent "+
			"jail would stage its skills from another jail's deleted staging dir.",
			beforeSkills, afterSkills)
	}
}

// TestPackRecordScopeRestoresTheUnrecordedState is the one thing the launch-level test
// above cannot show: "nothing recorded yet" is a state of its own, distinct from an empty
// record. PackModules() short-circuits on a SET flag rather than on length, and the
// unrecorded state is what falls through to the LAZY pack resolver — which is what keeps
// `yolo loopholes list`, `yolo check` and the config validator pack-aware without staging.
// A restore spelled `SetPackModules(prev)` would put back an empty RECORDED state instead
// and disable that fallback for the rest of the process.
func TestPackRecordScopeRestoresTheUnrecordedState(t *testing.T) {
	loopholes.ResetPackModules()
	loopholes.ResetPackSupersessions()
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.ResetPackSupersessions()
	})
	// The unrecorded answer: whatever the lazy resolver says on this machine.
	unrecorded := loopholes.PackModules()

	restore := packRecordScope()
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: "/nowhere/x", HostExecApproved: true}})
	jailcontent.SetPackSkillDirs([]jailcontent.PackSkillSource{{Dir: "/nowhere/skills"}})
	restore()

	if got := loopholes.PackModules(); !sameModules(got, unrecorded) {
		t.Errorf("PackModules() = %s after restore, want the unrecorded (lazy-resolver) "+
			"answer %s — the scope must put the SET FLAG back with the value, or the "+
			"resolver fallback is dead for the rest of the process",
			moduleDirs(got), moduleDirs(unrecorded))
	}
	if got := jailcontent.PackSkillDirs(); len(got) != 0 {
		t.Errorf("PackSkillDirs() = %v after restore, want none", got)
	}
}

// launchScopedPackRecords maps each process-wide record a LAUNCH sets from its staged pack
// set to the accessor packRecordScope must reach it through. The value is what the scope's
// own source has to mention, which is how a record that is listed here but not covered is
// caught rather than assumed.
var launchScopedPackRecords = map[string]string{
	"loopholes.SetPackModules":        "loopholes.SnapshotPackModules",
	"loopholes.SetPackSupersessions":  "loopholes.SnapshotPackSupersessions",
	"jailcontent.SetPackSkillDirs":    "jailcontent.PackSkillDirs",
	"jailcontent.SetPackSkillTargets": "jailcontent.PackSkillTargets",
}

// processWidePackRecords are the ones a launch must NOT restore: they are registered once
// per process from an init(), describe this machine's pack store rather than one launch,
// and are what the unrecorded state falls through to.
var processWidePackRecords = map[string]bool{
	"loopholes.SetPackModuleResolver":       true,
	"loopholes.SetPackSupersessionResolver": true,
}

// TestEveryPerLaunchPackRecordIsScoped is the tripwire for the NEXT one.
//
// The leak above was not a mistake in any record's code; it was a fourth record joining
// three others with nobody deciding what its lifetime was. So the decision is forced: every
// `loopholes.Set…`/`jailcontent.Set…` call in this package is either a per-launch record
// packRecordScope restores, or a process-wide registration that must not be — and a new one
// fails this test until its author says which.
//
// A SOURCE SCAN, deliberately, because the property is about call sites and nothing at run
// time can enumerate them. Its cost is the honest one AGENTS.md names for this shape: it
// pins the spelling, so a rename has to be made in two places.
//
// It scans internal/cli AS WELL as this package, because that is where the re-entry lives
// (capturehost.go's runCaptureJail) and a record set from there would leak the same way
// with nothing here to see it.
func TestEveryPerLaunchPackRecordIsScoped(t *testing.T) {
	scope, err := os.ReadFile("packrecords.go")
	if err != nil {
		t.Fatal(err)
	}
	setter := regexp.MustCompile(`\b(loopholes|jailcontent)\.(Set[A-Za-z0-9]*)\(`)
	found := map[string]string{}
	for _, dir := range []string{".", ".."} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range setter.FindAllStringSubmatch(string(body), -1) {
				found[m[1]+"."+m[2]] = filepath.Join(dir, name)
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("found no loopholes./jailcontent. setter calls in internal/cli or " +
			"internal/cli/run — the scan is broken, not the code")
	}
	for call, file := range found {
		accessor, scoped := launchScopedPackRecords[call]
		switch {
		case scoped:
			if !strings.Contains(string(scope), accessor) {
				t.Errorf("%s is listed as per-launch but packrecords.go never reads it "+
					"through %s — a sub-launch's value survives into its caller's launch",
					call, accessor)
			}
		case processWidePackRecords[call]:
			// Registered once per process; restoring it would break the fallback.
		default:
			t.Errorf("%s (%s) is a process-wide pack record with no declared lifetime.\n"+
				"Add it to launchScopedPackRecords and restore it in packRecordScope if a "+
				"LAUNCH sets it (auto-capture runs this pipeline in-process, so its value "+
				"would leak into the parent launch), or to processWidePackRecords if it is "+
				"registered once per process.", call, file)
		}
	}
	for call := range launchScopedPackRecords {
		if _, ok := found[call]; !ok {
			t.Errorf("launchScopedPackRecords names %s, which the launch path no longer "+
				"calls — drop the entry rather than leaving the list describing a call "+
				"site that is gone", call)
		}
	}
}

func moduleDirs(mods []loopholes.PackModule) string {
	var dirs []string
	for _, m := range mods {
		dirs = append(dirs, m.Dir)
	}
	if len(dirs) == 0 {
		return "(none)"
	}
	return strings.Join(dirs, ", ")
}

func sameModules(a, b []loopholes.PackModule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSkillSources(a, b []jailcontent.PackSkillSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Dir != b[i].Dir || strings.Join(a[i].Agents, ",") != strings.Join(b[i].Agents, ",") {
			return false
		}
	}
	return true
}
