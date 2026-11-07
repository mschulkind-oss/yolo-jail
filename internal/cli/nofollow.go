package cli

// nofollow.go is the no-follow directory walk the program capture reads a failure file through
// (readProgramFailureFile): every component is opened from a pinned root with O_NOFOLLOW, so a
// component that is a symbolic link is refused, not followed.

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// noFollowDir is the flag set for every directory open on a no-follow walk.
const noFollowDir = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

// errLinkedParent is a component on the way to a file that is not a real directory.
type errLinkedParent struct{ rel string }

func (e errLinkedParent) Error() string {
	return e.rel + " is a symbolic link or not a directory, and yolo reads nothing through it"
}

// openParentNoFollow opens the directory holding rel (slash-separated, relative to rootFD) by a
// walk from rootFD, one component at a time, following no link. The caller closes what it returns.
func openParentNoFollow(rootFD int, rel string) (int, error) {
	parts := strings.Split(rel, "/")
	fd, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for i, c := range parts[:len(parts)-1] {
		next, oerr := unix.Openat(fd, c, noFollowDir, 0)
		_ = unix.Close(fd)
		if oerr != nil {
			if errors.Is(oerr, unix.ELOOP) || errors.Is(oerr, unix.ENOTDIR) {
				return -1, errLinkedParent{rel: strings.Join(parts[:i+1], "/")}
			}
			return -1, oerr
		}
		fd = next
	}
	return fd, nil
}
