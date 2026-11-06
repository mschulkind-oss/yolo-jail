package prune

import (
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// agentLogWorkspaceSubdirs are the per-workspace overlay log dirs safe to
// age-purge (disk-footprint.md "Lifecycle sweeps"). Each lives under <ws>/.yolo/home/<subdir>
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
//   - "pi/agent/sessions" and "codex/sessions": pi's and Codex's session
//     transcripts, the same kind of user data as claude/projects. Only each
//     tool's LOG directory is listed.
var agentLogWorkspaceSubdirs = []string{
	"copilot/logs",  // GitHub Copilot CLI process logs (process-*.log)
	"gemini/tmp",    // Gemini CLI scratch/log dir
	"pi/agent/logs", // pi's diagnostic logs (perf-<timestamp>-<pid>.jsonl), a set per process
	"codex/log",     // Codex CLI's log dir ($CODEX_HOME/log, codex-tui.log)
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
	return PurgeAgentLogsGuarded(workspaces, globalCache, olderThanDays, apply, now, nil)
}

// AgentLogSubdirs names every directory PurgeAgentLogs sweeps, the workspace-overlay ones
// first, for a report header: the list is said where it is defined, never retyped.
func AgentLogSubdirs() []string {
	return append(append([]string(nil), agentLogWorkspaceSubdirs...), agentLogGlobalCacheSubdirs...)
}

// PurgeAgentLogsGuarded is PurgeAgentLogs with each deletion under guard (nil deletes
// directly): the launch's housekeeping slot passes its shared-lock guard. An empty
// globalCache sweeps no shared-cache dir.
func PurgeAgentLogsGuarded(workspaces []string, globalCache string, olderThanDays float64, apply bool, now time.Time, guard Guard) (bytesRemoved int64, filesRemoved int) {
	cutoff := now.Add(-time.Duration(olderThanDays * 86400 * float64(time.Second)))
	for _, ws := range workspaces {
		// The overlay is opened refusing a link at `.yolo` or at `.yolo/home`: both are
		// jail-writable (the workspace bind puts them at /workspace/.yolo), so a link at either
		// would aim this purge at a host directory of the jail's choosing. Such a workspace,
		// like one with no overlay, has nothing of its own to purge.
		// Every home the workspace holds is swept: the legacy one and each setup's
		// (paths.WorkspaceHomeStates; docs/design/named-setups.md §9), each opened refusing a link
		// at every component below `.yolo`.
		for _, dir := range paths.WorkspaceHomeStates(ws) {
			home, err := paths.OpenWorkspaceHomeRoot(ws, dir)
			if err != nil {
				continue
			}
			for _, sub := range agentLogWorkspaceSubdirs {
				b, f := purgeOldFilesUnder(home, sub, cutoff, apply, guard)
				bytesRemoved += b
				filesRemoved += f
			}
			home.Close()
		}
	}
	if globalCache == "" {
		return bytesRemoved, filesRemoved
	}
	// The shared cache is followed at its root, as PurgeCacheByAge's is, and walked beneath it.
	if cache, err := os.OpenRoot(globalCache); err == nil {
		for _, sub := range agentLogGlobalCacheSubdirs {
			b, f := purgeOldFilesUnder(cache, sub, cutoff, apply, guard)
			bytesRemoved += b
			filesRemoved += f
		}
		cache.Close()
	}
	return bytesRemoved, filesRemoved
}
