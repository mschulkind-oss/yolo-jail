package broker

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// serveFakeBroker binds a Unix socket and, for one connection, reads the
// length-prefixed request and replies with the frames the reply func produces.
// Returns the socket path. It mirrors the daemon side of the frame protocol so
// BrokerPing is exercised against a real socket, not a stub.
func serveFakeBroker(t *testing.T, reply func(conn net.Conn, reqBody []byte)) string {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "b.sock")
	assertSockPathFits(t, sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		body, err := frameproto.ReadRequestBytes(conn)
		if err != nil {
			return
		}
		reply(conn, body)
	}()
	return sock
}

func TestBrokerPingPongTrue(t *testing.T) {
	sock := serveFakeBroker(t, func(conn net.Conn, reqBody []byte) {
		// The request must be exactly {"action":"ping"}.
		if string(reqBody) != `{"action":"ping"}` {
			t.Errorf("request body = %q", string(reqBody))
		}
		out := jsonx.NewOrderedMap()
		out.Set("pong", true)
		out.Set("pid", jsonx.IntValue(4242))
		payload, _ := jsonx.DumpsCompact(out)
		_, _ = frameproto.WriteFrame(conn, frameproto.StreamStdout, []byte(payload))
		_, _ = frameproto.WriteExit(conn, 0)
	})
	if !BrokerPing(sock, PingTimeout) {
		t.Error("pong:true should ping OK")
	}
}

func TestBrokerPingPongFalse(t *testing.T) {
	sock := serveFakeBroker(t, func(conn net.Conn, _ []byte) {
		out := jsonx.NewOrderedMap()
		out.Set("pong", false)
		payload, _ := jsonx.DumpsCompact(out)
		_, _ = frameproto.WriteFrame(conn, frameproto.StreamStdout, []byte(payload))
		_, _ = frameproto.WriteExit(conn, 0)
	})
	if BrokerPing(sock, PingTimeout) {
		t.Error("pong:false should not ping OK")
	}
}

func TestBrokerPingExitBeforePong(t *testing.T) {
	sock := serveFakeBroker(t, func(conn net.Conn, _ []byte) {
		// Exit frame with no stdout frame first → not alive.
		_, _ = frameproto.WriteExit(conn, 0)
	})
	if BrokerPing(sock, PingTimeout) {
		t.Error("exit-before-pong should be false")
	}
}

func TestBrokerPingNoSocket(t *testing.T) {
	if BrokerPing(filepath.Join(t.TempDir(), "nope.sock"), 200*time.Millisecond) {
		t.Error("dialing a nonexistent socket should be false")
	}
}
