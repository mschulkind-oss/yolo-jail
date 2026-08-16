// Package brokerrelay is the per-jail Claude OAuth broker relay — a raw byte
// proxy with one protocol-aware trick — ported from src/broker_relay.py.
//
// One relay runs per jail, spawned by loopholes_runtime._relay_ensure. It
// listens on a HOST-ONLY Unix socket and dials the real broker socket PER
// CONNECTION, so a restarted broker (new socket inode) is picked up on the very
// next request (the "one jail 502s after `yolo broker restart`" bug the
// per-connection dial fixes).
//
// # How the jail reaches it
//
// NOT through that socket. The jail's hop is loopback-TLS (internal/svcendpoint):
// a front terminates a pinned-certificate, token-authenticated connection and
// SPLICES it into the Unix socket below. Two consequences worth stating:
//
//   - the Unix socket is host-only on purpose. It lives beside this relay's pid
//     and lock files in /tmp, NOT in the jail's :rw-mounted host-services dir —
//     leaving it there would keep the retired transport reachable from inside the
//     jail, and would let the jail unlink the relay's own socket. That directory
//     now holds endpoint files and nothing else;
//   - the relay core below is UNTOUCHED by the transport. It stays typed on
//     *net.UnixConn, which is why the front splices instead of swapping the
//     listener: pipe()'s teardown is frozen Python parity and a tls.Conn has
//     CloseWrite but no CloseRead.
//
// The one protocol-aware trick: the loophole protocol is exactly one 4-byte-BE
// length-prefixed UTF-8 JSON request per connection, client-first. The relay
// reads that first message, stamps request["jail_id"] with the jail's
// container name (host-side injection — trustworthy, unlike an in-jail
// self-report), re-frames it, then degrades to a dumb bidirectional pipe.
// Attribution is best-effort: an unparseable / oversized / slow first message
// is forwarded verbatim and the connection keeps working.
//
// Failure semantics the jail-side terminator relies on: the endpoint file
// missing, or the address in it refusing the dial, = relay layer; the relay
// accepts but ends the connection with zero response frames = broker layer. On
// dial failure the relay drains the
// client's pending request before closing so the client sees a CLEAN EOF —
// closing with unread bytes queued surfaces as ECONNRESET (Linux AF_UNIX
// discards the rx queue), which the terminator cannot attribute to a layer.
//
// SIGTERM unlinks the relay socket ONLY if its dev/ino still match what we
// bound (so a successor that healed over the same path is never disturbed),
// then exits — a stopped relay reads as "socket absent", not "socket dead".
package brokerrelay

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// First-message bounds for the attribution read. Requests are small JSON
// objects; the cap stops a garbage length prefix from buffering gigabytes, the
// timeout stops a silent client from parking the stamp path forever. Blowing
// either bound downgrades to verbatim forwarding, never failure.
const (
	firstMsgMax     = 4 * 1024 * 1024
	firstMsgTimeout = 5 * time.Second
)

// Logger is where the relay writes its (payload-free) diagnostics. Payloads
// carry OAuth tokens and are NEVER logged.
var Logger = log.New(os.Stderr, "", log.LstdFlags)

// pipe copies src->dst until EOF or error, then shuts down and closes BOTH
// sockets so fds never outlive the connection (shutdown alone doesn't release
// the fd). The sibling goroutine's double-close is swallowed.
func pipe(src, dst *net.UnixConn, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}
	buf := make([]byte, 65536)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	// Match Python's finally: shut down src read side + dst write side, then
	// close both. Errors are ignored (the sibling may have closed already).
	_ = src.CloseRead()
	_ = dst.CloseWrite()
	_ = src.Close()
	_ = dst.Close()
}

// readFirstMessage tries to read the connection's single framed request.
// Returns (body, raw): raw is EVERY byte consumed so far; body is the frame
// payload iff a complete frame arrived within firstMsgTimeout / firstMsgMax,
// else nil and the caller forwards raw verbatim. Faithful to Python's
// _read_first_message, which accumulates raw byte-by-byte via recv, so on EOF
// or timeout at any offset raw holds exactly what was received (io.ReadFull's
// n-on-error is captured here for that reason).
func readFirstMessage(client *net.UnixConn) (body, raw []byte) {
	_ = client.SetReadDeadline(time.Now().Add(firstMsgTimeout))
	defer client.SetReadDeadline(time.Time{})

	header := make([]byte, 4)
	n, err := io.ReadFull(client, header)
	raw = append(raw, header[:n]...)
	if err != nil {
		return nil, raw // short/clean-EOF header — forward what we got
	}
	length := binary.BigEndian.Uint32(header)
	if length > firstMsgMax {
		return nil, raw // oversized length prefix — forward the header verbatim
	}
	payload := make([]byte, length)
	n, err = io.ReadFull(client, payload)
	raw = append(raw, payload[:n]...)
	if err != nil {
		return nil, raw // partial body (timeout/EOF mid-message)
	}
	return payload, raw
}

// stampJailID re-frames body with jailID stamped, or returns (nil,false) if it
// isn't a JSON object (caller then forwards the original bytes verbatim). The
// stamp OVERRIDES any client-supplied jail_id — attribution must come from the
// host side. Re-serialization uses jsonx (Python json.dumps default separators
// + insertion order) so the re-framed request matches what Python would emit.
func stampJailID(body []byte, jailID string) ([]byte, bool) {
	decoded, err := jsonx.Decode(body)
	if err != nil {
		return nil, false
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	m.Set("jail_id", jailID) // insert-or-update, preserving position
	newBody, err := jsonx.DumpsCompact(m)
	if err != nil {
		return nil, false
	}
	nb := []byte(newBody)
	framed := make([]byte, 4+len(nb))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(nb)))
	copy(framed[4:], nb)
	return framed, true
}

// handle serves one client connection: dial the broker, stamp, pipe.
func handle(client *net.UnixConn, brokerPath, jailID string) {
	upstream, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: brokerPath, Net: "unix"})
	if err != nil {
		// Broker layer: clean EOF + zero frames. Shut down our write side
		// (client's recv sees EOF), drain what it already sent, then close —
		// closing with the request unread would DISCARD it and raise
		// ECONNRESET at the peer instead of EOF.
		Logger.Printf("dial %s failed: %v", brokerPath, err)
		_ = client.CloseWrite()
		drainBounded(client, firstMsgTimeout)
		_ = client.Close()
		return
	}

	body, raw := readFirstMessage(client)
	var framed []byte
	if body != nil {
		if stamped, ok := stampJailID(body, jailID); ok {
			framed = stamped
		}
	}
	if framed == nil {
		if len(raw) > 0 {
			// Payloads may carry tokens — log the length, never the bytes.
			Logger.Printf("first message not a framed JSON object (%d bytes) — forwarding unstamped", len(raw))
		}
		framed = raw
	}

	if len(framed) > 0 {
		if _, err := upstream.Write(framed); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go pipe(client, upstream, &wg)
	pipe(upstream, client, nil)
	wg.Wait()
}

// drainBounded reads and discards from conn until EOF or the deadline — a
// wedged client can't park the goroutine.
func drainBounded(conn *net.UnixConn, budget time.Duration) {
	deadline := time.Now().Add(budget)
	buf := make([]byte, 65536)
	for {
		now := time.Now()
		if !now.Before(deadline) {
			return
		}
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		if n == 0 || err != nil {
			return
		}
	}
}

// Config is one relay's parameters. EndpointPath and AdvertiseHost are the ONLY
// additions the loopback-TLS migration makes to this package; everything else is
// the frozen relay core.
type Config struct {
	// SocketPath is the relay's own Unix socket. HOST-ONLY — see the package doc.
	SocketPath string
	// BrokerPath is the host-wide broker singleton, dialed per connection.
	BrokerPath string
	// JailID is stamped host-side onto every request.
	JailID string
	// EndpointPath, when non-empty, is where the loopback-TLS front publishes its
	// address, its certificate and this jail's bearer token.
	//
	// THAT FILE IS A CREDENTIAL: 0600, in the per-jail host-services dir, never
	// shared. Empty means no front at all — the relay then serves the Unix socket
	// only, which is what this package's own tests use and what a host-side caller
	// on the same machine can still reach.
	EndpointPath string
	// AdvertiseHost overrides the host name the front publishes for the jail to
	// dial. Empty resolves through svcendpoint.AdvertiseHost(), i.e. the
	// YOLO_SVC_ADVERTISE_HOST this process was spawned with, else the container
	// runtime's gateway name.
	AdvertiseHost string
}

// Serve runs the accept loop until stop is closed; one goroutine per client.
// dev/ino, and on shutdown unlink the socket ONLY if it's still the file we
// bound. Returns nil on clean shutdown.
func Serve(cfg Config, stop <-chan struct{}) error {
	// Destructured up front so the relay core below is TEXTUALLY unchanged by the
	// transport work — a diff that touches pipe()'s teardown or the stamp path is
	// then visibly not this change.
	socketPath, brokerPath, jailID := cfg.SocketPath, cfg.BrokerPath, cfg.JailID
	if err := os.MkdirAll(dir(socketPath), 0o755); err != nil {
		return err
	}
	// A stale file at our path (crashed predecessor) would EADDRINUSE.
	_ = os.Remove(socketPath)

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return err
	}
	// The jail's half of the hop, started only once the socket it splices into
	// exists. FIRE AND FORGET, deliberately: a front that cannot bind or cannot
	// publish logs and dies while Serve keeps serving the Unix socket. Failing
	// Serve instead would take down a relay a same-host caller can still use, and
	// would report a publication problem as "the relay is dead" — a worse diagnosis
	// of the same fact. The host-side health gate (relayHealthy) sees the absent
	// endpoint and respawns, and `yolo check` names it.
	//
	// NoPreamble: THE ONE DELIBERATE OPT-OUT IN THE TREE, and it is on the
	// credential path. The relay is not a dumb pipe — handle -> readFirstMessage
	// reads the FIRST length-prefixed frame off this connection and stampJailID
	// rewrites it. A connection preamble (svcendpoint/preamble.go) is framed
	// identically, so with one in front the relay would stamp THE PREAMBLE, write
	// that to the broker, and leave the terminator's real request sitting unread
	// behind it: every jail's Claude OAuth refresh fails. The design doc called
	// this change "additive"; it is not, and this line is the whole correction
	// (docs/design/broker-as-a-pack.md §12, retraction).
	//
	// REMOVAL TRIGGER: the broker conversion, which deletes readFirstMessage and
	// stampJailID outright — the preamble is what replaces them. When this
	// function goes, so does the opt-out; until then nothing else may set it.
	if cfg.EndpointPath != "" {
		go func() {
			if err := svcendpoint.ServeFrontWithOptions(cfg.EndpointPath, cfg.AdvertiseHost, socketPath, stop,
				svcendpoint.FrontOptions{NoPreamble: true}); err != nil {
				Logger.Printf("front: %s not published (%v) — this jail cannot reach the relay",
					cfg.EndpointPath, err)
			}
		}()
	}
	// Go's UnixListener unlinks the socket on Close by default; disable that so
	// WE control unlink (only-if-ours), matching Python's dev/ino guard.
	ln.SetUnlinkOnClose(false)

	boundDev, boundIno, haveBound := statDevIno(socketPath)

	Logger.Printf("relaying %s -> %s (jail=%s)", socketPath, brokerPath, jailID)

	// Closing the listener breaks Accept out of its blocking call.
	go func() {
		<-stop
		_ = ln.Close()
	}()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			// stop closed => listener closed => break cleanly.
			break
		}
		go handle(conn, brokerPath, jailID)
	}

	// Cleanup: unlink the socket file only if it's still the one we bound.
	if haveBound {
		if dev, ino, ok := statDevIno(socketPath); ok && dev == boundDev && ino == boundIno {
			_ = os.Remove(socketPath)
		}
	}
	return nil
}

// statDevIno returns the (dev, ino) of path, or ok=false on error.
func statDevIno(path string) (dev uint64, ino uint64, ok bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, false
	}
	return uint64(st.Dev), st.Ino, true
}

func dir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
