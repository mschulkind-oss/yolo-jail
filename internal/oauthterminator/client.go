// Package oauthterminator is the Go port of src/oauth_broker_jail.py — the
// in-jail TLS terminator for Claude OAuth. Claude Code inside the jail opens
// TLS to platform.claude.com (--add-host routes it to 127.0.0.1); this daemon
// terminates it with a jail-trusted leaf cert and forwards to the host broker
// over the loophole Unix socket.
//
// Frozen contracts (go-port plan Stage 11): the ask_host_broker frame-protocol
// client + its TWO-LAYER 502 attribution (relay-layer connect failure vs
// broker-layer EOF-before-exit-frame / EPIPE-mid-request), the refresh-grant
// detection, and the proxy/refresh dispatch. HTTP hazards (header
// canonicalization, HTTP/1.0 no-keep-alive) are handled in the cmd's server.
//
// Source of truth: src/oauth_broker_jail.py.
package oauthterminator

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Loophole frame protocol stream IDs (client side; == frameproto v1's 0/1/2).
const (
	streamStdout = 0
	streamStderr = 1
	streamExit   = 2
)

// AskHostBroker sends a request to the host-side broker over the per-jail relay
// socket and returns the parsed JSON response. Mirrors ask_host_broker,
// including the two-layer error attribution:
//   - connect failure (ENOENT/refused) -> "relay unreachable" (relay layer)
//   - EOF before an exit frame, or EPIPE/ECONNRESET mid-request -> "host broker
//     unreachable through the relay" (broker layer)
//
// The distinction is load-bearing: the jail log must say WHICH layer failed.
func AskHostBroker(socketPath string, request *jsonx.OrderedMap) (*jsonx.OrderedMap, error) {
	conn, err := net.DialTimeout("unix", socketPath, 30*time.Second)
	if err != nil {
		// Relay layer: the socket is missing (never started / dir recreated)
		// or present-but-refused (no relay behind it). Distinct from the
		// broker being down behind a live relay (handled below).
		return nil, errors.New("relay unreachable — " + err.Error())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	body, err := jsonx.DumpsCompact(request)
	if err != nil {
		return nil, err
	}
	// Frame the request: 4-byte BE length + body.
	if err := writeFramed(conn, []byte(body)); err != nil {
		return nil, brokerMidRequestErr(err)
	}

	var stdout []byte
	rc := -1
	haveRC := false
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			// EOF/short read before an exit frame -> broker layer (below).
			break
		}
		streamID := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(conn, payload); err != nil {
				break
			}
		}
		switch streamID {
		case streamStdout:
			stdout = append(stdout, payload...)
		case streamStderr:
			// host broker stderr — logged by the caller; ignored here.
		case streamExit:
			rc = int(int32(binary.BigEndian.Uint32(payload)))
			haveRC = true
		}
		if haveRC {
			break
		}
	}

	if !haveRC {
		// The relay accepted the connection but closed it before an exit
		// frame — its per-connection dial of the real broker failed. Broker
		// layer, not relay layer.
		return nil, errors.New("host broker unreachable through the relay " +
			"(connection closed without an exit frame)")
	}
	if rc != 0 {
		return nil, errors.New("host broker exited " + itoa(rc))
	}
	decoded, err := jsonx.Decode(stdout)
	if err != nil {
		return nil, errors.New("host broker returned non-JSON: " + err.Error())
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, errors.New("host broker returned non-object JSON")
	}
	return m, nil
}

func writeFramed(conn net.Conn, body []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := conn.Write(body)
	return err
}

// brokerMidRequestErr maps a send-phase EPIPE/ECONNRESET/ENOTCONN to the
// broker-layer message (the relay accepted, failed its dial, and tore the
// connection down mid-write). Mirrors the OSError errno branch.
func brokerMidRequestErr(err error) error {
	if isConnReset(err) {
		return errors.New("host broker unreachable through the relay " +
			"(connection reset mid-request: " + err.Error() + ")")
	}
	return errors.New("host broker socket: " + err.Error())
}

func isConnReset(err error) bool {
	// EPIPE (Linux send-after-peer-close), ECONNRESET, ENOTCONN (macOS/BSD) —
	// the errno set the Python handler maps to the broker layer.
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ENOTCONN)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
