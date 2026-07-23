package prune

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// legacyBuildRootPrefixes are the on-disk names of the OLD nix-build-root
// staging mechanism (source staged on the host, bind-mounted into the jail at
// /opt/yolo-jail). That mechanism is gone — the image now bakes the flake bundle
// (installPrefix in flake.nix) and there is no source bind — so any of these
// dirs left under GlobalStorage is a pre-upgrade orphan.
var legacyBuildRootPrefixes = []string{
	"nix-build-root", // covers "nix-build-root" and "nix-build-root.old.*"
	"nix-build-tmp-",
}

// PruneLegacyBuildRoots reclaims the pre-upgrade nix-build-root* staging dirs.
// No liveness gate is needed: nothing binds these any more (a still-running
// pre-upgrade jail holds the inode alive through its own mount, so deleting the
// host dir is safe), and nothing creates new ones. An age grace floor still
// skips a dir touched within olderThan, so we never yank a *.tmp a concurrent
// legacy build (mid-upgrade window) is actively writing. Returns (bytesRemoved,
// dirsRemoved); apply=false reports without touching disk.
func PruneLegacyBuildRoots(globalStorage string, olderThan time.Duration, apply bool, now time.Time) (bytesRemoved int64, dirsRemoved int) {
	info, err := os.Stat(globalStorage)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	children, err := os.ReadDir(globalStorage)
	if err != nil {
		return 0, 0
	}
	for _, c := range children {
		name := c.Name()
		if !hasLegacyBuildRootPrefix(name) {
			continue
		}
		child := filepath.Join(globalStorage, name)
		st, err := os.Lstat(child)
		if err != nil {
			continue
		}
		// Age grace floor — skip recent entries (mid-upgrade window).
		if now.Sub(st.ModTime()) < olderThan {
			continue
		}
		var size int64
		if st.Mode()&os.ModeSymlink == 0 && st.IsDir() {
			size = dirSizeBytes(child)
		}
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

// hasLegacyBuildRootPrefix reports whether name is a legacy nix-build-root*
// staging entry (the live "nix-build-root", an ".old.*" generation, or an
// in-flight "nix-build-tmp-*").
func hasLegacyBuildRootPrefix(name string) bool {
	for _, p := range legacyBuildRootPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// dirSizeBytes sums regular-file sizes under p (missing → 0), via lstat so
// symlinks are never followed.
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
