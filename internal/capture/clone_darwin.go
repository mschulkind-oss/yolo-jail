//go:build darwin

package capture

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// clone_darwin.go is the macOS half of the reflink primitive: APFS's clonefile(2), plus the
// filesystem name a copy fallback owes its reader. clone_linux.go is the Linux half, and of its
// header's argument for reflink over link(2) only the own-inode half carries over. Its core is
// the bind mount: in a jail the store and the home are always two mounts, so link(2) answers
// EXDEV where FICLONE works. No bind mount separates a store from a home on a Mac's own disk,
// so the mount gives link(2) no reason to fail there. Reflink is tried first on macOS for
// isolation alone: a clone is its own inode and can take the manifest's mode, where a hardlink
// is the store's frozen inode.
//
// # The same arm, a different call shape
//
// clonefile(2) takes two PATHS and creates the destination itself, atomically, where Linux's
// FICLONE takes two open descriptors and fills a destination the caller made. So the split is
// the whole per-file act (reflinkFile), not one ioctl: there is nothing to open, nothing to
// remove when the clone fails (a failed call creates nothing), and a destination that already
// exists is the call's own EEXIST. Its predicate is the VOLUME: a clone into another APFS
// volume (the Nix store's, an external disk's) is EXDEV, and onto a filesystem with no clone
// support (HFS+, SMB, FAT) is ENOTSUP. A store and a home on one APFS volume, the layout of a
// Mac's own disk, clone.
//
// # What a clone carries
//
// A clone is its own inode (nlink 1) sharing extents with the source, so a write to it
// copies-on-write and leaves the store alone, exactly as a Linux reflink does. It gets its own
// COPY of the source's metadata, less what clonefile(2) excepts: ownership is the caller's
// (CLONE_NOOWNERCOPY makes that so for root too, who would otherwise get the store owner's) and
// setuid/setgid are cleared. The mode it copies is the store's FROZEN one (freezeTree drops
// every write bit at admit, so a vendor's 0755 is 0555 there), so reflinkFile sets the
// manifest's mode afterwards, as the Linux half does. Extended attributes come across too, by
// the man page, as they do through a hardlink, which IS the source: a quarantine or provenance
// attribute on a store file reaches the home on either arm, so Gatekeeper should judge a clone
// as it would the hardlink (NOT MEASURED on a Mac). Whether an ACL comes across is NOT MEASURED;
// yolo sets none on store files.
//
// # Who reaches it
//
// On darwin the chain is reached today by CopyTree: a patched extension's per-launch copy, which
// a launch on a container backend makes from the store on the Mac itself (treecopy.go,
// internal/cli's copyTreeForLaunch). Materialize reaches it on a Mac once the macOS host floor
// or macos-user's store access lands (docs/plans/install-capture.md, slice 6's H4). Its one
// measurement is clone_darwin_test.go, which check-macos runs same-user and unsandboxed. A
// clone from a store the invoking user owns into the macos-user sandbox account's sidecar,
// under Seatbelt, is NOT MEASURED: it may succeed, answer EPERM (the chain falls to hardlink,
// then copy), or answer EXDEV.

// errCloneUnsupported reports that this platform, filesystem or volume pair cannot reflink.
// It is a distinct error from a real I/O failure: the first retires the mechanism for the
// rest of the run, the second fails the materialize.
var errCloneUnsupported = fmt.Errorf("reflink is not supported here")

// reflinkFile is the chain's reflink arm on macOS (reflinkOne): dst, which must not exist, is
// made a clonefile(2) of src and given perm.
//
// CLONE_NOFOLLOW so a src that is a link is cloned as the link rather than through it, though
// regularSource has already refused one. A failure is never cleaned up: the call creates dst
// only when it succeeds, so whatever is at dst after a failure was there before it, and is not
// this call's to remove.
func reflinkFile(src, dst string, perm fs.FileMode) error {
	if err := clonefile(src, dst, unix.CLONE_NOFOLLOW|unix.CLONE_NOOWNERCOPY); err != nil {
		return clonefileError(src, dst, err)
	}
	// The clone carries the store's frozen mode; it is its own inode, so the manifest's mode is
	// safe to set.
	return os.Chmod(dst, perm)
}

// clonefile is the system call reflinkFile makes, behind a var so a test can answer for the
// filesystem. The "not here" errnos come from layouts check-macos's one APFS disk does not have
// (another volume, HFS+ or SMB, a Seatbelt denial), so without it nothing would run the fallback
// through reflinkFile's own call (clone_darwin_test.go).
var clonefile = unix.Clonefile

// clonefileError sorts a clonefile(2) failure into "not here", which retires reflink for the
// run, and a fact about this one file, which fails it.
//
// The five "not here" spellings: ENOTSUP for a filesystem with no clone support, and
// EOPNOTSUPP beside it, because on darwin they are two errnos (45 and 102) where Linux has one;
// EXDEV for two volumes; ENOSYS for a kernel without the call; and EPERM, which is how Seatbelt
// answers an operation its profile denies, so that a sandbox that forbids the clone falls through
// to the arms it may allow rather than failing the materialize. Anything else (EEXIST, EACCES,
// ENOENT, an I/O error) is returned with both paths and its errno intact.
func clonefileError(src, dst string, err error) error {
	switch err {
	case unix.ENOTSUP, unix.EOPNOTSUPP, unix.EXDEV, unix.ENOSYS, unix.EPERM:
		return fmt.Errorf("%w: clonefile: %v", errCloneUnsupported, err)
	}
	return &os.LinkError{Op: "clonefile", Old: src, New: dst, Err: err}
}

// fsName is the filesystem type at path, for the message a copy fallback owes its reader.
//
// By NAME, because that is what darwin's statfs answers (f_fstypename: "apfs", "hfs", "smbfs");
// there is no magic number to translate. "" when the path cannot be stat'd at all — a caller
// that cannot name the filesystem still has to report the copy.
func fsName(path string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return ""
	}
	return unix.ByteSliceToString(st.Fstypename[:])
}
