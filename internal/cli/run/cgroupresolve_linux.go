//go:build linux

package run

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// resolveContainerCgroup returns the host-side cgroup v2 path for a running
// container, or "" (macOS → always ""). Best-effort. Linux-only: the sole
// caller is startCgroupDelegateInProc in cgddaemon_linux.go, so keeping it in a
// linux-tagged file avoids a darwin-build "unused" lint (the darwin toolchain
// never compiles the caller).
func (o *Options) resolveContainerCgroup(cname, rt string) string {
	if o.IsMacOS {
		return ""
	}
	if rt == "podman" {
		res := o.Exec([]string{"podman", "inspect", "--format", "{{.State.CgroupPath}}", cname}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			if cg := strings.TrimSpace(res.Stdout); cg != "" {
				cand := filepath.Join("/sys/fs/cgroup", cg)
				if o.PathExists(cand) {
					return cand
				}
			}
		}
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{.State.Pid}}", cname}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil || pid <= 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			cand := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(parts[2], "/"))
			if o.PathExists(cand) {
				return cand
			}
		}
	}
	return ""
}
