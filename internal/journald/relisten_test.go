package journald

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestServeReleasesSocketPathOnReturn is the regression for the leftover-unlink
// bug: the accept loop's stop-watching goroutine used to call os.Remove(socket) as
// well as the post-loop path, and nothing waits for that goroutine. It could return
// (having unlinked the path itself) with a second, unsequenced os.Remove still
// pending — so a caller that re-served the SAME path had its brand-new socket
// file deleted by its predecessor. The new listener stayed bound to an unlinked
// inode: accepting, but unreachable by name, so every dial failed forever with no
// error reported anywhere.
//
// Drives serveUnixConns directly, for main_test.go's reason: the exported entry
// point above it consumes a connection preamble this test does not speak, and the
// unlink ownership under test is the accept loop's, not the front's.
//
// This is a RACE DETECTOR, not a proof, and it is pinned to one thread on purpose.
// The window is between the accept loop's return and its leftover goroutine being
// scheduled, so detection depends on the scheduler: with the bug reintroduced
// this caught it in 10/10 runs under GOMAXPROCS=1 but only 1/10 at the default on
// a 12-core host. The bug it guards is scheduler-dependent in exactly the same
// way, which is how it read as "a slow CI runner" for two days — so the test
// forces the conditions that make it visible instead of hoping for them.
func TestServeReleasesSocketPathOnReturn(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	sock := filepath.Join(shortSockDir(t), "j.sock")
	for i := 0; i < 60; i++ {
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := serveUnixConns(sock, stop, func(c net.Conn) { handleConn(c, "user") }); err != nil {
				t.Errorf("iter %d: serveUnixConns: %v", i, err)
			}
		}()

		var dialable bool
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
				c.Close()
				dialable = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !dialable {
			// Name the mechanism, not just the symptom: an absent socket FILE means a
			// predecessor unlinked it, which is a different bug from a listener that
			// never bound.
			_, serr := os.Stat(sock)
			t.Fatalf("iter %d: socket never became dialable; stat(%s) = %v "+
				"(absent file => a previous accept loop's leftover goroutine unlinked this listener's path)",
				i, sock, serr)
		}
		close(stop)
		<-done
	}
}

// shortSockDir returns a temp dir whose path is short enough to hold an AF_UNIX
// socket, because t.TempDir() alone is not.
//
// t.TempDir() embeds the TEST'S OWN NAME, and sun_path is 104 bytes on macOS (108
// on Linux). Under macOS's long $TMPDIR (`/var/folders/xx/<28 chars>/T/`) this
// test's name pushed the socket to 105 bytes and bind failed with the maximally
// unhelpful `invalid argument` — a test that appears to catch a daemon bug while
// actually only measuring the length of its own identifier. The existing
// TestNoTruncationRace lands at 90 bytes, i.e. 14 bytes of headroom that nobody
// declared and a rename could silently spend.
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "jd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if n := len(dir) + len("/j.sock"); n > 100 {
		t.Skipf("temp dir too long for an AF_UNIX path (%d bytes): %s", n, dir)
	}
	return dir
}
