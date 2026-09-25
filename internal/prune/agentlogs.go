package prune

import (
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// agentLogWorkspaceSubdirs are the per-workspace overlay log dirs safe to
// age-purge (storage-lifecycle §4). Each lives under <ws>/.yolo/home/<subdir>
// and holds pure diagnostic logs with no regeneration cost — the tool rewrites
// them on its next run.
//
// DELIBERATELY EXCLUDED — do NOT add these:
//   - "claude/projects": the Claude session transcripts (~250 MiB on a busy
//     workspace). These are durable, non-regenerable user data — the memory
//     system reads them, and a lost transcript cannot be rebuilt. Purging them
//     is data loss, not cache reclaim. The plan (§4) calls this out explicitly.
//   - anything under "claude/" that isn't a log: settings, plugins, tasks,
//     file-history, sessions — all live state.
var agentLogWorkspaceSubdirs = []string{
	"copilot/logs", // GitHub Copilot CLI process logs (process-*.log)
	"gemini/tmp",   // Gemini CLI scratch/log dir
}

// agentLogGlobalCacheSubdirs are the log dirs under the shared GlobalCache
// (~/.cache) — the gemini-cli logger writes here rather than into the overlay.
var agentLogGlobalCacheSubdirs = []string{
	"gemini-cli/logs",
}

// PurgeAgentLogs age-purges regenerable agent LOG files under each tracked
// workspace's overlay home and the shared GlobalCache, older than olderThanDays.
// It reuses the same file-walk discipline as PurgeCacheByAge (regular files
// only, symlinks never followed, dirs kept as anchors, mtime-keyed, dry-run
// accurate) via purgeOldFilesUnder, beneath a root on each tree.
//
// It NEVER touches the Claude session transcripts (claude/projects) — those are
// durable user data, not cache (see agentLogWorkspaceSubdirs). Only the
// allowlisted log subdirs are scanned; there is no recursion outside them.
//
// Returns (bytesRemoved, filesRemoved); apply=false reports without mutating.
func PurgeAgentLogs(workspaces []string, globalCache string, olderThanDays float64, apply bool, now time.Time) (bytesRemoved int64, filesRemoved int) {
	cutoff := now.Add(-time.Duration(olderThanDays * 86400 * float64(time.Second)))
	for _, ws := range workspaces {
		// The overlay is opened refusing a link at `.yolo` or at `.yolo/home`: both are
		// jail-writable (the workspace bind puts them at /workspace/.yolo), so a link at either
		// would aim this purge at a host directory of the jail's choosing. Such a workspace,
		// like one with no overlay, has nothing of its own to purge.
		home, err := paths.OpenWorkspaceStateSubdir(ws, "home")
		if err != nil {
			continue
		}
		for _, sub := range agentLogWorkspaceSubdirs {
			b, f := purgeOldFilesUnder(home, sub, cutoff, apply)
			bytesRemoved += b
			filesRemoved += f
		}
		home.Close()
	}
	// The shared cache is followed at its root, as PurgeCacheByAge's is, and walked beneath it.
	if cache, err := os.OpenRoot(globalCache); err == nil {
		for _, sub := range agentLogGlobalCacheSubdirs {
			b, f := purgeOldFilesUnder(cache, sub, cutoff, apply)
			bytesRemoved += b
			filesRemoved += f
		}
		cache.Close()
	}
	return bytesRemoved, filesRemoved
}
