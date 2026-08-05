// Package oauthterminator is the in-jail TLS terminator for Claude OAuth.
// Claude Code inside the jail opens
// TLS to platform.claude.com (--add-host routes it to 127.0.0.1); this daemon
// terminates it with a jail-trusted leaf cert and forwards to the host broker
// over the loophole Unix socket.
// Frozen contracts: the ask_host_broker frame-protocol
// client + its TWO-LAYER 502 attribution (relay-layer connect failure vs
// broker-layer EOF-before-exit-frame / EPIPE-mid-request), the refresh-grant
// detection, and the proxy/refresh dispatch. HTTP hazards (header
// canonicalization, HTTP/1.0 no-keep-alive) are handled in the cmd's server.
package oauthterminator

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// UpstreamHost is the intercepted host, named in the startup log line
// .
const UpstreamHost = "platform.claude.com"

// Loophole frame protocol stream IDs (client side; == frameproto v1's 0/1/2).
const (
	streamStdout = 0
	streamStderr = 1
	streamExit   = 2
)

// dialBroker connects to the host broker. On Linux the address is a unix socket
// path. On macOS the mounted unix socket is unconnectable across the podman-VM
// boundary, so the address is "tcpfile:<path>": read the relay's published
// host:port + pinned cert from that file (in the mounted host-services dir)
// fresh on every dial — so a relay that restarted on a new port/cert is picked
// up transparently — then TLS-dial (pinning that exact cert) and authenticate
// with the per-jail token as a leading 4-byte-length-framed message inside TLS,
// before the normal frame protocol begins.
func dialBroker(address string) (net.Conn, error) {
	if strings.HasPrefix(address, paths.BrokerTCPFileSentinel) {
		hostport, certB64, err := readBrokerEndpoint(strings.TrimPrefix(address, paths.BrokerTCPFileSentinel))
		if err != nil {
			return nil, err
		}
		return dialTCPBroker(hostport, certB64)
	}
	return net.DialTimeout("unix", address, 30*time.Second)
}

// dialTCPBroker TLS-dials the relay's loopback TCP front, trusting ONLY the
// published cert (a dedicated root pool — no PKI, no broker CA, whose key is
// mounted into every jail and so can't be trusted here), then writes the per-jail
// token frame (4-byte BE length + token) inside TLS. Encryption defeats a
// sibling jail sniffing the shared bridge; pinning the relay's host-only-key cert
// defeats impersonation/MITM. An empty token is rejected up front
// (misconfiguration) rather than sent as a zero-length frame the relay would
// silently drop as a confusing broker-layer EOF.
func dialTCPBroker(hostport, certB64 string) (net.Conn, error) {
	token := os.Getenv(paths.BrokerTokenEnv)
	if token == "" {
		return nil, errors.New("no broker token in env " + paths.BrokerTokenEnv + " — relay auth is misconfigured")
	}
	der, err := base64.StdEncoding.DecodeString(certB64)
	if err != nil {
		return nil, errors.New("bad broker cert in endpoint file: " + err.Error())
	}
	relayCert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errors.New("bad broker cert in endpoint file: " + err.Error())
	}
	pool := x509.NewCertPool()
	pool.AddCert(relayCert)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", hostport, &tls.Config{
		RootCAs:    pool,
		ServerName: paths.BrokerTLSServerName,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(token)))
	if _, werr := conn.Write(append(hdr, []byte(token)...)); werr != nil {
		_ = conn.Close()
		return nil, werr
	}
	return conn, nil
}

// readBrokerEndpoint reads the relay's published "host:port <base64 cert>" from
// path (the <name>.tcp file in the mounted host-services dir), fresh on every
// dial so a relay that restarted on a new port/cert is picked up without a jail
// relaunch. A missing file (relay not up yet) surfaces as ENOENT, which
// AskHostBroker attributes to the relay layer — the correct "relay down" reading.
func readBrokerEndpoint(path string) (hostport, certB64 string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return "", "", errors.New("malformed broker endpoint file " + path)
	}
	return fields[0], fields[1], nil
}

// AskHostBroker sends a request to the host-side broker over the per-jail relay
// socket and returns the parsed JSON response.
// including the two-layer error attribution:
// - connect failure (ENOENT/refused) -> "relay unreachable" (relay layer)
// - EOF before an exit frame, or EPIPE/ECONNRESET mid-request -> "host broker
// unreachable through the relay" (broker layer)
// The distinction is load-bearing: the jail log must say WHICH layer failed.
func AskHostBroker(socketPath string, request *jsonx.OrderedMap) (*jsonx.OrderedMap, error) {
	conn, err := dialBroker(socketPath)
	if err != nil {
		// Relay layer ONLY for ENOENT (socket missing) / ECONNREFUSED (no relay
		// behind the file) — matching Python's errno gate. Any OTHER connect
		// error (EACCES, timeout, ...) is the generic "host broker socket …"
		// form, NOT mis-attributed to the relay layer.
		if isRelayLayerDialErr(err) {
			return nil, errors.New("relay unreachable — the host-side relay for this jail " +
				"is down (" + socketPath + ": " + err.Error() + ")")
		}
		return nil, errors.New("host broker socket " + socketPath + ": " + err.Error())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	body, err := jsonx.DumpsCompact(request)
	if err != nil {
		return nil, err
	}
	// Frame the request: 4-byte BE length + body.
	if err := writeFramed(conn, []byte(body)); err != nil {
		return nil, brokerMidRequestErr(socketPath, err)
	}

	var stdout []byte
	rc := -1
	haveRC := false
	for {
		header := make([]byte, 5)
		if _, rerr := io.ReadFull(conn, header); rerr != nil {
			// A reset/EPIPE mid-read is the broker layer caught in the recv
			// phase (relay accepted, failed its dial, tore the conn down) — name
			// it that way. A clean EOF falls through to the "closed without an
			// exit frame" broker-layer message below.
			if isConnReset(rerr) {
				return nil, brokerMidRequestErr(socketPath, rerr)
			}
			break
		}
		streamID := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		payload := make([]byte, length)
		if length > 0 {
			if _, rerr := io.ReadFull(conn, payload); rerr != nil {
				if isConnReset(rerr) {
					return nil, brokerMidRequestErr(socketPath, rerr)
				}
				break
			}
		}
		switch streamID {
		case streamStdout:
			stdout = append(stdout, payload...)
		case streamStderr:
			// host broker stderr — surface it (Python's ask_host_broker logs
			// "host broker stderr: %s" at WARNING). Names a failing host broker.
			if s := strings.TrimSpace(string(payload)); s != "" {
				LogWarn("host broker stderr: %s", s)
			}
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

// brokerMidRequestErr maps a send/recv-phase EPIPE/ECONNRESET/ENOTCONN to the
// broker-layer message (the relay accepted, failed its dial, and tore the
// connection down mid-request).
// ask_host_broker; the generic branch includes the socket path like Python's
// "host broker socket {path}: {e}".
func brokerMidRequestErr(socketPath string, err error) error {
	if isConnReset(err) {
		return errors.New("host broker unreachable through the relay " +
			"(connection reset mid-request: " + err.Error() + ")")
	}
	return errors.New("host broker socket " + socketPath + ": " + err.Error())
}

// isRelayLayerDialErr reports whether a connect error is the relay layer:
// ENOENT (socket missing) or ECONNREFUSED (no relay behind the file). Matches
// Python's `if e.errno in (errno.ENOENT, errno.ECONNREFUSED)`.
func isRelayLayerDialErr(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
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
