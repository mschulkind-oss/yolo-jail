package main

// bridgeoutcome_test.go pins how yolo-serial ENDS against a bridge that answered, and how
// `pty` ends against one that did not.
//
// call() now reads a stream that ends without the bridge's exit frame as a dead bridge
// (attribution_test.go). That is only right if a healthy bridge always sends one, so the
// first test runs the REAL serial handler behind a real front and checks that `list` exits
// 0 and a refused device exits with the bridge's own code.
//
// `pty` reads the same stream in its own loop, and that loop read every end of it as
// success: a dead bridge printed the "Bridge Active" banner and exited 0 with nothing
// else, and a device the bridge refused printed the refusal and exited 0 as well
// (docs/reference/happy-path-principle.md, rules 1 and 5).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// liveBridgeFront runs the serial loophole's own handler behind a real front, the way a
// launch runs it (`publishes: "socket"`), allowing only devices that match nothing on this
// machine. It returns the published endpoint file.
func liveBridgeFront(t *testing.T) string {
	t.Helper()
	cfg := serialdaemon.Settings{
		AllowedDevices: []string{"/dev/yolo-serial-test-none-*"},
		DefaultBaud:    115200,
	}
	return frontFor(t, serialdaemon.BuildHandler(cfg))
}

// frontFor runs handler as the bridge behind a real front and returns the published endpoint
// file.
func frontFor(t *testing.T, handler hostservice.Handler) string {
	t.Helper()
	dir := privateDir(t)
	sock := filepath.Join(dir, "bridge.sock")
	stop := make(chan struct{})
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = hostservice.ServeFrontedUnix(handler, sock, stop)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bridge never bound its socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	endpoint := filepath.Join(dir, "serial.endpoint")
	frontStop := make(chan struct{})
	fronted := make(chan struct{})
	go func() {
		defer close(fronted)
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, frontStop)
	}()
	t.Cleanup(func() { close(frontStop); <-fronted; close(stop); <-served })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("the front never published a usable endpoint file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return endpoint
}

// TestALiveBridgeEndsWithItsExitCode: the healthy half of call()'s new rule. The bridge
// ends every session with an exit frame, so `list` exits 0 with its output, and a device
// outside the allowlist exits with the bridge's code and its words, not as a dead bridge.
func TestALiveBridgeEndsWithItsExitCode(t *testing.T) {
	endpoint := liveBridgeFront(t)

	var rc int
	stdout, stderr := captureStdio(t, func() {
		rc = run([]string{"list", "--endpoint", endpoint})
	})
	if rc != 0 || !strings.Contains(stdout, "No serial devices found") {
		t.Errorf("`list` against a live bridge: rc = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}

	_, stderr = captureStdio(t, func() {
		rc = run([]string{"read", "--endpoint", endpoint, "/dev/ttyUSB0"})
	})
	if rc != 2 || !strings.Contains(stderr, "not in the allowed_devices list") {
		t.Errorf("`read` of a refused device: rc = %d, want the bridge's 2; stderr = %q", rc, stderr)
	}
	if strings.Contains(stderr, "relaunch the jail") {
		t.Errorf("a bridge that answered was reported as dead:\n%s", stderr)
	}
}

// TestPtyReportsADeadBridge: a bridge dead behind a live front, met by `pty`. The dial and
// the request write both succeed, because the front answers them, and the stream then ends
// with no exit frame. That is the fault call() names, and `pty` must name it too.
func TestPtyReportsADeadBridge(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty allocation is Linux-only (openPty); the dial is never reached elsewhere")
	}
	endpoint := deadBridgeFront(t)
	var rc int
	_, stderr := captureStdio(t, func() {
		rc = run([]string{"pty", "--endpoint", endpoint, "/dev/ttyUSB0"})
	})
	if rc == 0 {
		t.Errorf("rc = 0: the bridge never answered and the exit said it worked; stderr = %q", stderr)
	}
	for _, want := range []string{endpoint, "relaunch the jail", "host-service-serial.log"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	assertNoToken(t, endpoint, stderr)
}

// TestPtyEndsWithTheBridgesExitCode: a device the bridge refuses, met by `pty`. The bridge
// says why and exits 2; `pty` printed the reason and exited 0.
func TestPtyEndsWithTheBridgesExitCode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty allocation is Linux-only (openPty); the dial is never reached elsewhere")
	}
	endpoint := liveBridgeFront(t)
	var rc int
	_, stderr := captureStdio(t, func() {
		rc = run([]string{"pty", "--endpoint", endpoint, "/dev/ttyUSB0"})
	})
	if rc != 2 {
		t.Errorf("rc = %d, want the bridge's 2 for a refused device; stderr = %q", rc, stderr)
	}
	if !strings.Contains(stderr, "not in the allowed_devices list") {
		t.Errorf("stderr does not carry the bridge's reason:\n%s", stderr)
	}
}
