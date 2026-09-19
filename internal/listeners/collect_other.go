//go:build !linux

package listeners

import "runtime"

// Collect off Linux returns the UNAVAILABLE snapshot, naming the platform.
//
// This is the truthful answer rather than the convenient one: darwin has no
// /proc/net/tcp, and the macos-user backend bakes no tools, so an empty socket list
// there would read as "nothing is listening" when the real answer is "this
// instrument does not work here". [Snapshot.Availability] returns [Unavailable]
// because no table was read, which is the same shape a Linux host with an
// unreadable /proc produces — a caller that handles one handles the other.
//
// Nothing in this file is a second code path: the parsers are platform-independent
// and their tests run on every GOOS. Only the entry point differs.
func Collect() Snapshot {
	return Snapshot{
		TablesAttempted: len(tables),
		Gaps: []Gap{{
			Source: ProcRoot,
			Reason: "not available on " + runtime.GOOS + " (this instrument reads Linux /proc)",
		}},
	}
}

// Supported reports whether this platform has the /proc interfaces this package
// reads. It is a build-time fact, not a probe.
func Supported() bool { return false }
