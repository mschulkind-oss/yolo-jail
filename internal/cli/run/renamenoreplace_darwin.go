//go:build darwin

package run

import "golang.org/x/sys/unix"

// renameNoReplaceAt renames old to new within the directory dirfd names, failing with EEXIST
// rather than replacing an existing new.
func renameNoReplaceAt(dirfd int, old, new string) error {
	return unix.RenameatxNp(dirfd, old, dirfd, new, unix.RENAME_EXCL)
}

// renameNoReplaceBetween renames old in the directory oldDir names to new in the one newDir
// names, failing with EEXIST rather than replacing an existing new.
func renameNoReplaceBetween(oldDir int, old string, newDir int, new string) error {
	return unix.RenameatxNp(oldDir, old, newDir, new, unix.RENAME_EXCL)
}
