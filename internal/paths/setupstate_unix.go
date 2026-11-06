//go:build linux || darwin

package paths

import (
	"errors"
	"io/fs"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"golang.org/x/sys/unix"
)

// EnsureSetupStateDir creates <workspace>/.yolo/setups/<name> and returns it open, so a caller
// acts on the directory it was handed rather than on a path a jail can re-point.
//
// `.yolo` is jail-writable (statefile.go), so the last jail can leave a symbolic link at
// `.yolo`, `setups` or `<name>`. Every step is a descriptor-relative mkdirat/openat with
// O_NOFOLLOW from the directory the step before opened, never a path walk: a link at any of the
// three is refused with a *LinkedStateDirError naming it, before anything is written beneath it.
// `.yolo` itself is created by EnsureWorkspaceStateDir, the chokepoint that keeps one out of
// the directories that may never hold one. name must be a setup name (setup.ValidName).
func EnsureSetupStateDir(workspace, name string) (*os.File, error) {
	full := SetupStateDir(workspace, name)
	if !setup.ValidName(name) {
		return nil, &fs.PathError{Op: "mkdir", Path: full, Err: fs.ErrInvalid}
	}
	dir, err := EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return nil, err
	}
	yolo, err := openDirNoFollowAt(unix.AT_FDCWD, dir, dir)
	if err != nil {
		return nil, err
	}
	defer unix.Close(yolo)
	setups, err := mkdirOpenNoFollowAt(yolo, "setups", SetupsDir(workspace))
	if err != nil {
		return nil, err
	}
	defer unix.Close(setups)
	fd, err := mkdirOpenNoFollowAt(setups, name, full)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), full), nil
}

// mkdirOpenNoFollowAt creates name beneath parent when it is absent and opens it as a directory
// without following a link.
func mkdirOpenNoFollowAt(parent int, name, full string) (int, error) {
	if err := unix.Mkdirat(parent, name, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, &fs.PathError{Op: "mkdirat", Path: full, Err: err}
	}
	return openDirNoFollowAt(parent, name, full)
}

// openDirNoFollowAt opens name beneath parent as a directory, refusing a symbolic link at name
// with a *LinkedStateDirError naming full.
func openDirNoFollowAt(parent int, name, full string) (int, error) {
	for {
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			return fd, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		var st unix.Stat_t
		if unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return -1, &LinkedStateDirError{Path: full}
		}
		return -1, &fs.PathError{Op: "openat", Path: full, Err: err}
	}
}
