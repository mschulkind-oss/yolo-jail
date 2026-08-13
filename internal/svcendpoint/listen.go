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

// Listener is an authenticated loopback-TLS listener. Accept returns ONLY
// connections that presented the right token, so a daemon cannot forget to
// authenticate — the failure is unrepresentable rather than handled.
type Listener struct {
	raw         net.Listener // the bound TCP listener; source of the real port
	tlsLn       net.Listener // raw wrapped in TLS
	publishPath string
	token       string

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

// Listen binds 127.0.0.1 on a kernel-assigned port, mints a certificate and a
// token, and publishes them to publishPath. An empty advertiseHost resolves via
// AdvertiseHost().
//
// THE ORDER BELOW IS LOAD-BEARING:
//
//  1. verify the publication directory FIRST, so a bad directory fails before a
//     port is bound and before a key exists;
//  2. bind 127.0.0.1:0 and let the KERNEL assign the port — nothing probes a port
//     for us to re-bind, so there is no window for another local process to squat
//     it, and this is exactly why the address must be PUBLISHED rather than
//     passed in;
//  3. mint the cert (private key: memory only);
//  4. mint the token (crypto/rand, memory only);
//  5. wrap in TLS;
//  6. read the port from the RAW listener, after bind;
//  7. join the ADVERTISED host to the LOCAL port. Bind 127.0.0.1 (off the LAN),
//     advertise the gateway name the jail resolves. Reverse these two and the
//     jail dials its own loopback;
//  8. publish AFTER a successful bind, so a published file always names a live
//     listener — which is what makes a Probe-based health check meaningful.
func Listen(publishPath, advertiseHost string) (*Listener, error) {
	if advertiseHost == "" {
		advertiseHost = AdvertiseHost()
	}
	if err := ensurePrivateDir(filepath.Dir(publishPath)); err != nil {
		return nil, err
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
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
	l := &Listener{
		raw:         raw,
		tlsLn:       tlsLn,
		publishPath: publishPath,
		token:       token,
		ready:       make(chan net.Conn),
		closed:      make(chan struct{}),
	}
	go l.acceptLoop()
	Logger.Printf("listening on 127.0.0.1:%d (advertised %s, cert-pinned, token-authenticated) -> %s",
		port, hostport, publishPath)
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
	if err := verifyTokenFrame(conn, l.token); err != nil {
		// A missing, oversized, zero-length or mismatched token drops the
		// connection. verifyTokenFrame already logged, payload-free.
		_ = conn.Close()
		return
	}
	select {
	case l.ready <- conn:
	case <-l.closed:
		_ = conn.Close()
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

// Addr returns the REAL bound address (127.0.0.1:<kernel-assigned port>), not the
// advertised host:port that was published.
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
