package prune

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// PruneOrphanAgentStaging reaps per-jail briefing/skill staging dirs
// (AGENTS_DIR/<cname>, created by jailcontent.PrepareSkills) whose container no
// longer exists (storage-lifecycle §4). One dir accumulates per distinct
// container name forever — 1000+ on a busy host — because nothing removed them
// at container teardown.
//
// FAIL-SAFE, mirroring every other liveness-gated sweep:
//   - liveKnown==false (runtime unenumerable) → reap NOTHING. An orphan set must
//     never be inferred from a failed probe.
//   - a staging dir whose name is a known container (live OR still tracked) is
//     kept. `known` is the union of live container names and the names with a
//     tracking file under CONTAINER_DIR, so a stopped-but-tracked jail (its
//     briefing may be reused on restart) is spared until its tracking file is
//     pruned.
//   - AGE floor — a dir modified within olderThan is kept, covering a jail
//     mid-startup whose container/tracking record hasn't landed yet.
//
// Returns (bytesRemoved, dirsRemoved); apply=false reports without touching disk.
// Unlike the symlink sweeps this DOES reclaim real bytes (the staged skill
// trees), so its total folds into the reclaimed-bytes summary.
func PruneOrphanAgentStaging(agentsDir string, known map[string]struct{}, liveKnown bool, olderThan time.Duration, apply bool, now time.Time) (bytesRemoved int64, dirsRemoved int, removedNames []string) {
	return PruneOrphanAgentStagingGuarded(agentsDir, known, liveKnown, olderThan, apply, now, nil, "")
}

// PruneOrphanAgentStagingGuarded is PruneOrphanAgentStaging with each removal bracketed by
// guard (guard.go). Its recheck asks again, right before the removal, what a launch can
// change while a pass runs: the dir is still past the age floor — a launch staging into it
// restamps its mtime (run's touchAgentStagingDir) — and no tracking file for the name has
// appeared under containerDir ("" skips that half). The live set is not asked again: a jail
// started since the listing staged into its dir first, which the age floor already sees.
func PruneOrphanAgentStagingGuarded(agentsDir string, known map[string]struct{}, liveKnown bool, olderThan time.Duration,
	apply bool, now time.Time, guard Guard, containerDir string) (bytesRemoved int64, dirsRemoved int, removedNames []string) {
	removedNames = []string{}
	if !liveKnown {
		return 0, 0, removedNames
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return 0, 0, removedNames
	}
	for _, e := range entries {
		name := e.Name()
		if _, keep := known[name]; keep {
			continue
		}
		dir := filepath.Join(agentsDir, name)
		st, err := os.Lstat(dir)
		if err != nil {
			continue
		}
		// Only real directories — never follow or remove a stray symlink here.
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			continue
		}
		if now.Sub(st.ModTime()) < olderThan {
			continue
		}
		size := dirSizeBytes(dir)
		if apply {
			removed := false
			guard.Do(func() bool {
				again, err := os.Lstat(dir)
				if err != nil || again.Mode()&os.ModeSymlink != 0 || !again.IsDir() ||
					now.Sub(again.ModTime()) < olderThan {
					return false
				}
				if containerDir != "" {
					if _, err := os.Lstat(filepath.Join(containerDir, name)); err == nil {
						return false // tracked since the listing
					}
				}
				return true
			}, func() { removed = os.RemoveAll(dir) == nil })
			if !removed {
				continue
			}
		}
		bytesRemoved += size
		dirsRemoved++
		removedNames = append(removedNames, name)
	}
	sort.Strings(removedNames)
	return bytesRemoved, dirsRemoved, removedNames
}

// TrackedContainerNames returns the set of container names with a tracking file
// under CONTAINER_DIR. Combined with the live-container set it forms the
// "known" set PruneOrphanAgentStaging keeps: a name is an orphan only when it is
// neither live nor tracked. A missing/unreadable dir yields an empty set (every
// staging dir then depends on the live set alone).
func TrackedContainerNames(containerDir string) map[string]struct{} {
	names := map[string]struct{}{}
	entries, err := os.ReadDir(containerDir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() {
			names[e.Name()] = struct{}{}
		}
	}
	return names
}

// SessionStagingNames returns the AGENTS_DIR entries a host-services SESSION may be using. A
// session is one macos-user invocation of yolo, or one `yolo host` launch that opens a doorway:
// one command and the host services started for it, from launch to teardown, each in a dir of its
// own whose lock it holds throughout (runtime.ListSessions reads them). The names returned are the
// staging dirs whose name hashes (paths.JailShortHash) to the workspace key of a session under
// sessionsBase that is live, or whose liveness cannot be read. It is the term of the known set
// that keeps a macos-user session's staging: that backend has no container, so nothing a
// container runtime lists or a tracking file records names it, and a session uses its staging
// for as long as its sandbox runs, past the age floor.
//
// EVERY SWEEP, UNDER EVERY RUNTIME. A Mac running macos-user beside Apple Container or podman
// runs the container runtime's sweeps too, in `yolo prune` and in a container launch's
// housekeeping slot, and without this term both would read a live macos-user session's staging
// as an orphan once it was an hour old (INFERRED from the code, not measured on a Mac). A
// `yolo host` session protects its workspace's names the same way, and only while it runs.
//
// ok is false when the sessions could not be listed at all; a caller then declines its sweep, as
// it does when the container runtime cannot be asked.
func SessionStagingNames(agentsDir, sessionsBase string) (names map[string]struct{}, ok bool) {
	sessions, ok := runtime.ListSessions(sessionsBase)
	if !ok {
		return nil, false
	}
	keys := map[string]bool{}
	for _, s := range sessions {
		if s.Liveness != runtime.SessionGone && s.Key != "" {
			keys[s.Key] = true
		}
	}
	names = map[string]struct{}{}
	if len(keys) == 0 {
		return names, true
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return names, true
	}
	for _, e := range entries {
		if keys[paths.JailShortHash(e.Name())] {
			names[e.Name()] = struct{}{}
		}
	}
	return names, true
}
