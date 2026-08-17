package journald

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// Main is the builtin journal-bridge daemon entry point. It accepts
// connections, reads a newline-terminated JSON request, validates the
// journalctl args, execs journalctl, and streams stdout/stderr/exit back as
// ">BI" frames with stream IDs 1/2/3.
//
// CLI contract: exactly one of --endpoint (jail-facing loopback-TLS) or
// --socket (host-to-host AF_UNIX), plus --mode ("user"|"full") and --log-file.
//
// TWO TRANSPORTS, NAMED BY THE CALLER, never guessed from the path. The same
// split hostservice draws between ServeEndpoint and ServeUnix, and for the same
// reason: a daemon that inferred its transport would be one refactor away from
// publishing a bearer-token file where a socket was meant, or binding a socket
// no jail can cross (docs/design/loophole-transport.md §2).
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-journald", flag.ExitOnError)
	socket := fs.String("socket", "", "AF_UNIX socket to bind (host-to-host)")
	endpoint := fs.String("endpoint", "", "loopback-TLS endpoint file to publish (jail-facing)")
	mode := fs.String("mode", "user", `"user" or "full"`)
	logFile := fs.String("log-file", "", "append per-request audit log here (default: stderr)")
	_ = fs.Parse(argv)
	switch {
	case *socket == "" && *endpoint == "":
		fmt.Fprintln(os.Stderr, "yolo-journald: one of --endpoint or --socket is required")
		return 2
	case *socket != "" && *endpoint != "":
		fmt.Fprintln(os.Stderr, "yolo-journald: --endpoint and --socket are mutually exclusive")
		return 2
	}
	setupLog(*logFile)

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; close(stop) }()

	serve := func() error { return Serve(*socket, *mode, stop) }
	if *endpoint != "" {
		serve = func() error { return ServeEndpoint(*endpoint, *mode, stop) }
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-journald:", err)
		return 1
	}
	return 0
}

// ServeEndpoint publishes a loopback-TLS endpoint at endpointPath and serves it
// until stop is closed. svcendpoint.Listen's Accept returns ONLY connections
// that presented the right token, so this daemon cannot forget to
// authenticate — the failure is unrepresentable rather than handled.
//
// Nothing below the accept loop differs from Serve: handleConn is net.Conn-based
// and never learns which transport carried its bytes. That is the whole point
// (docs/design/loophole-transport.md §8.1), and it is why Serve's own test suite
// still pins the protocol with its assertions unchanged.
//
// The ONE thing that does differ is above the accept loop: a jail-facing
// connection carries yolo's CONNECTION PREAMBLE (svcendpoint/preamble.go) and
// this daemon has to consume it before handleConn reads a byte. Serve's AF_UNIX
// path has no preamble and is untouched — both ends there are host processes.
func ServeEndpoint(endpointPath, mode string, stop <-chan struct{}) error {
	ln, err := svcendpoint.Listen(endpointPath, "", "")
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	go func() { <-stop; _ = ln.Close() }()

	for {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return nil // listener closed on stop, or the accept loop ended
		}
		go func(conn net.Conn) {
			// INSIDE THE GOROUTINE, NEVER IN THE ACCEPT LOOP: reading in the loop
			// would let one client that connects and never writes stall every
			// other client for the handshake timeout — the denial of service
			// svcendpoint's own accept loop is structured to avoid.
			//
			// AND ON THE RAW conn, never through a bufio.Reader: readHeaderCapped
			// reads the request one byte at a time, so a buffered read here would
			// swallow the head of the client's header into a buffer handleConn
			// never sees. ReadPreamble is io.ReadFull-based and consumes exactly
			// the frame, which is what makes the raw read safe.
			if _, perr := svcendpoint.ReadPreamble(conn); perr != nil {
				// Drop the connection; never re-read these bytes as a request.
				// A bare connect-and-close probe lands here and must not be
				// louder than it is. Payload-free by classification.
				logf("[journal] connection preamble rejected; connection dropped")
				_ = conn.Close()
				return
			}
			handleConn(conn, mode)
		}(conn)
	}
}

// Serve binds the Unix socket, accepts connections, and serves each journal
// request in its own goroutine until stop is closed (or the listener fails).
// The socket is chmod 0777 (frozen) so any jail UID can dial it. Callers wire
// their own signal handling to close stop.
func Serve(socket, mode string, stop <-chan struct{}) error {
	_ = os.Remove(socket)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}
	ln.SetUnlinkOnClose(false)
	_ = os.Chmod(socket, 0o777)

	go func() { <-stop; ln.Close(); os.Remove(socket) }()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			break
		}
		go handleConn(conn, mode)
	}
	os.Remove(socket)
	return nil
}

func handleConn(conn net.Conn, mode string) {
	defer conn.Close()

	// Read the JSON request header up to the first newline, capped at the
	// Python daemon's JOURNAL_MAX_HEADER (16384 bytes) — a newline-less or
	// over-cap client must be rejected, not allowed to grow daemon memory.
	header, foundNL := readHeaderCapped(conn, MaxHeaderBytes)
	if len(header) == 0 && !foundNL {
		return
	}
	if !foundNL {
		_ = WriteFrame(conn, FrameStderr, []byte("yolo-journal: malformed request\n"))
		_ = WriteExit(conn, 2)
		return
	}

	v := ParseRequest(header, mode)
	if v.ErrText != "" {
		_ = WriteFrame(conn, FrameStderr, []byte(v.ErrText))
		_ = WriteExit(conn, v.ExitCode)
		return
	}

	// Per-request audit log (module map freezes this): "[journal] mode=.. args=..".
	logf("[journal] mode=%s args=%s", mode, ArgsJSON(v.Args))

	cmd := exec.Command("journalctl", v.Args...)
	cmd.Stdin = nil
	// start_new_session=True (Python): isolate journalctl in its own session so
	// a group-directed signal at the daemon (Commit A SIGTERM/PDEATHSIG cascade)
	// doesn't also hit a live journalctl.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			_ = WriteFrame(conn, FrameStderr, []byte("yolo-journal: journalctl not found on host\n"))
			_ = WriteExit(conn, 127)
			return
		}
		_ = WriteFrame(conn, FrameStderr, []byte("yolo-journal: spawn failed: "+err.Error()+"\n"))
		_ = WriteExit(conn, 1)
		return
	}

	var sendMu sync.Mutex
	var wg sync.WaitGroup
	pump := func(r io.Reader, stream byte) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				sendMu.Lock()
				werr := WriteFrame(conn, stream, buf[:n])
				sendMu.Unlock()
				if werr != nil {
					// Client went away — SIGTERM (Python proc.terminate()),
					// NOT SIGKILL, so journalctl can flush/exit cleanly.
					_ = cmd.Process.Signal(syscall.SIGTERM)
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}
	wg.Add(2)
	go pump(stdout, FrameStdout)
	go pump(stderr, FrameStderr)

	// Drain the pumps to EOF BEFORE Wait: cmd.Wait closes the pipes after the
	// child exits, discarding kernel-buffered data. The pumps get EOF when
	// journalctl exits and closes its ends, so waiting on them first ensures
	// no data is lost.
	wg.Wait()
	rc := 0
	if werr := cmd.Wait(); werr != nil {
		rc = exitCode(werr)
	}
	sendMu.Lock()
	_ = WriteExit(conn, rc)
	sendMu.Unlock()
}

// exitCode extracts the process exit code, mapping a signal death to -N (the
// signed value Python's proc.wait() returns and packs into the exit frame),
// NOT the -1 that exec.ExitError.ExitCode() returns for any signal.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return -int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// readHeaderCapped reads bytes until '\n' or the cap. Returns (header-without-
// newline, foundNewline). A cap hit without a newline returns foundNewline=false
// (the caller frames the malformed-request error), mirroring the Python daemon
// stopping accumulation at JOURNAL_MAX_HEADER.
func readHeaderCapped(conn io.Reader, cap int) ([]byte, bool) {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	for len(buf) < cap {
		n, err := conn.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				return buf, true
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			return buf, false
		}
	}
	return buf, false // hit the cap with no newline
}

// logging plumbing (audit trail; the daemon supervisor also captures stderr).
var auditLog *log.Logger

func setupLog(path string) {
	out := os.Stderr
	if path != "" {
		if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644); err == nil {
			out = f
		}
	}
	auditLog = log.New(out, "", log.LstdFlags)
}

func logf(format string, args ...any) {
	if auditLog != nil {
		auditLog.Printf(format, args...)
	}
}
