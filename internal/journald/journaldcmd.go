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

// Main is the journal-bridge daemon entry point. It accepts connections, reads a
// newline-terminated JSON request, validates the journalctl args, execs
// journalctl, and streams stdout/stderr/exit back as ">BI" frames with stream IDs
// 1/2/3.
//
// CLI contract: --socket (the AF_UNIX socket yolo's own front dials) plus
// --settings and --log-file.
//
// # ONE TRANSPORT NOW, and both of the flags it replaced REFUSE rather than fall back
//
// This daemon used to publish its own loopback-TLS endpoint (--endpoint) and take
// its mode from an argv yolo computed (--mode), because it was a BUILTIN service the
// run pipeline started by hand off a top-level `journal` config key. It is an
// ordinary manifest loophole now, shipped by the official `journal` pack, and a
// pack-shipped loophole must declare `publishes: "socket"`
// (internal/loopholedecl/packshipped.go): it binds a plain socket and yolo runs the
// TLS front over it, so the endpoint file's mode, its key persistence, its
// constant-time token compare and its length cap are the framework's code rather
// than this daemon's.
//
// Both retired flags REFUSE, and neither falls back, for hostprocesses' `--config`
// reason: a silent fallback would be wrong in the widening direction. `--mode full`
// ignored in favour of a settings file would silently DROP the escalation somebody
// asked for; `--endpoint` honoured would publish a bearer-token regular FILE at the
// path the front expects to find a socket at, which fails the run pipeline's
// socket-connectable readiness probe with no diagnosis.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-journald", flag.ExitOnError)
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	settings := fs.String("settings", "", "Resolved settings file written by yolo (see --help)")
	retiredEndpoint := fs.String("endpoint", "", "RETIRED — use --socket")
	retiredMode := fs.String("mode", "", "RETIRED — use --settings")
	logFile := fs.String("log-file", "", "append per-request audit log here (default: stderr)")
	_ = fs.Parse(argv)

	if *retiredMode != "" {
		fmt.Fprintln(os.Stderr, "yolo-journald: --mode is retired — the mode is no longer an "+
			"argv yolo computes from a top-level `journal` config key. Pass --settings <file>, "+
			"the resolved settings file yolo writes from loopholes.journal.settings; the "+
			"manifest's host_daemon.cmd names it with the {settings} token, and the escalation "+
			"that was --mode full is now {\"full\": true} in there (user config only).")
		return 2
	}
	if *retiredEndpoint != "" {
		fmt.Fprintln(os.Stderr, "yolo-journald: --endpoint is retired — this daemon no longer "+
			"publishes its own loopback-TLS endpoint. Bind a plain AF_UNIX socket with --socket "+
			"and let yolo run the front over it (the manifest declares "+
			"host_daemon.publishes = \"socket\"); the jail still reads "+
			"/run/yolo-services/journal.endpoint and nothing jail-facing moved.")
		return 2
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-journald: --socket is required")
		return 2
	}
	setupLog(*logFile)

	// READ ONCE, HERE, before a single connection is accepted — the freeze
	// internal/hostprocesses states at length. Everything downstream holds a mode,
	// not a path.
	mode := LoadSettings(*settings)

	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; close(stop) }()

	if err := ServeFrontedUnix(*socket, mode, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-journald:", err)
		return 1
	}
	return 0
}

// ServeFrontedUnix binds the AF_UNIX socket ONLY YOLO'S OWN FRONT DIALS and serves
// it until stop is closed — the `publishes: "socket"` shape, the same one
// hostservice.ServeFrontedUnix implements for the frame-protocol daemons.
//
// The socket is host-only (yolo binds it outside the jail's :rw-mounted services
// dir) and the jail reaches it through svcendpoint's TLS front, so authentication,
// the pinned certificate and the per-jail bearer token are all the framework's.
//
// THE PREAMBLE IS CONSUMED HERE, and forgetting it is not a subtle failure: yolo
// prepends its connection preamble to every spliced connection (a manifest's
// `preamble` defaults ON), so a daemon that did not read the frame would hand those
// bytes to readHeaderCapped and answer every request with "malformed request".
// This daemon reads it and DISCARDS the value — it keeps its own per-request audit
// line and asserts no jail identity of its own — but the frame still has to leave
// the stream.
func ServeFrontedUnix(socket, mode string, stop <-chan struct{}) error {
	return serveUnixConns(socket, stop, func(conn net.Conn) {
		// INSIDE THE PER-CONNECTION GOROUTINE, NEVER IN THE ACCEPT LOOP: reading in
		// the loop would let one client that connects and never writes stall every
		// other client for the handshake timeout — the denial of service
		// svcendpoint's own accept loop is structured to avoid.
		//
		// AND ON THE RAW conn, never through a bufio.Reader: readHeaderCapped reads
		// the request one byte at a time, so a buffered read here would swallow the
		// head of the client's header into a buffer handleConn never sees.
		// ReadPreamble is io.ReadFull-based and consumes exactly the frame, which is
		// what makes the raw read safe.
		if _, perr := svcendpoint.ReadPreamble(conn); perr != nil {
			// Drop the connection; never re-read these bytes as a request. yolo's own
			// readiness probe is a bare connect-and-close on this socket
			// (socketConnectable, internal/cli/run/loopholesruntime.go), so it lands
			// here on every launch and must not be louder than it is. Payload-free by
			// classification.
			logf("[journal] connection preamble rejected; connection dropped")
			_ = conn.Close()
			return
		}
		handleConn(conn, mode)
	})
}

// serveUnixConns binds the Unix socket, accepts connections, and runs onConn in its
// own goroutine for each until stop is closed (or the listener fails). The socket is
// chmod 0777 (frozen) so any jail UID can dial it. Callers wire their own signal
// handling to close stop.
//
// UNEXPORTED, and the one exported socket entry point above it is the fronted one.
// A bare `Serve` used to sit here and it now has no caller: the endpoint half went
// with the pack conversion, and there is no host-to-host client of this bridge —
// leaving a preamble-less exported entry point beside a preamble-bearing one is how
// a future spawn picks the wrong door and answers every request with "malformed
// request". The accept loop is still shared, and the truncation-race harness in
// main_test.go drives it directly with handleConn, which is where that race lives.
func serveUnixConns(socket string, stop <-chan struct{}, onConn func(net.Conn)) error {
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
		go onConn(conn)
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
