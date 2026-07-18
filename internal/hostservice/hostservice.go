// Package hostservice is the server side of the unix-socket loophole frame
// protocol — the Go port of src/host_service.py's serve/Session/exec_allowlisted.
// It owns socket setup, the accept loop, per-connection threading, the access
// log, and the command-injection-guarded exec helper, so each daemon shrinks
// to a handler plus its allowlist.
//
// Frame wire format lives in internal/frameproto (the frozen contract);
// this package is the request-parsing + response-emitting harness around it.
//
// Source of truth: src/host_service.py.
package hostservice

import (
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
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
		s.Stderr("exec_allowlisted: " + err.Error() + "\n")
		s.Exit(1)
		return 1
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

// Serve listens on socketPath until a SIGTERM/SIGINT (or stop close); one
// goroutine per connection. The socket is created 0600 and removed on exit.
// Mirrors host_service.serve.
func Serve(handler Handler, socketPath string, stop <-chan struct{}) error {
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}
	if err := os.MkdirAll(dirOf(socketPath), 0o755); err != nil {
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
		conn, err := ln.AcceptUnix()
		if err != nil {
			break
		}
		go handleOne(handler, conn)
	}
	_ = os.Remove(socketPath)
	return nil
}

// handleOne receives one request, invokes the handler, logs the summary.
// Mirrors host_service._handle_one.
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

// exitCodeFromErr extracts a process exit code from cmd.Wait's error.
func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func dirOf(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return "."
	}
	if i == 0 {
		return "/"
	}
	return path[:i]
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
