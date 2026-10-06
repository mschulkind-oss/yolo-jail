package capture

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ownership.go gives a capture's output to the user who owns the store it is admitted into.
//
// # Why the driver has to
//
// The host's admit freezes every file read-only (freezeTree) and GC unlinks whole entries, and
// both are the STORE OWNER'S acts: chmod needs the file's owner, unlink a writable directory. A
// container capture runs its installer as the jail's root, and on a ROOTLESS podman that root is
// the host user, so whatever root creates is the host user's. What root EXTRACTS is not: tar run
// as root keeps the archive's owner (GNU tar's --same-owner is root's default), copilot's and
// codex's release tarballs record 1001/1001, and container uid 1001 is a subordinate uid on the
// host. The admit then failed — `chmod …/.local/bin/copilot: operation not permitted` — and left
// an entry nobody but `podman unshare` could delete (Pack Installs, 2026-10-06).
//
// Only the driver can fix that: inside the jail it is root over every uid the jail maps, and on
// the host nothing short of the runtime's own user namespace is. So the driver hands the delta it
// moved out to the owner of the directory the host made for it — the capture act's scratch dir,
// which is the store owner's, seen through whatever uid mapping the backend applies (on a
// rootless podman, the jail's own uid 0). What the driver itself creates afterwards, the
// manifest, is root's and so already that user's.
//
// # Why only as root
//
// A driver that is not root cannot give a file away and never has one to give: on macos-user it
// runs as the sandbox account, whose files reach the host user through the staging tree's ACLs
// (macosuser.CaptureStagingCommands), and the Linux host arm runs as the host user itself. A
// chown attempted there would fail a capture whose tree is fine.
//
// It loosens nothing: the admit still freezes every file, and the owner it names could already
// write all of it, being the jail's root on a rootless podman and the store's owner on any.

// fileOwner is a uid and gid pair.
type fileOwner struct{ uid, gid int }

// outOwner is the owner of out or, when out does not exist yet, of its nearest existing ancestor:
// the scratch directory the host act made. ok is false where the platform reports no owner.
func outOwner(out string) (fileOwner, bool) {
	for p := filepath.Clean(out); ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err == nil {
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return fileOwner{}, false
			}
			return fileOwner{uid: int(st.Uid), gid: int(st.Gid)}, true
		}
		if !errors.Is(err, fs.ErrNotExist) || p == filepath.Dir(p) {
			return fileOwner{}, false
		}
	}
}

// giveTo hands every path under root (root included) that o does not own to o, links themselves
// rather than what they point at. A no-op unless this process is root, for the reason above.
//
// Root's chown clears a regular file's setuid and setgid bits, so it runs before the manifest
// records modes, and the manifest describes the tree the store gets.
func (o fileOwner) giveTo(root string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || (int(st.Uid) == o.uid && int(st.Gid) == o.gid) {
			return nil
		}
		if err := os.Lchown(p, o.uid, o.gid); err != nil {
			return fmt.Errorf("giving %s to uid %d, who owns the store it is admitted into: %w", p, o.uid, err)
		}
		return nil
	})
}
