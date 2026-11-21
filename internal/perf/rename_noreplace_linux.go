package perf

import "golang.org/x/sys/unix"

func renameNoReplaceAt(dirfd int, old, new string) error {
	return unix.Renameat2(dirfd, old, dirfd, new, unix.RENAME_NOREPLACE)
}
