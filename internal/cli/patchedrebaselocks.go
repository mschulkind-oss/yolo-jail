package cli

// patchedrebaselocks.go is where every `yolo pack rebase` takes its REBASE DIRECTORY LOCK
// (packsrc.Store.TryLockRebaseDir; docs/design/patched-forks.md PF-D48, PF-D73): the keyed verb and
// the scratch rebase alike, so a run of either refuses a second run of either on one clone
// directory, and neither takes the other's clone, half made, for its own nor removes it.
//
// THE LOCKS LIVE IN A DIRECTORY OF THIS USER'S ALONE, outside yolo's state directory, so the scratch
// rebase still writes nothing there (PF-D66): under $XDG_RUNTIME_DIR when it names a directory, which
// is the user's own, else under the temporary directory, which every terminal of a jail, or of the
// host, shares. That directory has a name anyone can guess, and a shared /tmp lets another local
// user make it first, or plant a link where a lock file goes, so it is made with mode 0700 and taken
// only when it is a real directory this user owns that no one else may write or enter; a lock file
// is opened without following a link.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// rebaseDirLocks is the store the rebase directory lock is taken in (see the file doc); a var so a
// test can place it.
var rebaseDirLocks = func() (*packsrc.Store, error) {
	dir, err := privateRebaseLockDir(os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(), os.Getuid())
	if err != nil {
		return nil, err
	}
	return &packsrc.Store{Dir: dir}, nil
}

// rebaseLockStep is the next step a lock directory that cannot be used names.
const rebaseLockStep = "set TMPDIR to a directory of yours (or unset it to use /tmp), then run the rebase again"

// privateRebaseLockDir is the lock directory for uid: yolo-rebase-locks-<uid> under runtimeDir when
// that is an absolute path to a directory, else under tmp. It makes it, mode 0700, and refuses it
// unless it is a directory, not a link, owned by uid, with no permission for anyone else.
func privateRebaseLockDir(runtimeDir, tmp string, uid int) (string, error) {
	base := tmp
	if runtimeDir != "" && filepath.IsAbs(runtimeDir) {
		if fi, err := os.Stat(runtimeDir); err == nil && fi.IsDir() {
			base = runtimeDir
		}
	}
	dir := filepath.Join(base, fmt.Sprintf("yolo-rebase-locks-%d", uid))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("making %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if why := notPrivateDir(fi, uid); why != "" {
		return "", fmt.Errorf("%s %s, so it is not this user's alone and no lock is taken in it", dir, why)
	}
	return dir, nil
}

// notPrivateDir says why fi is not a directory of uid's alone, "" when it is.
func notPrivateDir(fi fs.FileInfo, uid int) string {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return "is a symbolic link"
	case !fi.IsDir():
		return "is not a directory"
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		return fmt.Sprintf("belongs to user %d", st.Uid)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("has mode %04o, open to other users", perm)
	}
	return ""
}
