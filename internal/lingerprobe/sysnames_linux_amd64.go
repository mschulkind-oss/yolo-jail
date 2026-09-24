package lingerprobe

import "golang.org/x/sys/unix"

// The amd64 calls arm64 never had: its table starts from the generic one,
// which dropped the non-p/non-at variants. Added in init rather than by a
// `linux && !amd64` twin, which no lint pass would ever read.
func init() {
	for nr, name := range map[int]string{
		unix.SYS_POLL: "poll", unix.SYS_SELECT: "select", unix.SYS_EPOLL_WAIT: "epoll_wait",
		unix.SYS_PAUSE: "pause",
	} {
		sysNames[nr] = name
	}
}
