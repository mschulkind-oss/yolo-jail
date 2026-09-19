package svcendpoint

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Logger is where this package's diagnostics go. Matching internal/hostservice so
// a daemon can point both at one file.
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
	host, _ := advertiseHostWithSource()
	return host
}

// Where an advertised host came from. A WRONG-BUT-PLAUSIBLE advertise value is the
// one fault in this package that NO test on this machine can catch — a nested jail
// is forced onto --net=host, so the bind address and the advertised name land in
// the same place and a mismatch cannot reproduce (AGENTS.md, Testing, carve-out 1).
// The runtime line is therefore the only instrument left, and "the jail cannot
// reach this service" has to be answerable from it: which half is wrong, and when
// it is the published name, WHO chose it. The three sources are three different
// faults — a caller-supplied name is the run pipeline's backend decision
// (run.advertiseHostFor, which answers 127.0.0.1 for a launcher-netns backend), the
// environment override is a human's or the value the pipeline handed a spawned
// daemon, and the default is the container runtime's gateway name, whose forwarding
// is the launcher's YOLO_HOST_LOOPBACK question and not this package's.
const (
	advertiseFromCaller  = "caller-supplied"
	advertiseFromEnv     = "from $" + AdvertiseHostEnv
	advertiseFromDefault = "runtime gateway default"
)

// advertiseHostWithSource is AdvertiseHost plus the provenance of its answer.
// Unexported: the source exists to be STATED, not branched on.
func advertiseHostWithSource() (host, source string) {
	if v := os.Getenv(AdvertiseHostEnv); v != "" {
		return v, advertiseFromEnv
	}
	return DefaultAdvertiseHost, advertiseFromDefault
}

// bindAddr is the only address any listener here binds: loopback, kernel-assigned
// port. Spelled ONCE so the bind diagnostic below cannot name an address different
// from the one that was tried.
const bindAddr = "127.0.0.1:0"

// listenLoopback is net.Listen, indirected for ONE reason: a 127.0.0.1:0 bind
// cannot be made to fail from a test, and its failure diagnostic is the only thing
// a host whose loopback is unusable (an empty netns, EMFILE, a sandbox policy) ever
// gets. Production must never reassign it.
var listenLoopback = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// mintCertificate and mintBearerToken are mintCert and NewToken, indirected for the
// same single reason as listenLoopback above: both fail only when crypto/rand does,
// which no test can arrange, and each failure's only trace is the line beside it.
// Production must never reassign either.
var (
	mintCertificate = mintCert
	mintBearerToken = NewToken
)

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
	return listenWith(publishPath, advertiseHost, CrossingViaEndpoint, true)
}

// listenWith is Listen plus the audit's "how was this served" label and the
// connection-preamble switch. Unexported because neither is a transport choice —
// there is one transport — only which server shape sits behind it and whether
// yolo introduces itself to the daemon on the way in, both of which the caller
// knows and a daemon does not.
//
// preamble is TRUE for Listen, so the framework default is ON everywhere and a
// daemon that is taught to read one never has to ask which shape delivered it.
//
// THERE IS NO OPT-OUT LEFT IN THE TREE, and its disappearance is worth a line
// because it was on the credential path. The broker relay's front set it: the
// relay consumed the first frame off the wire itself to stamp a jail_id into it,
// so a preamble in front would have been stamped INSTEAD of the request and every
// jail's Claude OAuth refresh would have failed. The broker conversion deleted the
// relay and its parse together (docs/design/broker-as-a-pack.md §7) — the preamble
// is what replaced them. The knob stays, for a config-declared daemon yolo did not
// write (internal/loopholes' discover.go defaults it OFF for those), but no yolo
// daemon sets it.
func listenWith(publishPath, advertiseHost, via string, preamble bool) (*Listener, error) {
	advertiseSource := advertiseFromCaller
	if advertiseHost == "" {
		advertiseHost, advertiseSource = advertiseHostWithSource()
	}
	if err := ensurePrivateDir(filepath.Dir(publishPath)); err != nil {
		// The directory check's own errors name the directory; this names the FILE
		// that will therefore never be published, which is what a caller reading
		// "service X is not there" is looking for.
		return nil, fmt.Errorf("svcendpoint: cannot publish %s: %w", publishPath, err)
	}
	raw, err := listenLoopback(bindAddr)
	if err != nil {
		// THE ADDRESS AND THE SYSCALL ERROR, VERBATIM, BOTH HALVES. net.Listen's
		// error already carries the address; naming it again here is deliberate,
		// because the wrap is what survives a caller that prints only its own
		// sentence, and an "address already in use" with no address is the bug this
		// line exists to prevent.
		Logger.Printf("bind %s failed: %v — nothing will be published at %s", bindAddr, err, publishPath)
		return nil, fmt.Errorf("svcendpoint: bind %s for %s: %w", bindAddr, publishPath, err)
	}
	// The three teardowns below discard their Close error ON PURPOSE: each unwinds a
	// bind we are abandoning, the error being returned IS the diagnosis, and a
	// failure to close a listener nothing will ever accept on is not something a
	// caller can act on. What must not be silent is the reason we are unwinding, so
	// that is logged instead.
	cert, der, err := mintCertificate()
	if err != nil {
		_ = raw.Close()
		Logger.Printf("minting a certificate for %s failed: %v — %s stays unpublished", publishPath, err, bindAddr)
		return nil, fmt.Errorf("svcendpoint: mint certificate for %s: %w", publishPath, err)
	}
	token, err := mintBearerToken()
	if err != nil {
		_ = raw.Close()
		Logger.Printf("minting a token for %s failed: %v — %s stays unpublished", publishPath, err, bindAddr)
		return nil, fmt.Errorf("svcendpoint: mint token for %s: %w", publishPath, err)
	}
	tlsLn := tls.NewListener(raw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	port := raw.Addr().(*net.TCPAddr).Port
	hostport := net.JoinHostPort(advertiseHost, strconv.Itoa(port))
	if err := Publish(publishPath, Endpoint{HostPort: hostport, CertDER: der, Token: token}); err != nil {
		_ = tlsLn.Close()
		// Publish's errors name the path already; the bind that is being thrown away
		// with it is the part only this frame knows.
		Logger.Printf("publishing %s failed: %v — the listener bound to %s is being retired unused",
			publishPath, err, raw.Addr())
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
	// THE BIND/ADVERTISE PAIR, AND THE PROVENANCE OF THE ADVERTISED HALF, on one
	// line, once per listener. Both addresses because they are two different facts
	// that fail independently, and the source because the three sources are three
	// different fixes (see the advertiseFrom* constants). Payload-free: two
	// addresses, a name yolo chose, and a path — never the token or the cert.
	Logger.Printf("listening on %s, advertising %s to the jail (%s) — cert-pinned, token-authenticated -> %s",
		raw.Addr(), hostport, advertiseSource, publishPath)
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
// THE END OF THIS LOOP IS THE END OF THE SERVICE, so it is reported HERE rather
// than left to whoever reads Accept's error — because one of the two server shapes
// does not read it at all (ServeFrontWithOptions returns nil on any accept failure,
// and every caller of that discards the return). An unreported accept-loop exit is
// a published endpoint file naming a listener that no longer accepts: the jail's
// next dial fails at connect, `yolo check` calls the front dead, and nothing
// anywhere says when or why it stopped.
//
// net.ErrClosed is the ORDINARY path — Close makes Accept return it — so it is not
// reported: Close is the caller's own act, and its own diagnostics (the endpoint
// retirement below) cover it.
func (l *Listener) acceptLoop() {
	for {
		conn, err := l.tlsLn.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				Logger.Printf("accept loop for %s ended: %v — %s no longer accepts, and the published endpoint file still names it",
					l.publishPath, err, l.raw.Addr())
			}
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
		//
		// The Close error is discarded deliberately: this rejection is already
		// reported twice (that log line, and the tier-1 record below), and a failure
		// to close a connection nothing will read again is not actionable.
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
		// AUTHENTICATED, ACKED, AND THEN DROPPED — the listener shut down before
		// Accept could take this connection. From the client's side that is a
		// connection that died immediately after a successful handshake for no
		// stated reason, and nothing else states one: the tier-1 record calls it an
		// accepted crossing with zero bytes, which reads as an idle client. Said
		// once, here, per dropped connection.
		Logger.Printf("dropping an authenticated connection to %s: the listener closed before it was accepted (%s)",
			l.publishPath, l.service)
		// cc.Close emits that tier-1 record; its own error is discarded for the
		// same reason as the rejection path above.
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

// Addr returns the REAL bound address (127.0.0.1:<kernel-assigned port>), not the
// advertised host:port that was published.
func (l *Listener) Addr() net.Addr { return l.raw.Addr() }

// Close stops accepting and UNLINKS the published endpoint file, so retiring the
// listener retires its credential in the same step. Idempotent.
func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		l.shutdown(net.ErrClosed)
		// The listener's own Close error becomes this call's return value and is
		// not logged separately: a caller that ignores it has nothing to do about a
		// listener that is going away regardless.
		l.closeErr = l.tlsLn.Close()
		if err := os.Remove(l.publishPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			// REPORTED HERE, not left to the caller, because BOTH front paths
			// discard this return (front.go's deferred close and its stop
			// goroutine) and because of what the failure leaves behind: a published
			// endpoint file that outlives its listener is a live-looking credential
			// for a dead port. The next dial gets a connect error, `yolo check`
			// calls the service dead, and the file that made it look alive is never
			// named. The path, never the contents.
			Logger.Printf("retiring %s failed: %v — the file still names this listener, which is gone",
				l.publishPath, err)
			if l.closeErr == nil {
				l.closeErr = err
			}
		}
	})
	return l.closeErr
}
