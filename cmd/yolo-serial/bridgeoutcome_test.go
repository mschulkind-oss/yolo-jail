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
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// liveBridgeFront runs the serial loophole's own handler behind a real front, the way a
// launch runs it (`publishes: "socket"`), granting only devices that match nothing on this
// machine. It returns the published endpoint file.
func liveBridgeFront(t *testing.T) string {
	t.Helper()
	return frontFor(t, serialdaemon.BuildHandler(grantedSettings(t, "/dev/yolo-serial-test-none-*")))
}

// grantedSettings is the bridge's config from a grant file, as a launch writes it, whose `devices`
// grant is globs: the grant file is the only allowlist the bridge has.
func grantedSettings(t *testing.T, globs ...string) serialdaemon.Settings {
	t.Helper()
	data, err := loopholegrant.Merge([]byte(`{"default_baud": 115200}`), loopholegrant.Block{
		Pack: "serial", Workspace: "/home/u/proj", PID: os.Getpid(),
		Kinds: map[string]loopholegrant.Kind{"devices": {Standing: loopholegrant.Granted, Values: globs}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return serialdaemon.LoadSettings(path)
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
	if rc != 64 || !strings.Contains(stderr, "does not cover") || !strings.Contains(stderr, "/dev/ttyUSB0") {
		t.Errorf("`read` of a device outside the grant: rc = %d, want the bridge's 64 naming it; stderr = %q", rc, stderr)
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
// says why and exits 64 (a device outside the grant); `pty` printed the reason and exited 0.
func TestPtyEndsWithTheBridgesExitCode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty allocation is Linux-only (openPty); the dial is never reached elsewhere")
	}
	endpoint := liveBridgeFront(t)
	var rc int
	_, stderr := captureStdio(t, func() {
		rc = run([]string{"pty", "--endpoint", endpoint, "/dev/ttyUSB0"})
	})
	if rc != 64 {
		t.Errorf("rc = %d, want the bridge's 64 for a device outside the grant; stderr = %q", rc, stderr)
	}
	if !strings.Contains(stderr, "does not cover") {
		t.Errorf("stderr does not carry the bridge's reason:\n%s", stderr)
	}
}
