// Package hostservice is the server side of the loophole frame protocol. It owns
// the accept loop, per-connection threading, the access log, and the
// command-injection-guarded exec helper, so each daemon shrinks to a handler plus
// its allowlist.
//
// It does NOT own a transport — it owns two, and each caller names the one it
// wants: ServeUnix (host-to-host) or ServeEndpoint (jail-facing loopback-TLS via
// internal/svcendpoint, cert-pinned and token-authenticated, whose Accept returns
// only authenticated connections). There is deliberately no `Serve`; see the note
// above ServeUnix for the outage that name caused. Neither transport reaches the
// code below: Session and handleOne are net.Conn-based, which is why the accept
// loop is shared and nothing here learns which transport carried its bytes.
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
// `publishes: "socket"` daemons behind svcendpoint.ServeFront.
//
// THERE IS NO TIER 2 FOR A FRONTED DAEMON AND THERE CANNOT BE. The front splices
// a byte stream it does not parse, and nothing constrains a loophole's protocol
// to be request-shaped — it may be framed, a raw stream, audio, video. So the two
// tiers are not a fallback and a better version of one thing: a daemon here
// produces BOTH (one connection record, plus one request line per request), and a
// fronted daemon produces only the first. Do not paper that seam over by
// promising the front something it cannot deliver.
//
// One consequence worth knowing when reading the two together: tier 2's `jail=`
// is the value the CLIENT sent (`jail_id`, which the protocol says daemons must
// not trust), while tier 1's is derived host-side from the path yolo published
// at. When they disagree, tier 1 is the one that means something.
package hostservice

import (
	"errors"
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
	// JailID is Request["jail_id"] or "unknown".
	JailID string

	conn     net.Conn
	mu       sync.Mutex
	bytesOut int
	exited   bool
}

// Get exposes a raw request value.
func (s *Session) Get(key string) (any, bool) { return s.Request.Get(key) }

func (s *Session) sendFrame(streamID byte, payload []byte) {
	if s.exited {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *Session) Exit(code int) {
	if s.exited {
		return
	}
	s.mu.Lock()
	n, err := frameproto.WriteExit(s.conn, code)
	if err == nil {
		s.bytesOut += n
	}
	s.mu.Unlock()
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
func ServeUnix(handler Handler, socketPath string, stop <-chan struct{}) error {
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}

	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return err
	}
	ln.SetUnlinkOnClose(false)
	_ = os.Chmod(socketPath, 0o600)

	Logger.Printf("listening on %s (protocol v%d)", socketPath, frameproto.ProtocolVersion)
	return serveListener(handler, ln, stop)
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
func ServeEndpoint(handler Handler, endpointPath string, stop <-chan struct{}) error {
	ln, err := svcendpoint.Listen(endpointPath, "")
	if err != nil {
		return err
	}
	return serveListener(handler, ln, stop)
}

// serveListener is the transport-agnostic half: shutdown wiring plus the accept
// loop. Split out of Serve so the loop is testable against any net.Listener and so
// there is exactly one place that decides a connection becomes a session.
//
// ln.Accept MUST return only authenticated connections. svcendpoint.Listener does;
// that guarantee is what lets handleOne stay unchanged.
func serveListener(handler Handler, ln net.Listener, stop <-chan struct{}) error {
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

	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		go handleOne(handler, conn)
	}
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
func handleOne(handler Handler, conn net.Conn) {
	start := time.Now()
	jailID := "unknown"
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
	jailID = "unknown"
	if v, ok := req.Get("jail_id"); ok {
		if s, ok := v.(string); ok && s != "" {
			jailID = s
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
