// Package brokerrelay is the per-jail Claude OAuth broker relay — a raw byte
// proxy with one protocol-aware trick — ported from src/broker_relay.py.
//
// One relay runs per jail, spawned by loopholes_runtime._relay_ensure. It
// listens on claude-oauth-broker.sock inside the jail's host-services dir and
// dials the real broker socket PER CONNECTION, so a restarted broker (new
// socket inode) is picked up on the very next request (the "one jail 502s
// after `yolo broker restart`" bug the per-connection dial fixes).
//
// The one protocol-aware trick: the loophole protocol is exactly one 4-byte-BE
// length-prefixed UTF-8 JSON request per connection, client-first. The relay
// reads that first message, stamps request["jail_id"] with the jail's
// container name (host-side injection — trustworthy, unlike an in-jail
// self-report), re-frames it, then degrades to a dumb bidirectional pipe.
// Attribution is best-effort: an unparseable / oversized / slow first message
// is forwarded verbatim and the connection keeps working.
//
// Failure semantics the jail-side terminator relies on: relay socket
// missing/refused = relay layer; relay accepts but ends the connection with
// zero response frames = broker layer. On dial failure the relay drains the
// client's pending request before closing so the client sees a CLEAN EOF —
// closing with unread bytes queued surfaces as ECONNRESET (Linux AF_UNIX
// discards the rx queue), which the terminator cannot attribute to a layer.
//
// SIGTERM unlinks the relay socket ONLY if its dev/ino still match what we
// bound (so a successor that healed over the same path is never disturbed),
// then exits — a stopped relay reads as "socket absent", not "socket dead".
package brokerrelay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// TCPFront configures the optional macOS loopback TCP front (issue #31). A nil
// *TCPFront on Config leaves the relay unix-only, which is every Linux run.
type TCPFront struct {
	// PublishPath is the file the front writes "<host:port> <base64 cert>" to,
	// inside the jail's mounted host-services dir.
	PublishPath string
	// AdvertiseHost is the name the jail uses to reach the host (written into
	// PublishPath alongside the kernel-assigned port).
	AdvertiseHost string
	// Token is the per-jail bearer token the front requires on each connection.
	Token string
}

// Config is Serve's parameter set. It is a struct rather than a positional list
// because the fields are same-typed strings a caller could silently transpose.
type Config struct {
	SocketPath string
	BrokerPath string
	JailID     string
	TCP        *TCPFront
}

// Serve runs the accept loop until stop is closed; one goroutine per client.
// dev/ino, and on shutdown unlink the socket ONLY if it's still the file we
// bound. Returns nil on clean shutdown.
func Serve(cfg Config, stop <-chan struct{}) error {
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

	// macOS transport (issue #31): an in-jail terminator can't connect to this
	// unix socket across the podman-machine VM boundary (virtiofs shares the
	// inode, not the connection). When tcpPublish is set, also run a loopback
	// TCP front that serves TLS (host-only key, terminator-pinned), authenticates
	// a per-jail token, and splices to this same unix socket, leaving the relay
	// core below untouched. The front binds 127.0.0.1:0 (kernel-assigned) and
	// publishes its host:port + cert there for the terminator to read.
	if front := cfg.TCP; front != nil && front.PublishPath != "" {
		go func() {
			if err := serveTCPFront(*front, socketPath, stop); err != nil {
				Logger.Printf("tcp-front (publish %s) failed: %v", front.PublishPath, err)
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

// serveTCPFront runs the macOS loopback TCP front (issue #31). It binds
// 127.0.0.1:0 and lets the KERNEL assign the port, so the relay OWNS the
// listener from birth — nobody probes-then-closes a port for the relay to
// re-bind, so there is no window in which another local process could squat it.
//
// The front is wrapped in TLS with an ephemeral self-signed cert whose PRIVATE
// KEY stays host-only (in-memory, never persisted, never entering a jail). It
// publishes the advertised host:port AND that cert (public) to tcpPublish — a
// file in the jail's mounted host-services dir — which the terminator reads at
// connect time, pinning the exact cert. This is what actually protects the hop:
// the jail-to-gateway segment is a shared bridge on which sibling jails hold
// NET_RAW, and the broker CA can't help (its key is mounted into every jail), so
// TLS + a host-only-key pin gives confidentiality against sniffing and blocks
// impersonation/MITM. Each accepted connection then authenticates a per-jail
// bearer token and splices to the relay's own unix socket, leaving the unix
// relay core (jail_id stamping, per-connection broker dial, failure semantics)
// completely untouched.
func serveTCPFront(front TCPFront, unixSocketPath string, stop <-chan struct{}) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	cert, certB64, err := mintRelayCert()
	if err != nil {
		_ = ln.Close()
		return err
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	go func() {
		<-stop
		_ = tlsLn.Close()
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	hostport := net.JoinHostPort(front.AdvertiseHost, strconv.Itoa(port))
	// Published line: "<host:port> <base64(cert DER)>". The cert is public; the
	// terminator trusts ONLY it (dedicated root pool) — no PKI, no broker CA.
	if err := writeEndpointFile(front.PublishPath, hostport+" "+certB64); err != nil {
		_ = tlsLn.Close()
		return err
	}
	Logger.Printf("tcp-front listening 127.0.0.1:%d (advertised %s, tls cert-pinned) -> %s", port, hostport, unixSocketPath)
	for {
		conn, err := tlsLn.Accept()
		if err != nil {
			break // listener closed on stop
		}
		go handleTCPFront(conn, unixSocketPath, front.Token)
	}
	return nil
}

// mintRelayCert generates an ephemeral P-256 self-signed cert for the TCP front.
// The private key is HOST-ONLY — in-memory, never persisted, never mounted into
// a jail — so a malicious jail cannot impersonate the relay even though it can
// read the broker CA's key (mounted into every jail). It returns the tls
// certificate and the base64(DER) the terminator pins. A fresh cert per relay
// process is fine: the terminator re-reads the published cert on every dial.
func mintRelayCert() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: paths.BrokerTLSServerName},
		DNSNames:              []string{paths.BrokerTLSServerName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // self-signed leaf doubles as its own trust anchor
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, base64.StdEncoding.EncodeToString(der), nil
}

// writeEndpointFile atomically publishes the endpoint line ("host:port cert")
// to path (temp + rename, so a terminator never reads a torn line). Neither
// field is a secret — the per-jail token guards access and the cert is public —
// but the write stays 0600 to match the host-services dir's posture.
func writeEndpointFile(path, endpoint string) error {
	d := dir(path)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, "relay-tcp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(endpoint + "\n"); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// tokenFrameMax caps the leading token frame so a garbage length prefix can't
// buffer unbounded memory before the token check.
const tokenFrameMax = 4096

// handleTCPFront authenticates one TCP connection's leading token frame
// (4-byte BE length + token bytes, matching the loophole framing) and, on a
// constant-time match, splices it to the relay's unix socket. Any missing,
// oversized, or mismatched token drops the connection (payload-free log), so an
// unauthenticated caller gets no broker access and learns nothing.
func handleTCPFront(client net.Conn, unixSocketPath, token string) {
	defer client.Close()

	_ = client.SetReadDeadline(time.Now().Add(firstMsgTimeout))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(client, hdr); err != nil {
		return
	}
	n := binary.BigEndian.Uint32(hdr)
	if n == 0 || n > tokenFrameMax {
		return
	}
	got := make([]byte, n)
	if _, err := io.ReadFull(client, got); err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	if subtle.ConstantTimeCompare(got, []byte(token)) != 1 {
		Logger.Printf("tcp-front: token mismatch (%d bytes) — dropping connection", len(got))
		return
	}

	// Authenticated: hand off to the unix relay, which does the real work.
	up, err := net.Dial("unix", unixSocketPath)
	if err != nil {
		Logger.Printf("tcp-front: dial relay %s failed: %v", unixSocketPath, err)
		return
	}
	defer up.Close()
	// Splice both ways, but wait ONLY on the response direction: it ends when
	// the relay closes after the broker's exit frame, by which point io.Copy has
	// written every byte to the client. Returning on whichever direction ended
	// first (and closing both) would cut a response short the moment the request
	// direction ended — and the request goroutine deliberately does NOT
	// propagate the client's EOF upstream, because the relay core's pipe() tears
	// down BOTH sockets on either EOF (frozen Python-parity semantics), which
	// would discard a response still in flight.
	go func() { _, _ = io.Copy(up, client) }()
	_, _ = io.Copy(client, up)
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
