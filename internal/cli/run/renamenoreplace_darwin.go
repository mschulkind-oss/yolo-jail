//go:build darwin

package run

import "golang.org/x/sys/unix"

// renameNoReplaceAt renames old to new within the directory dirfd names, failing with EEXIST
// rather than replacing an existing new.
func renameNoReplaceAt(dirfd int, old, new string) error {
	return unix.RenameatxNp(dirfd, old, dirfd, new, unix.RENAME_EXCL)
}
