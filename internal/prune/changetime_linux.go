//go:build linux

package prune

import (
	"syscall"
	"time"
)

// statChangeTime is st's inode change time (st_ctim). See storePathAge for why
// a store path is aged by it.
func statChangeTime(st *syscall.Stat_t) time.Time {
	return time.Unix(st.Ctim.Unix())
}
