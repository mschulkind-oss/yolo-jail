package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// packrecords.go bounds the PROCESS-WIDE records a launch derives from its own staged pack
// set, so one launch cannot inherit another's.
//
// # The leak this closes
//
// stagePacks records three things for every host-side consumer to read — the pack-shipped
// loophole modules, the pack `supersedes` claims, and the pack skills sources — and all
// three are process-wide on purpose: they are the convergence point that stopped seven
// discovery surfaces from each assembling their own view of what this machine has
// (docs/design/loophole-packaging.md §5.1, and jailcontent's own SetPackSkillDirs).
// Process-wide was the right scope while a process ran one launch.
//
// It does not. AUTO-CAPTURE RUNS THE ORDINARY RUN PIPELINE IN THIS PROCESS, once per
// uncaptured installer program, against a throwaway workspace of its own — deliberately,
// because "a capture jail must be the same jail a launch produces, or the bytes it records
// are not the bytes a launch would have installed" (internal/cli/capturehost.go's
// runCaptureJail). Each sub-launch stages its own packs, so each one overwrites all three
// records with the CAPTURE jail's staging root; then cleanupCaptureWorkspace deletes that
// root. The parent launch resumes one line later and reads the leftovers.
//
// Measured on a real host 2026-09-09, `yolo -- claude` after three capture sub-launches:
// twenty `loophole module dir …/agents/yolo-agy-<hash>/packs/_official/…/loopholes/… is
// not a directory, so that loophole is NOT active` warnings — four missing dirs, once per
// each of the five discovery passes a container launch makes (the briefing, the broker
// gate, the argv's broker gate, the runtime args, the daemon spawn), every one of them
// naming a jail the user never launched. The skills record leaked the same way with no
// warning at all: refreshJailBriefings runs BELOW auto-capture on the container path, so
// the parent jail staged its pack skills out of the deleted directory and simply got none.
//
// # Why a snapshot, and why here
//
// The records are read through package-level accessors by surfaces that know nothing about
// launches — that is what makes them a convergence point rather than a parameter — so the
// only thing that can know when a launch's record stops being current is the launch. Run
// installs this as its first act and releases it on every return path, which makes the
// scope a property of the pipeline instead of something each nested caller must remember;
// it holds for any future in-process re-entry, not just for auto-capture.
//
// ⚠ RESTORING IS NOT `Set…(nil)`. "Nothing recorded yet" is a distinct state from an empty
// record — the accessors short-circuit on a SET flag, and falling back to the lazy pack
// resolver is what keeps `yolo loopholes list` and the config validator pack-aware — so
// each snapshot puts back the flag with the value. See loopholes.SnapshotPackModules.
func packRecordScope() func() {
	restoreModules := loopholes.SnapshotPackModules()
	restoreSupersessions := loopholes.SnapshotPackSupersessions()
	skillDirs := jailcontent.PackSkillDirs()
	skillTargets := jailcontent.PackSkillTargets()
	return func() {
		restoreModules()
		restoreSupersessions()
		jailcontent.SetPackSkillDirs(skillDirs)
		jailcontent.SetPackSkillTargets(skillTargets)
	}
}
