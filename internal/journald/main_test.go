package journald

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// tailMarker is journalctl's final output — the last bytes of the stream. The
// truncation race drops the *tail* of the pipe (kernel-buffered data discarded
// when cmd.Wait closes the read end early), so a missing tail marker is a
// precise, deterministic truncation signal.
const tailMarker = "===END-OF-JOURNAL-MARKER===\n"

// TestNoTruncationRace is the regression for the confirmed Wait/pipe race: the
// daemon must deliver journalctl's FULL output — including the final bytes —
// even against a client that pauses before reading. It reproduces the race
// deterministically rather than probabilistically:
//
//   - Payload size is pinned just ABOVE the AF_UNIX send-buffer threshold
//     (measured at runtime), so once the daemon's stdout pump has filled the
//     socket it blocks in WriteFrame, and the payload's tail is left sitting
//     UNREAD in the kernel pipe. The size stays comfortably below
//     send-buffer + pipe capacity so the fake journalctl can finish writing and
//     EXIT without the client reading a single byte.
//   - The client sends its request, then SLEEPS before reading. During the
//     sleep journalctl exits. With the race (cmd.Wait first) cmd.Wait closes the
//     pipe read end while the tail is still buffered, discarding it; the client
//     later reads a short stream with the marker gone. With the fix (wg.Wait
//     first) the daemon blocks until the pumps drain the pipe to EOF — which can
//     only happen after the client resumes reading — so the full payload +
//     marker always arrive.
//
// This made the test bite: with the drain-wait moved back after cmd.Wait it
// fails every run (short byte count, missing marker); with the fix it passes.
//
// Driven in-process against journald.Serve (was: a built-and-exec'd binary) —
// the Wait/pipe race lives entirely inside handleConn, independent of the
// process boundary.
func TestNoTruncationRace(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes; -short")
	}
	// Size the body just past the point where the daemon's socket write blocks,
	// so the tail is guaranteed to still be in the pipe when journalctl exits,
	// yet the whole payload fits in socket+pipe so journalctl can exit unaided.
	sndThreshold := measureUnixSendBuffer(t)
	bodyLen := sndThreshold + 24*1024
	wantLen := bodyLen + len(tailMarker)
	dir := t.TempDir()
	// fake journalctl: write bodyLen bytes of 'x' then the tail marker, exit 0.
	fakeBin := filepath.Join(dir, "journalctl")
	writeFakeJournalctlTailed(t, fakeBin, bodyLen, tailMarker)
	// Put the fake journalctl first on PATH — handleConn resolves "journalctl"
	// against this process's env when it spawns the child.
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	for i := 0; i < 5; i++ {
		sock := filepath.Join(dir, "j.sock")
		os.Remove(sock)
		stop, done := startServe(t, sock, "user")
		// Delay the first read so journalctl exits (and, with the race, cmd.Wait
		// closes the pipe) before the client drains a single byte.
		out, rc := driveJournalDelayedRead(t, sock, `{"args":["-n","5"]}`, 200*time.Millisecond)
		stopServe(stop, done)
		if rc != 0 {
			t.Fatalf("run %d: rc=%d, want 0", i, rc)
		}
		if !bytes.HasSuffix(out, []byte(tailMarker)) {
			t.Fatalf("run %d: tail marker missing — got %d bytes, want %d (truncation race dropped the pipe tail)", i, len(out), wantLen)
		}
		if len(out) != wantLen {
			t.Fatalf("run %d: got %d bytes, want %d (truncation race)", i, len(out), wantLen)
		}
	}
}

// measureUnixSendBuffer returns how many bytes a writer can push into an AF_UNIX
// stream socket before the write blocks, when the peer never reads. This mirrors
// the daemon's stdout pump blocking on a full socket, and lets the test size its
// payload relative to the running kernel's buffer rather than a magic constant.
func measureUnixSendBuffer(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "probe.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dialed := make(chan net.Conn, 1)
	go func() {
		c, derr := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
		if derr != nil {
			dialed <- nil
			return
		}
		dialed <- c // hold it open, never read
	}()

	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer := <-dialed
	if peer == nil {
		t.Fatal("probe dial failed")
	}
	defer peer.Close()

	conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 4096)
	total := 0
	for {
		n, werr := conn.Write(buf)
		total += n
		if werr != nil {
			break
		}
	}
	if total <= 0 {
		t.Fatalf("measured send buffer = %d, want > 0", total)
	}
	return total
}

// TestHeaderCapRejectsNewlineless: a client that never sends a newline is
// rejected (exit 2) instead of growing daemon memory unbounded.
func TestHeaderCapRejectsNewlineless(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes; -short")
	}
	dir := t.TempDir()
	writeFakeJournalctl(t, filepath.Join(dir, "journalctl"), 10)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	sock := filepath.Join(dir, "j.sock")
	stop, done := startServe(t, sock, "user")
	defer stopServe(stop, done)

	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	// Send > MaxHeaderBytes with no newline.
	flood := make([]byte, MaxHeaderBytes+100)
	for i := range flood {
		flood[i] = 'x'
	}
	c.Write(flood)
	// Expect a malformed-request stderr frame + exit 2, not a hang.
	_, rc := readFrames(t, c)
	if rc != 2 {
		t.Errorf("newlineless flood rc=%d, want 2", rc)
	}
}

// --- helpers ---

func writeFakeJournalctl(t *testing.T, path string, n int) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"python3 -c \"import sys; sys.stdout.buffer.write(b'x'*" + strconv.Itoa(n) + ")\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeFakeJournalctlTailed writes n bytes of 'x' followed by tail, then exits.
// The tail marker lands at the very end of the pipe stream, so it is the first
// thing dropped by the Wait/pipe truncation race. The tail is base64-encoded so
// it survives the shell/python quoting unchanged (no embedded quotes/newlines).
func writeFakeJournalctlTailed(t *testing.T, path string, n int, tail string) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString([]byte(tail))
	script := "#!/bin/sh\n" +
		"python3 -c \"import sys,base64; sys.stdout.buffer.write(b'x'*" + strconv.Itoa(n) +
		"); sys.stdout.buffer.write(base64.b64decode('" + b64 + "')); sys.stdout.buffer.flush()\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// startServe launches journald.Serve in a goroutine on sock and blocks until the
// socket is dialable. It returns the stop channel and a done channel that closes
// when Serve returns; pass both to stopServe.
func startServe(t *testing.T, sock, mode string) (chan struct{}, chan struct{}) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := Serve(sock, mode, stop); err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
			c.Close()
			return stop, done
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	t.Fatal("daemon socket never appeared")
	return nil, nil
}

// stopServe closes the listener and waits for Serve to return.
func stopServe(stop, done chan struct{}) {
	close(stop)
	<-done
}

// driveJournalDelayedRead sends the request, then sleeps `preReadDelay` before
// reading the first reply byte. The pause lets journalctl finish and exit while
// the client is idle: the daemon's stdout pump fills the socket send buffer and
// blocks with the payload's tail still unread in the pipe. With the Wait/pipe
// race, cmd.Wait then closes the pipe and drops that tail before the client ever
// resumes; with the fix the daemon waits for the pump to drain it first.
func driveJournalDelayedRead(t *testing.T, sock, request string, preReadDelay time.Duration) ([]byte, int) {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(request + "\n"))
	time.Sleep(preReadDelay)
	c.SetDeadline(time.Now().Add(30 * time.Second))
	return readFrames(t, c)
}

// readFrames reads journal frames, returning accumulated stdout + the exit rc.
func readFrames(t *testing.T, c net.Conn) ([]byte, int) {
	t.Helper()
	var stdout []byte
	for {
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return stdout, -999 // EOF before exit
		}
		stream := hdr[0]
		length := binary.BigEndian.Uint32(hdr[1:])
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(c, payload); err != nil {
				return stdout, -999
			}
		}
		switch stream {
		case FrameStdout:
			stdout = append(stdout, payload...)
		case FrameExit:
			return stdout, int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}
