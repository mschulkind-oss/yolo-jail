// Command yolo-journald is the builtin journal-bridge daemon. It listens on a
// Unix socket, reads a newline-terminated JSON request, validates the journalctl
// args, execs journalctl, and streams stdout/stderr/exit back as ">BI" frames
// with stream IDs 1/2/3 (DELIBERATELY distinct from the loophole protocol's
// 0/1/2).
//
// Frozen: socket chmod 0777, the arg validation + "user"-mode --user prepend,
// the journalctl-not-found (127) / spawn-failure (1) exit codes.
package main

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

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
)

func main() {
	socket := flag.String("socket", "", "Unix socket to bind")
	mode := flag.String("mode", "user", `"user" or "full"`)
	logFile := flag.String("log-file", "", "append per-request audit log here (default: stderr)")
	flag.Parse()
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-journald: --socket is required")
		os.Exit(2)
	}
	setupLog(*logFile)

	_ = os.Remove(*socket)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: *socket, Net: "unix"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo-journald:", err)
		os.Exit(1)
	}
	ln.SetUnlinkOnClose(false)
	_ = os.Chmod(*socket, 0o777)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; ln.Close(); os.Remove(*socket) }()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			break
		}
		go handleConn(conn, *mode)
	}
	os.Remove(*socket)
}

func handleConn(conn *net.UnixConn, mode string) {
	defer conn.Close()

	// Read the JSON request header up to the first newline, capped at the
	// Python daemon's JOURNAL_MAX_HEADER (16384 bytes) — a newline-less or
	// over-cap client must be rejected, not allowed to grow daemon memory.
	header, foundNL := readHeaderCapped(conn, journald.MaxHeaderBytes)
	if len(header) == 0 && !foundNL {
		return
	}
	if !foundNL {
		_ = journald.WriteFrame(conn, journald.FrameStderr, []byte("yolo-journal: malformed request\n"))
		_ = journald.WriteExit(conn, 2)
		return
	}

	v := journald.ParseRequest(header, mode)
	if v.ErrText != "" {
		_ = journald.WriteFrame(conn, journald.FrameStderr, []byte(v.ErrText))
		_ = journald.WriteExit(conn, v.ExitCode)
		return
	}

	// Per-request audit log (module map freezes this): "[journal] mode=.. args=..".
	logf("[journal] mode=%s args=%s", mode, journald.ArgsJSON(v.Args))

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
			_ = journald.WriteFrame(conn, journald.FrameStderr, []byte("yolo-journal: journalctl not found on host\n"))
			_ = journald.WriteExit(conn, 127)
			return
		}
		_ = journald.WriteFrame(conn, journald.FrameStderr, []byte("yolo-journal: spawn failed: "+err.Error()+"\n"))
		_ = journald.WriteExit(conn, 1)
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
				werr := journald.WriteFrame(conn, stream, buf[:n])
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
	go pump(stdout, journald.FrameStdout)
	go pump(stderr, journald.FrameStderr)

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
	_ = journald.WriteExit(conn, rc)
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
func readHeaderCapped(conn *net.UnixConn, cap int) ([]byte, bool) {
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
