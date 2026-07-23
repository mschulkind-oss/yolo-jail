package prune

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// PruneOrphanAgentStaging reaps per-jail briefing/skill staging dirs
// (AGENTS_DIR/<cname>, created by agents.PrepareSkills) whose container no
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
			if err := os.RemoveAll(dir); err != nil {
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
