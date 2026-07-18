package prune

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReferencedSet is the tri-state result of enumerating live-container build-root
// mounts. Known distinguishes "enumerated (maybe empty)" from "could not
// enumerate" — the polarity the sweep depends on (None ≠ empty set).
type ReferencedSet struct {
	// Known is false when the runtime couldn't be enumerated (flaky/absent
	// `ps`); the sweep then DECLINES to delete. True even for an empty Paths.
	Known bool
	// Paths are the resolved host build-root paths a live jail binds.
	Paths map[string]struct{}
}

// PruneOrphanBuildRoots reclaims nix-build-root.old.* generations that are
// BOTH (a) not referenced by a live jail AND (b) older than olderThan. Mirrors
// _prune_orphan_build_roots including the FAIL-SAFE: referenced.Known==false
// (liveness unknown) → delete NOTHING. Returns (bytesRemoved, dirsRemoved);
// apply=false reports without touching disk.
func PruneOrphanBuildRoots(globalStorage string, referenced ReferencedSet, olderThan time.Duration, apply bool, now time.Time) (bytesRemoved int64, dirsRemoved int) {
	info, err := os.Stat(globalStorage)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	if !referenced.Known {
		// Liveness unknown — decline to delete (fail safe). This is the
		// tri-state guard: a nil/unknown set must NEVER read as "nothing live".
		return 0, 0
	}
	children, err := os.ReadDir(globalStorage)
	if err != nil {
		return 0, 0
	}
	for _, c := range children {
		name := c.Name()
		// Only aside-generations; never the live nix-build-root or the
		// in-flight nix-build-tmp-* dirs.
		if !strings.HasPrefix(name, "nix-build-root.old.") {
			continue
		}
		child := filepath.Join(globalStorage, name)
		st, err := os.Lstat(child)
		if err != nil {
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			continue
		}
		// (a) liveness gate — skip anything a running jail still binds. Compare
		// both the resolved and raw path (Python checks resolved in referenced
		// OR child in referenced).
		resolved := child
		if r, err := filepath.EvalSymlinks(child); err == nil {
			resolved = r
		}
		if _, ok := referenced.Paths[resolved]; ok {
			continue
		}
		if _, ok := referenced.Paths[child]; ok {
			continue
		}
		// (b) age grace floor — skip recent generations (startup window).
		if now.Sub(st.ModTime()) < olderThan {
			continue
		}
		size := dirSizeBytes(child)
		if apply {
			if err := os.RemoveAll(child); err != nil {
				continue
			}
		}
		bytesRemoved += size
		dirsRemoved++
	}
	return bytesRemoved, dirsRemoved
}

// dirSizeBytes sums regular-file sizes under p (missing → 0). Mirrors
// _dir_size_bytes (lstat, follow no symlinks).
func dirSizeBytes(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if st, err := os.Lstat(path); err == nil {
			total += st.Size()
		}
		return nil
	})
	return total
}
