package prune

import (
	"os"
	"path/filepath"
	"strings"
)

// ShadowedHomePaths is the frozen registry of :ro GLOBAL_HOME seed subtrees that
// are fully overlay-masked at jail runtime, so their contents can never be read
// by a live jail but accumulate tens of GiB from pre-cache-split installs.
// Frozen contract (must not drift — every entry must be genuinely shadowed by an
// overlay mount declared in the run package's mount assembly).
var ShadowedHomePaths = []string{
	".cache",
	".npm",
	".npm-global",
	".local",
	"go",
}

// PruneShadowedHome reclaims the shadowed copies under globalHome listed in
// ShadowedHomePaths. Directories are EMPTIED but PRESERVED — they anchor live
// jails' overlay mounts, so rmtree'ing the dir itself would orphan those mounts
// in-place (observed incident 2026-07-04). Symlinks are unlinked but never
// traversed. Returns (bytesRemoved, itemsRemoved). A failed child skips counting
// the whole entry.
func PruneShadowedHome(globalHome string, apply bool) (bytesRemoved int64, itemsRemoved int) {
	info, err := os.Stat(globalHome)
	if err != nil || !info.IsDir() {
		return 0, 0
	}

	for _, rel := range ShadowedHomePaths {
		// Refuse suspicious registry entries defensively (a compile-time
		// constant today, but guard against a bad edit that would escape
		// globalHome).
		suspicious := rel == "" || strings.HasPrefix(rel, "/")
		for _, part := range strings.Split(rel, "/") {
			if part == ".." {
				suspicious = true
			}
		}
		if suspicious {
			continue
		}

		target := filepath.Join(globalHome, rel)
		lst, err := os.Lstat(target)
		if err != nil {
			continue
		}

		switch {
		case lst.Mode()&os.ModeSymlink != 0:
			// Symlink itself takes ~0 bytes but still counts as one item.
			if apply {
				if err := os.Remove(target); err != nil {
					continue
				}
			}
			itemsRemoved++

		case lst.IsDir():
			size := dirSizeBytes(target)
			if apply {
				failed := false
				children, err := os.ReadDir(target)
				if err != nil {
					// iterdir failure
					// this entry; treat as failed and skip counting.
					continue
				}
				for _, c := range children {
					child := filepath.Join(target, c.Name())
					ci, cerr := os.Lstat(child)
					if cerr != nil {
						failed = true
						continue
					}
					var rmErr error
					if ci.Mode()&os.ModeSymlink == 0 && ci.IsDir() {
						rmErr = os.RemoveAll(child)
					} else {
						rmErr = os.Remove(child)
					}
					if rmErr != nil {
						failed = true
					}
				}
				if failed {
					continue
				}
			}
			bytesRemoved += size
			itemsRemoved++

		case lst.Mode().IsRegular():
			size := lst.Size()
			if apply {
				if err := os.Remove(target); err != nil {
					continue
				}
			}
			bytesRemoved += size
			itemsRemoved++
		}
	}

	return bytesRemoved, itemsRemoved
}
