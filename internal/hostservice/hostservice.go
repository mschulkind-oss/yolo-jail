// Package hostservice is the server side of the loophole frame protocol. It owns
// the accept loop, per-connection threading, the access log, and the
// command-injection-guarded exec helper, so each daemon shrinks to a handler plus
// its allowlist.
//
// It does NOT own a transport — it owns three, and each caller names the one it
// wants: ServeUnix (host-to-host), ServeFrontedUnix (an AF_UNIX socket only
// yolo's own front dials, for a `publishes: "socket"` daemon) or ServeEndpoint
// (jail-facing loopback-TLS via internal/svcendpoint, cert-pinned and
// token-authenticated, whose Accept returns only authenticated connections).
// There is deliberately no `Serve`; see the note above ServeUnix for the outage
// that name caused. None of the three reaches the code below: Session and
// handleOne are net.Conn-based, which is why the accept loop is shared and
// nothing here learns which transport carried its bytes.
//
// Frame wire format lives in internal/frameproto (the frozen contract);
// this package is the request-parsing + response-emitting harness around it.
//
// # Two audit tiers, and one cannot cover the other
//
// The access log this package writes is TIER 2 of the boundary audit: one
// structured line per REQUEST (see handleOne). It exists here and only here,
// because only here is there a parsed request to describe.
//
// TIER 1 is svcendpoint's connection-level Crossing record (its crossing.go):
// which service, which jail, when, how long, bytes each way, and whether the
// connection authenticated. It is emitted by the transport, so it covers every
// daemon that rides it — including the ones this package never sees, the
// `publishes: "socket"` daemons that speak their own protocol behind
// svcendpoint.ServeFront.
//
// THE FRONT CANNOT PRODUCE A TIER 2 LINE, and nothing should be built as if it
// could. It splices a byte stream it does not parse, and nothing constrains a
// loophole's protocol to be request-shaped — it may be framed, a raw stream,
// audio, video. So the two tiers are not a fallback and a better version of one
// thing: tier 2 comes from the DAEMON, or from nowhere. Do not paper that seam
// over by promising the front something it cannot deliver.
//
// Which is why "fronted" and "no tier 2" are not the same statement, though they
// coincided until ServeFrontedUnix existed. A `publishes: "socket"` daemon built
// on THIS package sits behind the front and still writes one request line per
// request, because the parsing happens here rather than in the splice. A fronted
// daemon yolo did not write gets tier 1 and only tier 1.
//
// One consequence worth knowing when reading the two together: WHICH `jail=` a
// tier-2 line carries now depends on the transport that delivered the request,
// and the split is the point rather than an inconsistency.
//
//   - On a PREAMBLE-BEARING connection (ServeEndpoint and ServeFrontedUnix),
//     `jail=` is the value yolo asserted in the connection preamble
//     — derived host-side from the path it published at, the SAME derivation
//     tier 1 uses. The two tiers then agree by construction, and a client that
//     sends a spoofed `jail_id` sees it overridden in both.
//   - On a bare ServeUnix connection there is no preamble, so `jail=` falls back
//     to the CLIENT's `jail_id` exactly as it always did — which the protocol
//     says daemons must not trust. NOTHING YOLO SHIPS IS ON THAT PATH ANY MORE:
//     the broker singleton was its last user, reached through a relay that stamped
//     the field in-payload, and the broker conversion moved it to ServeFrontedUnix
//     when that relay was deleted (docs/design/broker-as-a-pack.md §7). The
//     fallback stays for a host-to-host caller of ServeUnix, where no jail
//     boundary was crossed and there is nothing for yolo to assert.
//
// When the two disagree, tier 1 is still the one that means something.
package hostservice

import (
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// Logger is where the access log + diagnostics go (stderr, or a file the
// caller wires up). Matches host_service's logging.
var Logger = log.New(os.Stderr, "", log.LstdFlags)

// Session is a single client connection. A handler drives it with
// Stdout/Stderr/JSON/Exit/ExecAllowlisted. Frame writes are serialized by mu,
// mirroring Session._lock.
type Session struct {
	// Request is the parsed JSON the client sent, order-preserving.
	Request *jsonx.OrderedMap
	// JailID is the jail this request came from, in descending order of trust:
	// the HOST-ASSERTED jail_id from the connection preamble when the transport
	// carried one, else the client's own Request["jail_id"], else "unknown".
	// See the package comment for why the fallback survives.
	JailID string

	conn     net.Conn
	mu       sync.Mutex
	bytesOut int
	exited   bool
}

// Get exposes a raw request value.
func (s *Session) Get(key string) (any, bool) { return s.Request.Get(key) }

// sendFrame writes one frame, unless the session has already exited.
//
// THE EXITED CHECK IS INSIDE THE LOCK, and that is the whole point of this function's
// shape. It used to read s.exited before taking s.mu while Exit wrote it after RELEASING
// s.mu — an unsynchronised flag whose only job is to suppress output, read and written by
// different goroutines. ExecAllowlisted runs two `pump` goroutines that call this
// concurrently with the exit path, so the flag was a data race in the strict sense and, when
// it misfired, silently DROPPED a stdout frame while still sending the exit frame.
//
// SCOPE, stated honestly: no CURRENT caller demonstrably opens that window. ExecAllowlisted
// joins both pumps (wg.Wait) before its Exit, and handleOne's default Exit runs after the
// handler has returned, so the two never provably overlap today — which is also why `-race`
// does not flag it. This is therefore HARDENING of a flag that was unsynchronised by the
// memory model, not a proven fix for the 2026-08-22 CI flake in
// TestBlackboxFrontedListModeIsIdentical. It is worth noting that the flag's misfire WOULD
// produce that flake's exact signature — rc=0 with bytes_out=9, one exit frame and no stdout
// frame from a handler that had output — but a matching shape is a lead, not a diagnosis, and
// that flake's cause is still open. Dropping a client's output is the one thing this path
// must never do quietly, whether or not it has done so yet.
func (s *Session) sendFrame(streamID byte, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return
	}
	n, err := frameproto.WriteFrame(s.conn, streamID, payload)
	if err == nil {
		s.bytesOut += n
	}
}

// Stdout frame-writes to the client's stdout stream.
func (s *Session) Stdout(data string) { s.sendFrame(frameproto.StreamStdout, []byte(data)) }

// StdoutBytes frame-writes raw bytes to stdout.
func (s *Session) StdoutBytes(data []byte) { s.sendFrame(frameproto.StreamStdout, data) }

// Stderr frame-writes to the client's stderr stream.
func (s *Session) Stderr(data string) { s.sendFrame(frameproto.StreamStderr, []byte(data)) }

// JSON emits obj as one newline-terminated JSON line on stdout (compact
// separators, matching Session.json's json.dumps default).
func (s *Session) JSON(obj any) error {
	line, err := jsonx.DumpsCompact(obj)
	if err != nil {
		return err
	}
	s.Stdout(line + "\n")
	return nil
}

// Exit ends the session with an exit code (signed int32). Idempotent.
// Exit ends the session with an exit code (signed int32). Idempotent.
//
// Both the check and the set are under s.mu, for the reason in sendFrame: the flag orders
// this write against every concurrent sendFrame, so "exited" means the same thing to all of
// them. Setting it after releasing the lock left a window in which a pump goroutine could
// observe the old value and write AFTER the exit frame, or the new one and drop a frame
// before it.
func (s *Session) Exit(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return
	}
	n, err := frameproto.WriteExit(s.conn, code)
	if err == nil {
		s.bytesOut += n
	}
	s.exited = true
}

// ExecAllowlisted runs an external command whose argv is built by argvBuilder,
// enforcing that every argv element whose index is in positions belongs to
// allowlist (default: indices 1..n, i.e. everything after argv[0]). Streams the
// child's stdout/stderr back as frames and calls Exit(rc). Mirrors
// Session.exec_allowlisted.
//
// positions==nil selects the Python default (1..len-1); pass an explicit set to
// validate argv[0] too (as host_processes' pid mode does).
func (s *Session) ExecAllowlisted(
	argvBuilder func(*jsonx.OrderedMap) []string,
	allowlist map[string]struct{},
	positions map[int]struct{},
	timeout time.Duration,
) int {
	argv := argvBuilder(s.Request)
	if positions == nil {
		positions = map[int]struct{}{}
		for i := 1; i < len(argv); i++ {
			positions[i] = struct{}{}
		}
	}
	for i, arg := range argv {
		if _, checked := positions[i]; checked {
			if _, ok := allowlist[arg]; !ok {
				// Python: f"exec_allowlisted: argv[{i}]={arg!r} not in allowlist\n"
				s.Stderr("exec_allowlisted: argv[" + itoa(i) + "]=" + pytext.Repr(arg) + " not in allowlist\n")
				s.Exit(2)
				return 2
			}
		}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		// Python's Popen RAISES here (FileNotFoundError etc.), which _handle_one
		// catches -> "handler error: <e>\n" stderr frame + exit(1) + access-log
		// rc=1. Panic so handleOne's recover reproduces that exact path (byte
		// text of <e> differs — Go's "fork/exec ...: no such file" vs Python's
		// "[Errno 2] ..." — which is a ledgered-acceptable OS-string residue).
		panic(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	pump := func(r interface{ Read([]byte) (int, error) }, streamID byte) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				s.sendFrame(streamID, append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}
	go pump(stdout, frameproto.StreamStdout)
	go pump(stderr, frameproto.StreamStderr)

	rc := 0
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	if timeout > 0 {
		select {
		case err := <-done:
			rc = exitCodeFromErr(err)
		case <-time.After(timeout):
			_ = cmd.Process.Kill()
			<-done
			rc = 124
			timedOut = true
		}
	} else {
		rc = exitCodeFromErr(<-done)
	}
	wg.Wait()
	if timedOut {
		s.Stderr("exec_allowlisted: timed out\n")
	}
	s.Exit(rc)
	return rc
}

// Handler processes one Session.
type Handler func(*Session)

// THERE IS DELIBERATELY NO `Serve`. There were two transports and one function
// name, and the migration to loopback-TLS changed what that name DID while leaving
// its signature alone — so `internal/oauthbroker` kept compiling, kept passing a
// Unix socket path, and silently began publishing a token-bearing regular FILE at
// the path three call sites dial with net.Dial("unix", …). On a real host it did
// not even get that far: /tmp is 1777, and svcendpoint refuses to write a
// credential into a group/world-accessible directory, so the host-wide singleton
// could not start at all and every jail's Claude auth failed — issue #31's symptom
// on Linux, from the fix for issue #31.
//
// Both the design (loophole-transport.md §8.4, "Unchanged, deliberately: … the
// host broker singleton daemon") and the shipped manifest ("host-to-host … so it
// stays a plain Unix socket") said the singleton keeps its socket. The design was
// right; the implementation drifted, and nothing caught it because a
// signature-preserving change to a shared helper is invisible at the call site.
//
// So the ambiguous name is GONE. Each caller now names its transport, and a future
// migration cannot silently re-point one by editing a helper.

// ServeUnix serves a plain AF_UNIX socket at socketPath until SIGTERM/SIGINT (or
// stop close); one goroutine per connection.
//
// This is the HOST-TO-HOST transport: both ends are processes on the host, no jail
// boundary is crossed, and the filesystem already carries the authorization the
// loopback-TLS transport has to reconstruct with a token (svcendpoint's whole
// reason to exist). The socket is 0600 via umask, and a stale one is removed
// first so a crashed daemon does not wedge its successor.
//
// NO CONNECTION PREAMBLE, for the same reason: a preamble is yolo introducing a
// JAIL to a daemon, and nothing here crossed a boundary for yolo to have an
// opinion about. Callers of this socket write their request first, as they
// always have.
func ServeUnix(handler Handler, socketPath string, stop <-chan struct{}) error {
	ln, err := bindUnixSocket(socketPath)
	if err != nil {
		return err
	}
	Logger.Printf("listening on %s (protocol v%d)", socketPath, frameproto.ProtocolVersion)
	return serveListener(handler, ln, stop, false)
}

// ServeFrontedUnix serves a plain AF_UNIX socket at socketPath for a daemon that
// yolo puts BEHIND ITS OWN FRONT (`publishes: "socket"`): svcendpoint.ServeFront
// holds the jail-facing loopback-TLS listener, authenticates, and splices each
// accepted connection to this socket. Same bind as ServeUnix, and one goroutine
// per connection as always.
//
// So every connection accepted here arrived through the front and carries the
// CONNECTION PREAMBLE, which is read before the request exactly as ServeEndpoint
// does. That is the whole difference between the two socket entry points: who
// owns the listener, and therefore what is on the wire before the first request.
// The handler still never learns which of the three carried its bytes.
//
// THE THIRD NAME EXISTS SO ServeUnix's DOC STAYS TRUE. ServeUnix says both ends
// are host processes and no jail boundary is crossed — a fronted socket
// falsifies that sentence while looking identical from the filesystem. Adding a
// `preamble bool` to ServeUnix instead would have been exactly the
// signature-preserving change to a shared helper that the note above it records
// an outage for.
//
// THE ONE MISMATCH NOTHING IN THIS TREE CAN DETECT: a manifest declaring
// `host_daemon.preamble: false` whose daemon calls THIS function blocks forever
// on a frame yolo agreed not to send. It blocks inside the daemon, on a
// connection both ends believe is healthy, so no readiness probe, no access line
// and no test reports it — the failure is a jail request that never answers. The
// opposite pairing (`preamble: true` reaching a ServeUnix daemon) is the
// silent-CORRUPTION direction: the preamble is consumed as the client's request.
// An official pack should therefore never write `preamble: false`; teaching
// `yolo pack lint` to refuse it on a PublishesSocket daemon is where enforcement
// belongs (internal/loopholedecl/packshipped.go holds the strict decode). See
// loopholedecl.HostDaemon.Preamble for why the flag is only enforceable under
// `publishes: "socket"` in the first place.
func ServeFrontedUnix(handler Handler, socketPath string, stop <-chan struct{}) error {
	ln, err := bindUnixSocket(socketPath)
	if err != nil {
		return err
	}
	Logger.Printf("listening on %s (protocol v%d, behind yolo's front)", socketPath, frameproto.ProtocolVersion)
	return serveListener(handler, ln, stop, true)
}

// bindUnixSocket binds the AF_UNIX listener both socket entry points want, with
// the conventions this package has always applied: a stale socket left by a
// crashed predecessor is removed so it cannot wedge its successor, the parent
// directory is created, the socket is born 0600 under a 0o077 umask (and chmod'd
// after, so the mode does not depend on the umask taking effect), and
// UnlinkOnClose is OFF so a graceful shutdown cannot delete a path a successor
// has already re-bound.
//
// Sharing the BIND is safe in a way that sharing the SERVE would not be, and the
// distinction is the lesson of the note above ServeUnix: this helper produces a
// listener and decides nothing about what arrives on it. The preamble decision
// stays in the exported function the daemon named, where a future refactor
// cannot move it without changing a signature someone has to read.
func bindUnixSocket(socketPath string) (*net.UnixListener, error) {
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}

	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	_ = os.Chmod(socketPath, 0o600)
	return ln, nil
}

// ServeEndpoint publishes a loopback-TLS endpoint at endpointPath and serves it
// until a SIGTERM/SIGINT (or stop close); one goroutine per connection.
//
// This is the JAIL-FACING transport — use it when the client is inside a container
// and a Unix socket cannot cross the boundary (macOS + podman: virtiofs shares the
// inode, not the endpoint).
//
// The endpoint file it publishes IS A CREDENTIAL — it carries this service's
// per-jail bearer token — so svcendpoint writes it 0600 and refuses to publish
// into a group/world-accessible directory. Retirement is automatic: Listener.Close
// unlinks the file, so the token dies with the listener and there is no second
// artifact to reap. Nothing here persists a key or a token: the TLS private key
// lives only in this process's memory.
//
// Because this is the jail-facing transport, every connection it accepts is a
// boundary crossing and gets svcendpoint's tier-1 connection record in addition
// to this package's per-request line. ServeUnix's do not: both of its ends are
// host processes, so nothing crosses.
//
// And because it crosses, every connection here carries a CONNECTION PREAMBLE
// (svcendpoint/preamble.go): yolo's host-asserted statement of which jail is on
// the other end, read below before the request and never confused with one.
func ServeEndpoint(handler Handler, endpointPath string, stop <-chan struct{}) error {
	ln, err := svcendpoint.Listen(endpointPath, "")
	if err != nil {
		return err
	}
	return serveListener(handler, ln, stop, true)
}

// serveListener is the transport-agnostic half: shutdown wiring plus the accept
// loop. Split out of Serve so the loop is testable against any net.Listener and so
// there is exactly one place that decides a connection becomes a session.
//
// ln.Accept MUST return only authenticated connections. svcendpoint.Listener does;
// that guarantee is what lets handleOne stay unchanged.
//
// readPreamble is a PROPERTY OF THE LISTENER, named by whichever Serve* bound it,
// never inferred per connection. It cannot be inferred: a preamble and a
// frameproto request are byte-identical in shape (4-byte BE length then a JSON
// object), so sniffing would be guessing, and guessing on this path is how the
// broker's transport was silently re-pointed once already (see the note above
// ServeUnix).
func serveListener(handler Handler, ln net.Listener, stop <-chan struct{}, readPreamble bool) error {
	Logger.Printf("serving frame protocol v%d", frameproto.ProtocolVersion)

	// stop channel (explicit) OR signals (when run as a real daemon).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-stop:
		case <-sigCh:
			Logger.Print("signal received, shutting down")
		}
		_ = ln.Close()
	}()

	// EVERY HANDLER IS WAITED ON, and this loop returning therefore means "no request is
	// still being served" rather than "no NEW request will be accepted".
	//
	// It used to `go handleOne(...)` unwaited, so closing the stop channel returned from
	// here while accepted connections were still inside handleOne — including its DEFERRED
	// access-log line, which reads the package-global Logger. A caller that stops a daemon
	// and then touches anything that daemon can still touch is racing it, and the tests do
	// exactly that: captureAccessLog swaps Logger per test, so a leaked handler from the
	// PREVIOUS test wrote through the swap. Measured as a real `-race` failure with
	// `go test -race -count=40 -run TestBlackbox ./internal/hostprocesses/` (2026-08-22),
	// reproducing against unmodified HEAD.
	//
	// It is a shutdown-correctness fix before it is a test fix: a daemon whose Serve has
	// returned while a handler still writes to a client is one that cannot be shut down
	// deterministically by anyone, and the leak scales with in-flight requests rather than
	// being bounded.
	var inFlight sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		inFlight.Add(1)
		go func() {
			defer inFlight.Done()
			handleOne(handler, conn, readPreamble)
		}()
	}
	inFlight.Wait()
	return nil
}

// handleOne receives one request, invokes the handler, logs the summary.
//
// THE ACCESS LINE IS TIER 2 (see the package comment). It is deliberately
// PAYLOAD-FREE — the request's top-level key NAMES, never its values, plus the
// exit code, the elapsed time and the total bytes written. Same rule svcendpoint
// logs under: a length, never a value. Enough to audit "what did jail X ask for"
// without hoarding request bodies, which can be large or sensitive.
//
// Unchanged by the arrival of tier 1, on purpose. The connection-level record
// says a crossing happened and how big it was; this says what was asked. Deleting
// either in favour of the other loses information that the other cannot
// reconstruct.
//
// THE PREAMBLE IS READ FIRST AND IS NEVER FALLEN BACK ON. When readPreamble is
// set, svcendpoint.ReadPreamble consumes exactly one frame off the raw conn
// before frameproto sees a byte, and a failure DROPS the connection — it does not
// retry the same bytes as a request. A fallback would reinstate the framing
// coincidence transport_test.go's case 2 documents (a 64-byte token frame that
// "failed JSON decode anyway"), this time on the auditing path, where the payoff
// for guessing right is a request attributed to the wrong jail.
//
// It also must not be FATAL to the daemon: yolo's own readiness probe for a
// fronted service is a bare connect-and-close that bypasses the front and
// therefore sends nothing at all, so "closed before a preamble" degrades exactly
// as "closed before a request" already does — one log line, one closed
// connection, the accept loop untouched.
func handleOne(handler Handler, conn net.Conn, readPreamble bool) {
	start := time.Now()
	jailID := "unknown"
	hostAsserted := false
	var reqKeys []string
	var rcForLog *int
	var sess *Session
	defer func() {
		elapsedMs := int(time.Since(start).Milliseconds())
		keys := "-"
		if len(reqKeys) > 0 {
			sort.Strings(reqKeys)
			keys = strings.Join(reqKeys, ",")
		}
		bytesOut := 0
		if sess != nil {
			bytesOut = sess.bytesOut
		}
		Logger.Print(frameproto.AccessLogLine(jailID, keys, rcForLog, elapsedMs, bytesOut))
		_ = conn.Close()
	}()

	if readPreamble {
		p, perr := svcendpoint.ReadPreamble(conn)
		if perr != nil {
			// PAYLOAD-FREE by classification, not by formatting: perr wraps a
			// json decode error that can quote a byte of the frame, and the
			// preamble is a versioned envelope meant to grow. Name the fault,
			// never the bytes.
			switch {
			case errors.Is(perr, io.EOF), errors.Is(perr, io.ErrUnexpectedEOF):
				Logger.Printf("conn closed without a request")
			case errors.Is(perr, svcendpoint.ErrPreambleVersion):
				Logger.Printf("connection preamble rejected: unrecognized version")
			case errors.Is(perr, svcendpoint.ErrBadPreambleFrame):
				Logger.Printf("connection preamble rejected: malformed frame")
			default:
				Logger.Printf("connection preamble unreadable")
			}
			return
		}
		// HOST-ASSERTED, so it outranks anything the request says. Recorded now
		// rather than after the request parse, so a client that opens a
		// preamble-bearing connection and then sends nothing is still attributed.
		//
		// An EMPTY jail_id is not an assertion. yolo cannot produce one today
		// (crossingName normalizes a degenerate path to "unknown"), but treating
		// "" as authoritative would turn a future encoding slip into a silently
		// blank audit column instead of a fall back to what is known.
		if p.JailID != "" {
			jailID, hostAsserted = p.JailID, true
		}
	}

	body, err := frameproto.ReadRequestBytes(conn)
	if err != nil {
		Logger.Printf("conn closed without a request")
		return
	}
	decoded, derr := jsonx.Decode(body)
	if derr != nil {
		// Python's _read_request returns None on bad JSON -> treated as no
		// request (conn closed without a request).
		Logger.Printf("conn closed without a request")
		return
	}
	req, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		Logger.Printf("conn closed without a request")
		return
	}
	reqKeys = req.Keys()
	// The client's own claim is consulted ONLY when the transport asserted
	// nothing. On a preamble-bearing connection a spoofed jail_id is still
	// visible in keys= and is still ignored here — which is the whole point.
	if !hostAsserted {
		jailID = "unknown"
		if v, ok := req.Get("jail_id"); ok {
			if s, ok := v.(string); ok && s != "" {
				jailID = s
			}
		}
	}
	sess = &Session{Request: req, JailID: jailID, conn: conn}
	rc := 0
	rcForLog = &rc
	func() {
		defer func() {
			if r := recover(); r != nil {
				Logger.Printf("handler raised: %v", r)
				sess.Stderr("handler error: " + panicMsg(r) + "\n")
				sess.Exit(1)
				rc = 1
			}
		}()
		handler(sess)
		sess.Exit(0) // default exit if handler didn't
	}()
}

// exitCodeFromErr extracts a process exit code from cmd.Wait's error, mapping
// a signal death to -N — the value Python's proc.wait() returns and
// Session.exit packs into the signed exit frame (e.g. -11 for SIGSEGV). Go's
// exec.ExitError.ExitCode() returns -1 for ANY signal, which would make
// yolo-ps exit 255 instead of 245.
func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return -int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

func panicMsg(r any) string {
	if err, ok := r.(error); ok {
		return err.Error()
	}
	if s, ok := r.(string); ok {
		return s
	}
	return "panic"
}
