package prune

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SweepDanglingOutLinks removes build/run-result-* symlinks whose nix store
// target no longer exists (storage-lifecycle §4). These are the per-build
// out-links image.AutoLoadImage creates and then removes on the success path;
// a crash between build and removal — or the store path being GC'd out from
// under a leftover link — leaves a dangling symlink. It protects nothing (its
// target is already gone), so this needs NO liveness gate: it is pure cleanup,
// safe in-jail.
//
// SCOPE, deliberately narrow:
//   - only entries named "run-result-*" (the per-PID out-link pattern), never
//     the durable "roots/<sha16>" GC roots (§1) or the "restore-result" manual
//     recovery root;
//   - only SYMLINKS (a stray regular file is not ours);
//   - only DANGLING ones — a link whose target still resolves is a live GC root
//     for an in-flight build and must be kept.
//
// Returns the link paths removed (or, in dry-run, that WOULD be), sorted for a
// deterministic report. Removing a symlink frees ~0 bytes directly (the closure
// bytes come back only on a later nix GC), so this reports a COUNT, not bytes —
// it must not inflate the reclaimed-bytes summary.
func SweepDanglingOutLinks(buildDir string, apply bool) []string {
	removed := []string{}
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return removed // no build dir yet → nothing to sweep
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "run-result-") {
			continue
		}
		link := filepath.Join(buildDir, name)
		st, err := os.Lstat(link)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			continue // only symlinks
		}
		// Dangling test: os.Stat follows the link; an error means the target is
		// gone (or otherwise unreachable), which is exactly what we reap. A link
		// that still resolves is a live root for an in-flight build — keep it.
		if _, err := os.Stat(link); err == nil {
			continue
		}
		removed = append(removed, link)
		if apply {
			_ = os.Remove(link)
		}
	}
	sort.Strings(removed)
	return removed
}
