//go:build linux

package runcmd

import "golang.org/x/sys/unix"

// isattyFD reports whether fd is a terminal (a TCGETS ioctl succeeds), matching
// Python's os.isatty.
func isattyFD(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

// sysconfPhysMem returns total physical memory in bytes. Python uses
// SC_PAGE_SIZE * SC_PHYS_PAGES; unix.Sysinfo's Totalram*Unit is the equivalent
// total-RAM figure. ok=false on error. This feeds only the Apple-Container
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
// it is RLIM_INFINITY (→ the "memlock=-1:-1" literal). Mirrors
// resource.getrlimit(RLIMIT_MEMLOCK)[1].
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
