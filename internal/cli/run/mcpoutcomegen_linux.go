//go:build linux

package run

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fsIocGetVersion is FS_IOC_GETVERSION, _IOR('v', 1, long) in the generic ioctl encoding.
// golang.org/x/sys/unix does not export it. On an architecture with another encoding the
// request is simply unknown to the filesystem, and the generation reads as unreported.
const fsIocGetVersion = 2<<30 | uint(unsafe.Sizeof(uintptr(0)))<<16 | 'v'<<8 | 1

// mcpOutcomeInodeGeneration reads the inode's generation number, which the filesystem
// assigns afresh each time it allocates an inode, so an inode NUMBER that ext4 or XFS hands
// straight back to the next new file carries a different generation. ext4, XFS and btrfs
// report it; tmpfs does not (its inode numbers are not reused), and then ok is false.
//
// The buffer is the request's full encoded size, a long: ext4 and btrfs store an int, but a
// FUSE filesystem copies out the whole size, and a 4-byte buffer would be overrun. The value
// is only ever compared with another read of the same kind, so which bytes hold it does not
// matter.
func mcpOutcomeInodeGeneration(f *os.File) (gen uint64, ok bool) {
	conn, err := f.SyscallConn()
	if err != nil {
		return 0, false
	}
	var errno unix.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, uintptr(fsIocGetVersion), uintptr(unsafe.Pointer(&gen)))
	}); err != nil || errno != 0 {
		return 0, false
	}
	return gen, true
}
