//go:build linux

package check

import (
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// accessRW reports whether the current process can open the
// node for read+write. Uses the real access(2) so it honors the effective
// uid/gid.
func accessRW(path string) bool {
	return unix.Access(path, unix.R_OK|unix.W_OK) == nil
}

// nodeGIDReal returns the node's owning gid and group name. ok=false
// when the node can't be stat'd. A gid with no group entry
// falls back to its decimal string.
func nodeGIDReal(path string) (int, string, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, "", false
	}
	gid := int(st.Gid)
	name := strconv.Itoa(gid)
	if g, err := user.LookupGroupId(name); err == nil {
		name = g.Name
	}
	return gid, name, true
}

// inUserGroupsReal reports whether gid is in the process's supplementary groups.
func inUserGroupsReal(gid int) bool {
	groups, err := unix.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g == gid {
			return true
		}
	}
	return false
}

// isExecutable reports whether path is executable by the current process.
func isExecutable(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}
