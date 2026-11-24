package resourcegroups

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ObserveMemberDir reads <procRoot>/<pid>/cgroup and maps its cgroup-v2 path
// through the mount table to the filesystem directory, the form a membership
// comparison needs. Anything but exactly one unified-hierarchy line is not
// membership it can witness. An empty procRoot or mountInfoPath means /proc and
// /proc/self/mountinfo.
func ObserveMemberDir(procRoot, mountInfoPath string, pid int) (string, error) {
	if procRoot == "" {
		procRoot = "/proc"
	}
	if mountInfoPath == "" {
		mountInfoPath = "/proc/self/mountinfo"
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "0::") {
		return "", errors.New("process is not on a pure cgroup-v2 hierarchy, so its membership cannot be witnessed")
	}
	controlGroup := strings.TrimPrefix(lines[0], "0::")
	if !validControlGroup(controlGroup) {
		return "", fmt.Errorf("process cgroup %q is not a usable path", controlGroup)
	}
	mountInfo, err := os.ReadFile(mountInfoPath)
	if err != nil {
		return "", err
	}
	mount, err := cgroup2MountFor(mountInfo, controlGroup)
	if err != nil {
		return "", err
	}
	return mapControlGroup(mount, controlGroup)
}

// InsideGroup reports whether member is the group's cgroup directory or one of
// its descendants: a transient scope placed under a slice is inside it.
func InsideGroup(member, group string) bool {
	return group != "" && pathWithin(member, group)
}
