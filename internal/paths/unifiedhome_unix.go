//go:build linux || darwin

package paths

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// EnsureUnifiedHome creates <workspace>/.yolo/unified-home and returns it open, on the terms of
// EnsureSetupStateDir: `.yolo` is jail-writable, so a link at `.yolo` or at the unified home is
// refused with a *LinkedStateDirError naming it, before anything is written beneath it. Each step
// is a descriptor-relative mkdirat/openat with O_NOFOLLOW, and `.yolo` itself is created by
// EnsureWorkspaceStateDir, the chokepoint that keeps one out of the directories that may never
// hold one.
func EnsureUnifiedHome(workspace string) (*os.File, error) {
	// A dangling link at `.yolo` is named as one here; MkdirAll would report a bare "file exists".
	if fi, err := os.Lstat(WorkspaceStateDir(workspace)); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &LinkedStateDirError{Path: WorkspaceStateDir(workspace)}
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
	full := UnifiedHome(workspace)
	fd, err := mkdirOpenNoFollowAt(yolo, UnifiedHomeName, full)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), full), nil
}
