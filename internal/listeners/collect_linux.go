//go:build linux

package listeners

// Collect reads the calling process's own /proc — which on Linux means THIS
// namespace's sockets, the only vantage point from which the answer is right. A
// jail's listening sockets are invisible from the host when the jail has its own
// network namespace, and the host's UNIX sockets are invisible from the jail; each
// side can only answer for itself.
func Collect() Snapshot { return CollectFrom(DirSource(ProcRoot), Options{}) }

// Supported reports whether this platform has the /proc interfaces this package
// reads. It is a build-time fact, not a probe.
func Supported() bool { return true }
