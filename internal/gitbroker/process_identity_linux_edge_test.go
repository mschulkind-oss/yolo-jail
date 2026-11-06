//go:build linux

package gitbroker

import (
	"os"
	"syscall"
	"testing"
)

func TestProcessIdentityTreatsProcReadESRCHAsExited(t *testing.T) {
	start, live, err := processIdentityRead(42, func(path string) ([]byte, error) {
		if path != "/proc/42/stat" {
			t.Fatalf("unexpected stat path: %s", path)
		}
		return nil, &os.PathError{Op: "read", Path: path, Err: syscall.ESRCH}
	})
	if err != nil || live || start != "" {
		t.Fatalf("process vanished during proc read: start=%q live=%t err=%v", start, live, err)
	}
}
