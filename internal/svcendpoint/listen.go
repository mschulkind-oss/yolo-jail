package svcendpoint

import (
	"crypto/tls"
	"errors"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Logger is where this package's diagnostics go. Matching internal/hostservice and
// internal/brokerrelay so a daemon can point all three at one file.
//
// EVERY line it emits is payload-free by construction. Never add a line that
// prints an endpoint line, a token, or a cert: that would write a live credential
// into ~/.local/share/yolo-jail/logs/ (and into any transcript), and CI's secret
// scan runs --only-verified, so it would not be caught.
var Logger = log.New(os.Stderr, "", log.LstdFlags)

// AdvertiseHostEnv names the environment variable that overrides the host name
// published for clients to dial. It is read from a HOST child process's
// environment, never from inside a jail, so it carries no inheritance problem.
//
// One definition, here, on purpose: a per-daemon flag would have to be added to
// every daemon's flag set (three today, every future one) and would make the
// framework's contract with a daemon two placeholders instead of one — which is
// the drift this package exists to prevent.
const AdvertiseHostEnv = "YOLO_SVC_ADVERTISE_HOST"

// DefaultAdvertiseHost is the container runtime's host-gateway name: what a jail
// resolves to reach the host it runs on.
const DefaultAdvertiseHost = "host.containers.internal"

// AdvertiseHost resolves the host name to publish: AdvertiseHostEnv when set and
// non-empty, else DefaultAdvertiseHost. Daemons should call this rather than
// re-reading the variable, so the name and its default cannot be spelled twice.
func AdvertiseHost() string {
	if v := os.Getenv(AdvertiseHostEnv); v != "" {
		return v
	}
	return DefaultAdvertiseHost
}

// BindHostEnv names the environment variable that overrides the address the
// listener BINDS to. Like AdvertiseHostEnv, it is read from a HOST child
// process's environment, never from inside a jail, so it carries no inheritance
// problem.
//
// One definition, here, on purpose — the same reasoning as AdvertiseHostEnv: a
// per-daemon flag would have to be added to every daemon's flag set.
const BindHostEnv = "YOLO_SVC_BIND_HOST"

// DefaultBindHost is the loopback address the listener binds by default: off the
// LAN, and correct wherever the runtime forwards host.containers.internal to
// loopback (slirp4netns, macOS podman-machine). netavark does NOT forward, so
// the launcher overrides this with the bridge gateway address — reachable from
// the jail, not routable from the LAN.
const DefaultBindHost = "127.0.0.1"

// BindHost resolves the address to bind: BindHostEnv when set and non-empty,
// else DefaultBindHost. Daemons should call this rather than re-reading the
// variable, so the name and its default cannot be spelled twice.
func BindHost() string {
	if v := os.Getenv(BindHostEnv); v != "" {
		return v
	}
	return DefaultBindHost
}

// Listener is an authenticated loopback-TLS listener. Accept returns ONLY
// connections that presented the right token, so a daemon cannot forget to
// authenticate — the failure is unrepresentable rather than handled.
type Listener struct {
	raw         net.Listener // the bound TCP listener; source of the real port
	tlsLn       net.Listener // raw wrapped in TLS
	publishPath string
	token       string

	// Connection-level audit identity, derived ONCE from publishPath at bind and
	// never mutated after acceptLoop starts (which is why via is a listenWith
	// parameter rather than a field ServeFront assigns afterwards — that would
	// race the accept loop). See crossing.go.
	service, jail, via string

	// pre is the connection preamble every accepted connection is handed, built
	// ONCE at bind from service/jail above, or nil when this listener sends none
	// (FrontOptions.NoPreamble). READ-ONLY after bind and SHARED by every
	// connection: countingConn only reslices its own view of it.
	pre []byte

	// ready carries authenticated conns from the accept loop to Accept. It is
	// never closed: pending auth goroutines still hold a send on it.
	ready chan net.Conn
	// closed is closed exactly once, by whichever of Close or the accept loop's
	// exit happens first. It is the ONLY shutdown signal both sides read.
	closed chan struct{}

	shutOnce sync.Once
	errMu    sync.Mutex
	firstErr error

	closeOnce sync.Once
	closeErr  error
}

// Listen binds bindHost on a kernel-assigned port, mints a certificate and a
// token, and publishes them to publishPath. An empty advertiseHost resolves via
// AdvertiseHost(); an empty bindHost resolves via BindHost().
//
// THE ORDER BELOW IS LOAD-BEARING:
//
//  1. verify the publication directory FIRST, so a bad directory fails before a
//     port is bound and before a key exists;
//  2. bind <BindHost()>:0 and let the KERNEL assign the port — nothing probes a
//     port for us to re-bind, so there is no window for another local process to
//     squat it, and this is exactly why the address must be PUBLISHED rather
//     than passed in;
//  3. mint the cert (private key: memory only);
//  4. mint the token (crypto/rand, memory only);
//  5. wrap in TLS;
//  6. read the port from the RAW listener, after bind;
//  7. join the ADVERTISED host to the LOCAL port. Bind off the LAN (loopback by
//     default, or the bridge gateway address the launcher overrides it to under
//     netavark), advertise the gateway name the jail resolves. Reverse these two
//     and the jail dials its own loopback;
//  8. publish AFTER a successful bind, so a published file always names a live
//     listener — which is what makes a Probe-based health check meaningful.
func Listen(publishPath, advertiseHost, bindHost string) (*Listener, error) {
	return listenWith(publishPath, advertiseHost, bindHost, CrossingViaEndpoint, true)
}

// listenWith is Listen plus the audit's "how was this served" label and the
// connection-preamble switch. Unexported because neither is a transport choice —
// there is one transport — only which server shape sits behind it and whether
// yolo introduces itself to the daemon on the way in, both of which the caller
// knows and a daemon does not.
//
// preamble is TRUE for Listen, so the framework default is ON everywhere and a
// daemon that is taught to read one never has to ask which shape delivered it.
// The single opt-out in the tree is the broker relay's front (brokerrelay.go),
// which consumes the first frame off the wire itself.
func listenWith(publishPath, advertiseHost, bindHost, via string, preamble bool) (*Listener, error) {
	if advertiseHost == "" {
		advertiseHost = AdvertiseHost()
	}
	if bindHost == "" {
		bindHost = BindHost()
	}
	if err := ensurePrivateDir(filepath.Dir(publishPath)); err != nil {
		return nil, err
	}
	raw, err := net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
	if err != nil {
		return nil, err
	}
	cert, der, err := mintCert()
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	token, err := NewToken()
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	tlsLn := tls.NewListener(raw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	port := raw.Addr().(*net.TCPAddr).Port
	hostport := net.JoinHostPort(advertiseHost, strconv.Itoa(port))
	if err := Publish(publishPath, Endpoint{HostPort: hostport, CertDER: der, Token: token}); err != nil {
		_ = tlsLn.Close()
		return nil, err
	}
	service, jailName := crossingIdentity(publishPath)
	// The preamble is encoded ONCE, here, from the SAME derivation tier 1 uses —
	// which is what makes "the connection record and the daemon's idea of the
	// jail agree" true by construction rather than by two derivations that have
	// to be kept in step. Nothing per-connection recomputes it; countingConn
	// reslices this array (crossing.go).
	var pre []byte
	if preamble {
		pre = encodePreamble(Preamble{JailID: jailName, Service: service, V: PreambleVersion})
	}
	l := &Listener{
		raw:         raw,
		tlsLn:       tlsLn,
		publishPath: publishPath,
		token:       token,
		service:     service,
		jail:        jailName,
		via:         via,
		pre:         pre,
		ready:       make(chan net.Conn),
		closed:      make(chan struct{}),
	}
	go l.acceptLoop()
	Logger.Printf("listening on %s (advertised %s, cert-pinned, token-authenticated) -> %s",
		raw.Addr().String(), hostport, publishPath)
	return l, nil
}

// acceptLoop accepts raw TLS connections and authenticates each in its OWN
// goroutine before offering it to Accept.
//
// Authenticating inline in Accept would serialize the handshake: one connection
// that opens and never writes would block every other client for handshakeTimeout,
// and a loop of those is a trivial denial of service from precisely the adversary
// this transport is built against. So the cost of a stalled or hostile connection
// stays with that connection.
func (l *Listener) acceptLoop() {
	for {
		conn, err := l.tlsLn.Accept()
		if err != nil {
			l.shutdown(err)
			return
		}
		go l.authenticate(conn)
	}
}

func (l *Listener) authenticate(conn net.Conn) {
	start := time.Now()
	if err := verifyTokenFrame(conn, l.token); err != nil {
		// A missing, oversized, zero-length or mismatched token drops the
		// connection. verifyTokenFrame already logged, payload-free.
		_ = conn.Close()
		// AUDIT (tier 1, crossing.go). A REJECTED crossing is at least as
		// interesting as an accepted one — it is the only record that a jail, or
		// something wearing one's address, tried and failed to get through — so
		// it is recorded here rather than left as a silent drop. Byte counts are
		// zero by construction: nothing but the pre-auth handshake happened, and
		// the wrapper that counts is not installed until after this point.
		recordCrossing(Crossing{
			Service: l.service, Jail: l.jail, Via: l.via,
			Outcome: CrossingRejected, Reason: crossingRejectReason(err),
			At: start, Duration: time.Since(start),
		})
		return
	}
	// AUTHENTICATION PRECEDES THE WRAPPER, so it precedes the preamble: a
	// connection that failed the token check returned above and no daemon ever
	// sees it, let alone yolo's assertion about which jail it came from.
	cc := newCountingConn(conn, l.service, l.jail, l.via, start, l.pre)
	select {
	case l.ready <- cc:
	case <-l.closed:
		_ = cc.Close()
	}
}

// shutdown records the first terminal error and signals every waiter, once.
func (l *Listener) shutdown(err error) {
	l.shutOnce.Do(func() {
		l.errMu.Lock()
		if l.firstErr == nil {
			l.firstErr = err
		}
		l.errMu.Unlock()
		close(l.closed)
	})
}

// Accept returns the next AUTHENTICATED connection. It never returns a connection
// that failed the token check, and it never returns one before the ack was sent.
//
// The returned net.Conn is a *countingConn wrapping the *tls.Conn (crossing.go):
// it forwards every method by embedding, counts bytes each way, and emits this
// connection's tier-1 audit record when it is CLOSED. Callers must therefore keep
// closing what they accept — they already do — and must not type-assert an
// accepted connection to a concrete transport type.
//
// AND ITS READ STREAM BEGINS WITH THE CONNECTION PREAMBLE unless this listener
// was built with preamble=false: 4-byte BE length then a JSON object, once, at
// connection open (preamble.go). It is a PREFIX, not a concatenation — the first
// Read returns preamble bytes and no more — so a caller that reads it must use
// ReadPreamble, and a caller that does not must never be handed this connection.
// Nothing is added to the write direction, so the client neither sees the
// preamble nor can suppress it.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ready:
		return conn, nil
	case <-l.closed:
		l.errMu.Lock()
		err := l.firstErr
		l.errMu.Unlock()
		if err == nil {
			err = net.ErrClosed
		}
		return nil, err
	}
}

// Addr returns the REAL bound address (<bindHost>:<kernel-assigned port>), not
// the advertised host:port that was published.
func (l *Listener) Addr() net.Addr { return l.raw.Addr() }

// Close stops accepting and UNLINKS the published endpoint file, so retiring the
// listener retires its credential in the same step. Idempotent.
func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		l.shutdown(net.ErrClosed)
		l.closeErr = l.tlsLn.Close()
		if err := os.Remove(l.publishPath); err != nil && !errors.Is(err, fs.ErrNotExist) && l.closeErr == nil {
			l.closeErr = err
		}
	})
	return l.closeErr
}
