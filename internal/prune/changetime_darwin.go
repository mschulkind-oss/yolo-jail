//go:build darwin

package prune

import (
	"syscall"
	"time"
)

// statChangeTime is st's inode change time (st_ctimespec on darwin). See
// storePathAge for why a store path is aged by it.
func statChangeTime(st *syscall.Stat_t) time.Time {
	return time.Unix(st.Ctimespec.Unix())
}
