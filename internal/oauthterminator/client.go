// Package oauthterminator is the in-jail TLS terminator for Claude OAuth.
// Claude Code inside the jail opens
// TLS to platform.claude.com (--add-host routes it to 127.0.0.1); this daemon
// terminates it with a jail-trusted leaf cert and forwards to the host broker
// over the per-jail relay's loopback-TLS endpoint (internal/svcendpoint): the
// address, the certificate to pin and this jail's bearer token all come from the
// 0600 endpoint file named by BrokerEndpointEnv, re-read fresh on every dial.
//
// Only that hop changed. This daemon still terminates Claude Code's TLS on
// 127.0.0.1:443 with server.crt/server.key, and the host broker singleton behind
// the relay is untouched.
//
// Frozen contracts: the ask_host_broker frame-protocol
// client + its TWO-LAYER 502 attribution (relay-layer connect failure vs
// broker-layer EOF-before-exit-frame / EPIPE-mid-request), the refresh-grant
// detection, and the proxy/refresh dispatch. HTTP hazards (header
// canonicalization, HTTP/1.0 no-keep-alive) are handled in the cmd's server.
package oauthterminator

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// BrokerEndpointEnv names this jail's broker ENDPOINT FILE.
//
// Composed from internal/paths rather than spelled out, because the producer (the
// run pipeline's hostServiceEnvVar) and this consumer drifting apart is exactly
// what once silently disabled the cgroup delegate in every jail. There is no
// token variable beside it and never will be: an environment variable is
// inherited by every child this daemon spawns, and the token lives in the file.
const BrokerEndpointEnv = paths.ServiceEnvVarPrefix + "CLAUDE_OAUTH_BROKER" + paths.ServiceEnvVarSuffix

// UpstreamHost is the intercepted host, named in the startup log line
// .
const UpstreamHost = "platform.claude.com"

// Loophole frame protocol stream IDs (client side; == frameproto v1's 0/1/2).
const (
	streamStdout = 0
	streamStderr = 1
	streamExit   = 2
)

// AskHostBroker sends a request to the host-side broker over the per-jail relay's
// loopback-TLS endpoint and returns the parsed JSON response.
// including the error attribution:
// - AUTH REJECTED -> "relay auth rejected" (its own layer, see below)
// - connect failure (ENOENT/refused) -> "relay unreachable" (relay layer)
// - EOF before an exit frame, or EPIPE/ECONNRESET mid-request -> "host broker
// unreachable through the relay" (broker layer)
// The distinction is load-bearing: the jail log must say WHICH layer failed.
func AskHostBroker(endpointPath string, request *jsonx.OrderedMap) (*jsonx.OrderedMap, error) {
	conn, err := svcendpoint.Dial(endpointPath, 30*time.Second)
	if err != nil {
		// AUTH IS ITS OWN LAYER, and it goes FIRST. A token mismatch is a
		// post-accept drop, so without this branch it would arrive below as an
		// ordinary connection failure — or, worse, reach the read loop as
		// EOF-before-exit-frame and be reported as the BROKER layer. That would give
		// the single most likely misconfiguration the single most misleading message
		// in the system. svcendpoint's one-byte accept ack is what makes the
		// distinction possible at all: the ack arrives before any request is sent,
		// so an EOF at that point can only mean the token was refused.
		if errors.Is(err, svcendpoint.ErrAuthRejected) {
			return nil, errors.New("relay auth rejected — this jail's endpoint file token does " +
				"not match the relay (" + endpointPath + ")")
		}
		// Relay layer ONLY for ENOENT (endpoint file missing) / ECONNREFUSED (no
		// listener at the published address) — the SAME errno gate as before, and
		// svcendpoint preserves both errnos through its wrapping precisely so it
		// keeps working. Any OTHER failure (a malformed endpoint file, EACCES, a
		// timeout, a pin mismatch) takes the generic form below rather than being
		// mis-attributed to the relay layer; its own message names the real fault.
		if isRelayLayerDialErr(err) {
			return nil, errors.New("relay unreachable — the host-side relay for this jail " +
				"is down (" + endpointPath + ": " + err.Error() + ")")
		}
		return nil, errors.New("host broker endpoint " + endpointPath + ": " + err.Error())
	}
	defer conn.Close()
	// The terminator's own 30s session deadline, unlike yolo-ps's: this is a
	// request/response exchange with a bounded broker, not an open-ended stream.
	// svcendpoint deliberately sets none, leaving the choice here.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	body, err := jsonx.DumpsCompact(request)
	if err != nil {
		return nil, err
	}
	// Frame the request: 4-byte BE length + body.
	if err := writeFramed(conn, []byte(body)); err != nil {
		return nil, brokerMidRequestErr(endpointPath, err)
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
				return nil, brokerMidRequestErr(endpointPath, rerr)
			}
			break
		}
		streamID := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		payload := make([]byte, length)
		if length > 0 {
			if _, rerr := io.ReadFull(conn, payload); rerr != nil {
				if isConnReset(rerr) {
					return nil, brokerMidRequestErr(endpointPath, rerr)
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
//
// The generic branch names the ENDPOINT path. It used to say "host broker socket
// {path}" for parity with the retired Python — but the value is an endpoint file
// now, and a message reading "host broker socket …/claude-oauth-broker.endpoint"
// sends the next reader looking for a socket that does not exist.
func brokerMidRequestErr(endpointPath string, err error) error {
	if isConnReset(err) {
		return errors.New("host broker unreachable through the relay " +
			"(connection reset mid-request: " + err.Error() + ")")
	}
	return errors.New("host broker endpoint " + endpointPath + ": " + err.Error())
}

// isRelayLayerDialErr reports whether a connect error is the relay layer: ENOENT
// (the endpoint file is not published) or ECONNREFUSED (nothing listening at the
// address it names). Matches Python's `if e.errno in (errno.ENOENT,
// errno.ECONNREFUSED)`, and svcendpoint keeps both errnos reachable through
// errors.Is for exactly that reason.
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
