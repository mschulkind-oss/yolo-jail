//go:build linux

package unifiedhome

import "golang.org/x/sys/unix"

// defaultRename renames old in oldDir to new in newDir, failing with EEXIST rather than
// replacing an existing new.
func defaultRename(oldDir int, old string, newDir int, new string) error {
	return unix.Renameat2(oldDir, old, newDir, new, unix.RENAME_NOREPLACE)
}
