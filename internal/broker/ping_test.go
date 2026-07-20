package broker

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// serveFakeBroker binds a Unix socket and, for one connection, reads the
// length-prefixed request and replies with the frames the reply func produces.
// Returns the socket path. It mirrors the daemon side of the frame protocol so
// BrokerPing is exercised against a real socket, not a stub.
func serveFakeBroker(t *testing.T, reply func(conn net.Conn, reqBody []byte)) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "b.sock")
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
