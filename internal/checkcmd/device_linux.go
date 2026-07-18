//go:build linux

package checkcmd

import (
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// accessRW ports os.access(path, R_OK|W_OK): can the current process open the
// node for read+write? Uses the real access(2) so it honors the effective
// uid/gid the way Python's os.access does.
func accessRW(path string) bool {
	return unix.Access(path, unix.R_OK|unix.W_OK) == nil
}

// nodeGIDReal ports node.stat().st_gid + grp.getgrgid(gid).gr_name. ok=false
// when the node can't be stat'd (the OSError branch). A gid with no group entry
// falls back to its decimal string (Python's KeyError → str(gid)).
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

// inUserGroupsReal ports `gid in set(os.getgroups())`.
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

// isExecutable ports os.access(path, os.X_OK).
func isExecutable(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}
