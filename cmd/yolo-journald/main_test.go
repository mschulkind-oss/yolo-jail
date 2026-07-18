package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
)

// TestNoTruncationRace is the regression for the confirmed Wait/pipe race: the
// daemon must deliver journalctl's FULL output even when it bursts a large
// buffer and exits immediately. Runs the real binary + a fake journalctl
// emitting a fixed large payload, N iterations; every run must deliver all
// bytes (before the fix, ~1/3 of runs truncated).
func TestNoTruncationRace(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes; -short")
	}
	const payloadLen = 500_000
	dir := t.TempDir()
	// fake journalctl: write payloadLen bytes of 'x' to stdout, then exit 0.
	fakeBin := filepath.Join(dir, "journalctl")
	writeFakeJournalctl(t, fakeBin, payloadLen)

	binPath := buildJournald(t, dir)

	for i := 0; i < 15; i++ {
		sock := filepath.Join(dir, "j.sock")
		os.Remove(sock)
		proc := startDaemon(t, binPath, sock, dir)
		out, rc := driveJournal(t, sock, `{"args":["-n","5"]}`)
		stopDaemon(proc)
		if rc != 0 {
			t.Fatalf("run %d: rc=%d, want 0", i, rc)
		}
		if len(out) != payloadLen {
			t.Fatalf("run %d: got %d bytes, want %d (truncation race)", i, len(out), payloadLen)
		}
	}
}

// TestHeaderCapRejectsNewlinelessFlood: a client that never sends a newline is
// rejected (exit 2) instead of growing daemon memory unbounded.
func TestHeaderCapRejectsNewlineless(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes; -short")
	}
	dir := t.TempDir()
	writeFakeJournalctl(t, filepath.Join(dir, "journalctl"), 10)
	binPath := buildJournald(t, dir)
	sock := filepath.Join(dir, "j.sock")
	proc := startDaemon(t, binPath, sock, dir)
	defer stopDaemon(proc)

	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	// Send > MaxHeaderBytes with no newline.
	flood := make([]byte, journald.MaxHeaderBytes+100)
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

func buildJournald(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "yolo-journald.bin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func startDaemon(t *testing.T, bin, sock, fakePathDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, "--socket", sock, "--mode", "user")
	// Put the fake journalctl first on PATH.
	cmd.Env = append(os.Environ(), "PATH="+fakePathDir+":"+os.Getenv("PATH"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for the socket.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
			c.Close()
			return cmd
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon socket never appeared")
	return nil
}

func stopDaemon(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

func driveJournal(t *testing.T, sock, request string) ([]byte, int) {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	c.Write([]byte(request + "\n"))
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
		case journald.FrameStdout:
			stdout = append(stdout, payload...)
		case journald.FrameExit:
			return stdout, int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}
