//go:build unix

package helperpin

import (
	"os"
	"syscall"
)

func devIno(fi os.FileInfo) (dev, ino uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino) //nolint:unconvert // widths differ per platform
	}
	return 0, 0
}
