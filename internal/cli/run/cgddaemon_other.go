//go:build !linux

package run

// startCgroupDelegateInProc is Linux-only (cgroup v2). Off Linux the cgroup
// delegate is never started.
func (o *Options) startCgroupDelegateInProc(cname, rt, sockPath string) (func(), bool) {
	return nil, false
}
