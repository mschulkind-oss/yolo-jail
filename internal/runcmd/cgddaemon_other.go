//go:build !linux

package runcmd

// startCgroupDelegateInProc is Linux-only (cgroup v2). Off Linux the cgroup
// delegate is never started (matching _start_host_service_builtin_cgroup's
// macOS early return).
func (o *Options) startCgroupDelegateInProc(cname, rt, sockPath string) (func(), bool) {
	return nil, false
}
