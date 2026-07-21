//go:build linux

package run

import "golang.org/x/sys/unix"

// isattyFD reports whether fd is a terminal (a TCGETS ioctl succeeds).
func isattyFD(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

// sysconfPhysMem returns total physical memory in bytes via unix.Sysinfo
// (Totalram*Unit). ok=false on error. This feeds only the Apple-Container
// default-memory path's non-macOS branch, which is effectively unreachable (AC
// is macOS-only), so exactness here is not load-bearing.
func sysconfPhysMem() (int64, bool) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, false
	}
	unit := int64(si.Unit)
	if unit == 0 {
		unit = 1
	}
	return int64(si.Totalram) * unit, true
}

// hostHardMemlock returns the host's hard RLIMIT_MEMLOCK. unlimited is true when
// it is RLIM_INFINITY (→ the "memlock=-1:-1" literal).
func hostHardMemlock() (hard int64, unlimited bool) {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &rl); err != nil {
		return 0, true
	}
	if rl.Max == unix.RLIM_INFINITY {
		return 0, true
	}
	return int64(rl.Max), false
}
