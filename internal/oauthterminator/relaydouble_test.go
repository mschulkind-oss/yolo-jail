package oauthterminator

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The doubles in this file speak the REAL transport: a loopback-TLS listener that
// publishes an endpoint file, exactly as the per-jail relay's front does. Nothing
// here fakes the pin or the token — a test that stubbed those would stop proving
// that the terminator can reach a relay at all, which is the one thing it must.

// privateDir returns a 0700 directory. t.TempDir() is 0755 (os.MkdirTemp uses 0777
// under the umask), and svcendpoint REFUSES to publish a credential into a
// group/world-accessible directory — so a test that skipped this would fail with a
// publication error rather than the thing it meant to assert.
func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// relayDouble is a fake per-jail relay behind a real authenticated listener.
type relayDouble struct {
	endpointPath string
	ln           *svcendpoint.Listener
}

// startRelayDouble publishes an endpoint and serves each AUTHENTICATED connection
// with handle. Accept returns only conns that already presented the right token, so
// a handler here sees the frame protocol and nothing else — which is the property
// the daemon side is supposed to have.
func startRelayDouble(t *testing.T, handle func(net.Conn)) *relayDouble {
	t.Helper()
	endpointPath := filepath.Join(privateDir(t), "claude-oauth-broker.endpoint")
	ln, err := svcendpoint.Listen(endpointPath, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				handle(c)
			}(conn)
		}
	}()
	return &relayDouble{endpointPath: endpointPath, ln: ln}
}

// readFramedRequest reads one 4-byte-BE length-prefixed request body.
func readFramedRequest(c net.Conn) ([]byte, error) {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr))
	if _, err := io.ReadFull(c, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeFramedResponse writes one stdout frame carrying body, then exit 0.
func writeFramedResponse(c net.Conn, body []byte) {
	fh := make([]byte, 5)
	fh[0] = streamStdout
	binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
	_, _ = c.Write(fh)
	_, _ = c.Write(body)
	ex := make([]byte, 5)
	ex[0] = streamExit
	binary.BigEndian.PutUint32(ex[1:], 4)
	_, _ = c.Write(ex)
	_, _ = c.Write([]byte{0, 0, 0, 0})
}

// respondOnce is the common double: read the request, reply with respFn()'s object.
func respondOnce(respFn func() *jsonx.OrderedMap) func(net.Conn) {
	return func(c net.Conn) {
		if _, err := readFramedRequest(c); err != nil {
			return
		}
		body, _ := json.Marshal(mapOf(respFn()))
		writeFramedResponse(c, body)
	}
}

// respondPlain is respondOnce for a plain map (the serve_test doubles).
func respondPlain(resp map[string]any) func(net.Conn) {
	return func(c net.Conn) {
		if _, err := readFramedRequest(c); err != nil {
			return
		}
		body, _ := json.Marshal(resp)
		writeFramedResponse(c, body)
	}
}

// hex64 matches a bearer token's shape (64 lowercase hex). Nothing token-shaped may
// reach a daemon's protocol stream, a log, or an error message.
var hex64 = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
