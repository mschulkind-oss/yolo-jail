package nixroots

import (
	"time"

	"golang.org/x/sys/unix"
)

// lchtimes sets a symlink's own mtime.
func lchtimes(path string, t time.Time) error {
	ts := []unix.Timespec{unix.NsecToTimespec(t.UnixNano()), unix.NsecToTimespec(t.UnixNano())}
	return unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW)
}
