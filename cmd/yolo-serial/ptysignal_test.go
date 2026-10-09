package main

// ptysignal_test.go pins that `yolo-serial pty` ENDS when it is sent SIGTERM while bridging. The
// macos-user integration test runs exactly that in the guest (`yolo-serial pty <slave> --link
// "$HOME/vpty" & p=$!; sleep 5; kill "$p"; wait "$p"`), and a pty that survives the kill hangs the
// whole launch at the `wait`: it did, for 30 minutes, on the macos-user CI runner.
//
// THE HANG: the bridge's stdout frames are written to the virtual PTY's master, and nothing had
// opened the virtual slave. On darwin a write to a pty master blocks until the slave is opened
// (xnu ptcwrite sleeps while the slave is not TS_ISOPEN), so the bridge's first frame — its
// "--- Connected to serial bridge" line — parked the main goroutine in write(2) on a BLOCKING
// descriptor. SIGTERM's handler closed the connection and the master, but Go cannot cancel I/O on
// a descriptor it does not poll, and the handler is installed with SA_RESTART, so the write
// resumed and the process never returned. Linux buffers a master write while the slave is closed,
// so the same hang needs more bytes than the line's buffer holds; this test sends that many.

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
)

// ptyHelperEnv makes this test binary run `yolo-serial` itself, so the signal reaches a process
// of its own, as it does in the guest.
const ptyHelperEnv = "YOLO_SERIAL_TEST_RUN_MAIN"

func TestPtyHelperProcess(t *testing.T) {
	if os.Getenv(ptyHelperEnv) != "1" {
		t.Skip("helper process for the pty SIGTERM tests")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(run(args))
}

// startPty runs `yolo-serial pty device` against endpoint as a process of its own, waits for
// its banner, and returns it with a channel that receives its exit and a reader of its output.
func startPty(t *testing.T, endpoint, device string) (*exec.Cmd, <-chan error, func() string) {
	t.Helper()
	dir := privateDir(t)
	out := filepath.Join(dir, "pty.out")
	outFile, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outFile.Close() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestPtyHelperProcess$", "--",
		"pty", device, "--link", filepath.Join(dir, "vpty"), "--endpoint", endpoint)
	cmd.Env = append(os.Environ(), ptyHelperEnv+"=1")
	cmd.Stdout, cmd.Stderr = outFile, outFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	read := func() string { b, _ := os.ReadFile(out); return string(b) }
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(read(), "Virtual PTY:") {
		if time.Now().After(deadline) {
			t.Fatalf("`pty` never printed its banner:\n%s", read())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cmd, exited, read
}

// requireExitOnSIGTERM sends SIGTERM and fails unless the process ends, with status 0, soon.
func requireExitOnSIGTERM(t *testing.T, cmd *exec.Cmd, exited <-chan error, read func() string) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("`pty` ended with %v after SIGTERM, want exit 0:\n%s", err, read())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("`pty` was still running 10s after SIGTERM (no one opened its virtual slave):\n%s", read())
	}
}

// frontEndingSessions is frontFor, plus a cleanup that closes the bridge's side of every session
// before the front's stop waits for them. A streaming session outlives its client: the front
// does not hang up the bridge when the jail does, so the bridge's request read never ends.
func frontEndingSessions(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	var mu sync.Mutex
	var conns []net.Conn
	endpoint := frontFor(t, func(s *hostservice.Session) {
		mu.Lock()
		conns = append(conns, s.Conn())
		mu.Unlock()
		handler(s)
	})
	// Registered after frontFor's cleanup, so it runs first.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return endpoint
}

func requirePtyPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("openPty allocates a pair on linux and darwin only")
	}
}

// TestPtyExitsOnSIGTERMWhileTheBridgeFillsAnUnopenedPty: the bridge sends more than a pty line
// holds while no serial tool has opened the virtual slave, so the client's write to the master
// cannot complete. SIGTERM must still end it.
func TestPtyExitsOnSIGTERMWhileTheBridgeFillsAnUnopenedPty(t *testing.T) {
	requirePtyPlatform(t)
	flood := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB, far past any pty buffer
	endpoint := frontEndingSessions(t, func(s *hostservice.Session) {
		s.StdoutBytes(flood)
		for { // hold the session until the client hangs up, as the monitor arm does
			if _, err := frameproto.ReadFrame(s.Conn()); err != nil {
				return
			}
		}
	})
	cmd, exited, read := startPty(t, endpoint, "/dev/ttyUSB0")
	time.Sleep(time.Second) // let the client fill the line and block in its write
	requireExitOnSIGTERM(t, cmd, exited, read)
}

// TestPtyExitsOnSIGTERMWhileBridging is the guest's script against the REAL serial handler: the
// device is a pty pair this test opens, standing for a USB serial port as the integration test's
// host pair does, so the bridge's monitor arm opens and reads it.
func TestPtyExitsOnSIGTERMWhileBridging(t *testing.T) {
	requirePtyPlatform(t)
	hostMaster, hostSlave, err := openPty()
	if err != nil {
		t.Fatalf("openPty for the stand-in device: %v", err)
	}
	// Closed by cleanups registered BEFORE frontFor's, so they run after the bridge's session
	// ends: a defer would close the master first, and the bridge would spend its reconnect
	// budget on a device that vanished under it.
	t.Cleanup(func() { _ = hostMaster.Close() })
	// Hold the slave open, as the integration test does, so the line stays up.
	hold, err := os.OpenFile(hostSlave, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the stand-in slave %s: %v", hostSlave, err)
	}
	t.Cleanup(func() { _ = hold.Close() })
	endpoint := frontEndingSessions(t, serialdaemon.BuildHandler(serialdaemon.Settings{
		AllowedDevices: []string{hostSlave},
		DefaultBaud:    115200,
	}))
	cmd, exited, read := startPty(t, endpoint, hostSlave)
	time.Sleep(1500 * time.Millisecond) // the guest's `sleep 5`, shortened
	requireExitOnSIGTERM(t, cmd, exited, read)
}
