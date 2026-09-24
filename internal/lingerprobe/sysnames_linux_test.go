//go:build linux

package lingerprobe

import (
	"runtime"
	"testing"
)

// TestAmd64OnlySyscallsAreNamed pins the four calls amd64 has and arm64 does not, which the
// table adds by number because x/sys defines their constants only for amd64. A lingering
// podman thread in poll/select/epoll_wait would otherwise render as syscall_<nr>.
func TestAmd64OnlySyscallsAreNamed(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64-only syscall numbers")
	}
	for nr, want := range map[int]string{7: "poll", 23: "select", 34: "pause", 232: "epoll_wait"} {
		if got := syscallName(nr); got != want {
			t.Errorf("syscallName(%d) = %q, want %q", nr, got, want)
		}
	}
}
