//go:build linux

package capture

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// clone_linux.go is the REFLINK primitive materialize is built on, plus the filesystem
// name a fallback has to report.
//
// # Why reflink and not link(2)
//
// The store is a HOST directory and a jail reaches it through a bind mount, so the store
// and the home a capture materializes into are ALWAYS two different mounts. `link(2)`
// compares the MOUNT — MEASURED in this jail 2026-09-04, a hardlink from the workspace
// bind into the /home/agent/.local bind returns EXDEV even though both are one btrfs with
// one st_dev — so the design's "unpack/hardlink" cannot be the primary mechanism here at
// all (docs/design/program-delivery.md §6.3, amended).
//
// FICLONE compares the FILESYSTEM instead: the kernel's clone path refuses only when
// `inode_out->i_sb != inode_in->i_sb`, and every bind mount of one filesystem shares its
// superblock. That is exactly the gap link(2) falls into. MEASURED the same day, in a real
// podman container with the two mounts a materialize actually uses — a `:ro` bind of the
// store at /ctx/captures and a rw bind of the home surface at /home/agent/.local, both on
// one btrfs:
//
//	FICLONE  /ctx/captures/src.bin -> /home/agent/.local/dst.bin   OK   (256 MiB in 3 ms,
//	                                                                     32 KiB of new space)
//	link(2)  /ctx/captures/src.bin -> /home/agent/.local/dst.bin   EXDEV
//
// A cloned file is its OWN INODE (nlink 1) sharing EXTENTS with the source: writing to it
// copies-on-write and touches nothing else. That is a strictly better property than a
// hardlink's, not merely an equal one — see the hazard note on Materialize.
//
// # Where it does not work, and why that is a real path
//
// btrfs, XFS with reflink=1, and ZFS support it; ext4 does not, and ext4 is the default
// filesystem of most Linux installs and of every GitHub runner. So the copy fallback is a
// path this code takes in production, not a theoretical arm — which is why it is loud, and
// why fsName exists: "your store and your home are on ext4" is the whole explanation, and a
// message that omitted it would leave a user with a slow materialize and no lead.

// errCloneUnsupported reports that this platform, filesystem or mount pair cannot reflink.
// It is a distinct error from a real I/O failure: the first retires the mechanism for the
// rest of the run, the second fails the materialize.
var errCloneUnsupported = fmt.Errorf("reflink is not supported here")

// cloneFile makes dst a reflink (copy-on-write clone) of src.
//
// Both must already be open — dst for writing, freshly created and empty. FICLONE replaces
// dst's ENTIRE contents, so a partially written destination is not a hazard, but a
// destination that already had a hardlink to something else would be: materialize unlinks
// before it creates, for that reason.
//
// A "this cannot work here" errno is wrapped in errCloneUnsupported so the caller can retire
// reflink for the whole run after ONE failed ioctl rather than paying one per file. The five
// spellings are all real: EXDEV for two different filesystems, EOPNOTSUPP for a filesystem
// with no clone op, ENOTTY for one that does not know the ioctl at all, EINVAL for a mount
// option (XFS without reflink=1) or an unaligned/unsupported case, and ENOSYS for a kernel
// without the interface.
func cloneFile(dst, src *os.File) error {
	err := unix.IoctlFileClone(int(dst.Fd()), int(src.Fd()))
	if err == nil {
		return nil
	}
	switch err {
	case unix.EXDEV, unix.EOPNOTSUPP, unix.ENOTTY, unix.EINVAL, unix.ENOSYS, unix.EPERM:
		return fmt.Errorf("%w: %v", errCloneUnsupported, err)
	}
	return err
}

// fsName is the filesystem type at path, for the message a copy fallback owes its reader.
//
// BY MAGIC NUMBER, because that is what statfs answers and it is the same number on every
// distribution; /proc/mounts would give a prettier name and a different answer inside a
// mount namespace. An unrecognised filesystem is reported as its magic in hex rather than as
// "unknown": the number is searchable and the name is not.
//
// "" when the path cannot be stat'd at all — a caller that cannot name the filesystem still
// has to report the copy.
func fsName(path string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return ""
	}
	//nolint:unconvert // st.Type is int64 on some arches and uint32 on others.
	switch magic := int64(st.Type); magic {
	case unix.BTRFS_SUPER_MAGIC:
		return "btrfs"
	case unix.XFS_SUPER_MAGIC:
		return "xfs"
	// EXT2_SUPER_MAGIC, EXT3 and EXT4 are ONE number (0xef53); the kernel does not
	// distinguish them through statfs, so neither can this.
	case unix.EXT4_SUPER_MAGIC:
		return "ext2/3/4"
	case unix.TMPFS_MAGIC:
		return "tmpfs"
	case unix.OVERLAYFS_SUPER_MAGIC:
		return "overlayfs"
	case unix.NFS_SUPER_MAGIC:
		return "nfs"
	case zfsSuperMagic:
		return "zfs"
	default:
		return fmt.Sprintf("fstype 0x%x", magic)
	}
}

// zfsSuperMagic is ZFS's statfs magic. It is spelled here because golang.org/x/sys/unix does
// not carry it — ZFS is out of tree — and it is worth naming because ZFS is the third
// filesystem on which the reflink path actually works.
const zfsSuperMagic = 0x2fc12fc1
