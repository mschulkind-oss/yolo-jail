package integration

import (
	"strings"
	"testing"
	"time"
)

// TestOpenTestPtyCarriesBytesBothWays runs openTestPty with no container, so it runs under -short
// on both of its halves: the Linux one in check-go, and the darwin one in ci.yml's check-macos on
// every push to main, so that half runs before the Apple Container job's measures depend on it
// (pty_darwin_test.go). A line written at the master reaches the slave through the line
// discipline, and a line written at the slave reaches the master.
func TestOpenTestPtyCarriesBytesBothWays(t *testing.T) {
	master, slave := openTestPty(t)
	fromMaster, fromSlave := &syncBuffer{}, &syncBuffer{}
	// Each reader stops once it has what it waits for: both ends are this process's own, and a
	// read still blocked on either would hold its descriptor open past the cleanup's close.
	drain := func(r interface{ Read([]byte) (int, error) }, into *syncBuffer, until string) {
		buf := make([]byte, 256)
		for !strings.Contains(into.String(), until) {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = into.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	go drain(master, fromMaster, "to-the-master")
	go drain(slave, fromSlave, "to-the-slave")
	await := func(b *syncBuffer, want, what string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !strings.Contains(b.String(), want); {
			if time.Now().After(deadline) {
				t.Fatalf("%s never carried %q; it read %q", what, want, b.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if _, err := master.Write([]byte("to-the-slave\n")); err != nil {
		t.Fatalf("writing at the master: %v", err)
	}
	await(fromSlave, "to-the-slave", "the slave")
	if _, err := slave.Write([]byte("to-the-master\n")); err != nil {
		t.Fatalf("writing at the slave: %v", err)
	}
	await(fromMaster, "to-the-master", "the master")
}
