package prune

import (
	"os"
	"syscall"
)

// inode returns (ino, dev, ok) for path via lstat. Used to detect already-
// hardlinked pairs (skip same-inode) — the dedup correctness guard. Both
// linux and darwin expose Ino/Dev on syscall.Stat_t, so this is portable
// across the supported hosts.
func inode(path string) (ino uint64, dev uint64, ok bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Ino, uint64(st.Dev), true
}
