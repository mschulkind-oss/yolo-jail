//go:build linux

package listeners

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// THE REALITY CHECK. Every other test in this package feeds the parser bytes a human
// typed, which cannot catch a parser that is endian-wrong in the same direction the
// fixture is. This one makes the kernel produce the bytes: it binds a socket, then
// demands that [Collect] find THAT socket, on THAT port, attributed to THIS process.
//
// It fails if the byte order is wrong, if the LISTEN state filter is wrong, if the
// inode column is wrong, or if the fd walk cannot join an inode to a pid — the four
// ways this package could be plausible and useless. It starts no agent, makes no API
// call and shells out to nothing.
func TestCollectFindsASocketThisTestIsHolding(t *testing.T) {
	if !Supported() {
		t.Fatal("Supported() is false on linux")
	}
	cases := []struct {
		name     string
		network  string
		address  string
		wantKind Kind
		wantAddr string
	}{
		{"ipv4 loopback", "tcp", "127.0.0.1:0", KindTCP, "127.0.0.1"},
		{"ipv6 loopback", "tcp", "[::1]:0", KindTCP6, "::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ln, err := net.Listen(c.network, c.address)
			if err != nil {
				// A jail without IPv6 is a legitimate configuration; it is not
				// this package being wrong.
				t.Skipf("cannot listen on %s: %v", c.address, err)
			}
			defer ln.Close()
			port := ln.Addr().(*net.TCPAddr).Port

			snap := Collect()
			if snap.Availability() == Unavailable {
				t.Fatalf("Collect() could not read /proc at all:\n%s", Render(snap))
			}
			var found Listener
			var ok bool
			for _, l := range snap.OnPort(port) {
				if l.Kind == c.wantKind {
					found, ok = l, true
				}
			}
			if !ok {
				t.Fatalf("the socket this test holds on %s (port %d, kind %s) is not in the snapshot:\n%s",
					ln.Addr(), port, c.wantKind, Render(snap))
			}
			if got := found.Addr.String(); got != c.wantAddr {
				t.Errorf("address = %q, want %q — the kernel's hex is little-endian per 4-byte group", got, c.wantAddr)
			}
			if found.Inode == 0 {
				t.Error("inode = 0; the fd walk has nothing to join on")
			}
			if !holdsPID(found.Owners, os.Getpid()) {
				t.Errorf("owners = %+v, want this test's own pid %d:\n%s", found.Owners, os.Getpid(), Render(snap))
			}
		})
	}
}

func TestCollectFindsAUnixSocketThisTestIsHolding(t *testing.T) {
	// A short path: a UNIX socket address is capped near 108 bytes, and Go's
	// t.TempDir() under a long TMPDIR can exceed it.
	dir, err := os.MkdirTemp("", "lsn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	snap := Collect()
	got := snap.OnPath(path)
	if len(got) != 1 {
		t.Fatalf("OnPath(%q) = %+v, want the socket this test holds:\n%s", path, got, Render(snap))
	}
	if !holdsPID(got[0].Owners, os.Getpid()) {
		t.Errorf("owners = %+v, want this test's own pid %d", got[0].Owners, os.Getpid())
	}
	if got[0].UID != -1 {
		t.Errorf("unix UID = %d, want -1 (the table has no uid column)", got[0].UID)
	}
}

// A CONNECTED socket is not a listening one, and the live kernel is the only place
// this is worth asserting: a fixture cannot get the state column wrong by accident.
func TestCollectDoesNotReportAConnectedSocketAsListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Skipf("cannot dial: %v", err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Skipf("cannot accept: %v", err)
	}
	defer server.Close()

	clientPort := client.LocalAddr().(*net.TCPAddr).Port
	snap := Collect()
	if got := snap.OnPort(clientPort); len(got) != 0 {
		t.Errorf("the client's ephemeral port %d is reported as listening: %+v", clientPort, got)
	}
	if got := snap.OnPort(ln.Addr().(*net.TCPAddr).Port); len(got) == 0 {
		t.Error("the listener disappeared while a connection was open")
	}
}

// The cost bound, measured rather than asserted in prose: on the real /proc of a
// jail the walk has to stay far under its budget, or the instrument gets deleted for
// being slow.
func TestCollectStaysWellInsideItsBudget(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()

	snap := Collect()
	if snap.FDLinksRead >= DefaultMaxFDLinks {
		t.Errorf("FDLinksRead = %d hit the budget of %d on an ordinary machine",
			snap.FDLinksRead, DefaultMaxFDLinks)
	}
	if snap.FDLinksRead == 0 {
		t.Error("FDLinksRead = 0 with a socket listening: the walk did not run")
	}
	t.Logf("%d sockets, %d/%d processes read, %d descriptors examined, capped=%v",
		len(snap.Sockets), snap.PIDsScanned, snap.PIDsFound, snap.FDLinksRead, snap.Capped)
}
