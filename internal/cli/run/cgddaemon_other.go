//go:build !linux

package run

// startCgroupDelegateInProc is Linux-only (cgroup v2). Off Linux the cgroup
// delegate is never started.
//
// SILENT ON PURPOSE, and it is the one decline in this pair that must stay so. The
// Linux file reports each of its three declines (a runtime fact about one host, which
// nothing else can state), while off Linux the answer is the PLATFORM axis — the
// manifest declares `platforms: ["linux"]`, so the record is not Active here and the
// inert report already says this loophole does nothing on this machine
// (loopholeinert.go, docs/reference/loophole-system.md#where-a-loophole-does-nothing:
// one mechanism, one message rendering). A line here would be the second half-message
// that section exists to prevent — and it would be unreachable besides, since an
// inactive loophole never reaches startCgroupDelegate.
func (o *Options) startCgroupDelegateInProc(cname, rt, sockPath string) (func(), bool) {
	return nil, false
}
