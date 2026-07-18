// Command yolo-journald is the Go port of the builtin journal-bridge daemon
// (go-port plan Stage 7, Commit B). It listens on a Unix socket, reads a
// newline-terminated JSON request, validates the journalctl args, execs
// journalctl, and streams stdout/stderr/exit back as ">BI" frames with stream
// IDs 1/2/3 (DELIBERATELY distinct from the loophole protocol's 0/1/2).
//
// Frozen: socket chmod 0777, the arg validation + "user"-mode --user prepend,
// the journalctl-not-found (127) / spawn-failure (1) exit codes.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
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
	flag.Parse()
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-journald: --socket is required")
		os.Exit(2)
	}

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

	// Read up to the first newline (the JSON request header). Mirrors the
	// Python recv-until-newline; extra bytes after the newline are ignored.
	reader := bufio.NewReader(conn)
	header, err := reader.ReadBytes('\n')
	if len(header) == 0 && err != nil {
		return
	}
	if !containsNewline(header) {
		_ = journald.WriteFrame(conn, journald.FrameStderr, []byte("yolo-journal: malformed request\n"))
		_ = journald.WriteExit(conn, 2)
		return
	}
	// Strip the trailing newline before JSON decode.
	if n := len(header); n > 0 && header[n-1] == '\n' {
		header = header[:n-1]
	}

	v := journald.ParseRequest(header, mode)
	if v.ErrText != "" {
		_ = journald.WriteFrame(conn, journald.FrameStderr, []byte(v.ErrText))
		_ = journald.WriteExit(conn, v.ExitCode)
		return
	}

	cmd := exec.Command("journalctl", v.Args...)
	cmd.Stdin = nil
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
					_ = cmd.Process.Kill()
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

	rc := 0
	if werr := cmd.Wait(); werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			rc = ee.ExitCode()
		} else {
			rc = 1
		}
	}
	wg.Wait()
	sendMu.Lock()
	_ = journald.WriteExit(conn, rc)
	sendMu.Unlock()
}

func containsNewline(b []byte) bool {
	for _, c := range b {
		if c == '\n' {
			return true
		}
	}
	return false
}
