package stateguard

import (
	"errors"
	"io/fs"
	"syscall"
)

// lstatIdentity is path's device and inode, without following a link at path. A parent that is
// no longer a directory is reported as not existing: the name is gone either way.
func lstatIdentity(path string) (dev, ino uint64, err error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		if errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ENOENT) {
			return 0, 0, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
		}
		return 0, 0, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	return uint64(st.Dev), st.Ino, nil //nolint:unconvert // Dev is int32 on darwin
}
