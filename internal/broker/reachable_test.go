package broker

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sunPathMax is the longest AF_UNIX path that binds on the tighter of the two platforms:
// darwin's sun_path is 104 bytes INCLUDING the NUL, Linux's is 108.
const sunPathMax = 103

// shortSocketDir returns a 0700 per-test dir short enough to hold a socket.
//
// t.TempDir() is NOT, and the margin here was 9 bytes: a t.TempDir()-rooted `b.sock` comes to
// 94 bytes at darwin's real 45-byte TMPDIR (/var/folders/<2>/<26>/T/), against the 103-byte
// limit. So it passes today and one longer test name, one more nesting level, or a `/private`
// prefix tips it into a bare `bind: invalid argument` — an error naming neither the limit nor
// the fix. Reproduced on Linux by pointing TMPDIR at a long path, which is how this was found.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-brk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// assertSockPathFits fails with the actionable message instead of letting the bind return a
// bare "invalid argument".
func assertSockPathFits(t *testing.T, path string) {
	t.Helper()
	if len(path) > sunPathMax {
		t.Fatalf("socket path is %d bytes, over the %d-byte darwin sun_path limit:\n  %s\n"+
			"use shortSocketDir(t) — t.TempDir() is rooted at TMPDIR, which on macOS is "+
			"/var/folders/<2>/<26>/T/ (~45 bytes) before the test name is even appended",
			len(path), sunPathMax, path)
	}
}

// serveSilentListener binds a Unix socket whose accept loop reads to EOF and
// records every byte it was sent. It is the daemon side of the property below: a
// FRONTED daemon reads yolo's connection preamble first and answers nothing until
// it has one, so a prober that writes bytes and waits for a reply hangs — and a
// prober that writes the WRONG bytes is refused.
func serveSilentListener(t *testing.T, got *[]byte) string {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "b.sock")
	assertSockPathFits(t, sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b, rerr := io.ReadAll(conn)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return
		}
		*got = append(*got, b...)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return sock
}

// TestSingletonReachableWritesNothing is the REGRESSION this probe exists as, and
// it is on the credential path.
//
// The function it replaced (BrokerPing) wrote a framed `{"action":"ping"}` and
// waited for a pong. The singleton is behind yolo's front now, so its first read on
// every connection is the CONNECTION PREAMBLE — a framed JSON object carrying a
// version — and a bare ping is rejected as a preamble with no `v`. Under the old
// probe every healthy broker read as dead, brokerEnsure respawned the singleton on
// every launch, and `yolo broker status` said "no response" about a daemon serving
// jails perfectly well.
//
// So the property is not "the probe still works" but "the probe SENDS NOTHING": the
// preamble is yolo asserting which jail is on the other end, and a host-side
// liveness check belongs to no jail. A future edit that reinstates a protocol write
// here is reinstating a forged identity, which is why the assertion is on the bytes
// rather than on the return value alone.
func TestSingletonReachableWritesNothing(t *testing.T) {
	var got []byte
	sock := serveSilentListener(t, &got)
	if !SingletonReachable(sock, ReachTimeout) {
		t.Fatal("a listening socket read as unreachable")
	}
	// The connection must be closed by the probe, which is what lets the listener's
	// ReadAll return. Give it the same grace the cleanup does.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 0 {
		t.Errorf("the reachability probe wrote %d bytes (%q) — it must write NONE; a daemon "+
			"behind yolo's front reads a connection preamble first, and anything this side "+
			"could send is either rejected or a forged jail identity", len(got), got)
	}
}

// TestSingletonReachableNoSocket: nothing listening is unreachable.
func TestSingletonReachableNoSocket(t *testing.T) {
	if SingletonReachable(filepath.Join(t.TempDir(), "nope.sock"), 200*time.Millisecond) {
		t.Error("dialing a nonexistent socket should be false")
	}
}

// TestSingletonReachableStaleSocketFile: a leftover REGULAR FILE at the socket path
// — what a SIGKILLed daemon leaves behind — is unreachable.
//
// Existence is not liveness, and this is the case that makes the difference
// load-bearing: BrokerIsAlive checks PathExists AND this probe, so a stale file with
// a recycled-but-live PID beside it would otherwise read as a healthy broker and no
// jail would ever get one.
func TestSingletonReachableStaleSocketFile(t *testing.T) {
	stale := filepath.Join(shortSocketDir(t), "b.sock")
	if err := os.WriteFile(stale, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if SingletonReachable(stale, 200*time.Millisecond) {
		t.Error("a stale regular file at the socket path read as reachable")
	}
}
