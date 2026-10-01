package main

// attribution_test.go pins what yolo-serial says when it cannot use the serial bridge,
// through run(), the way a user in the jail meets it.
//
// Each failure used to stop at the fault: `endpoint file <path> is malformed.`, `no
// endpoint published at <path>.`, `serial daemon rejected this jail's token.`, and for a
// bridge that died behind yolo's front, NOTHING and exit 0. yolo-ps, the sibling client
// of the same transport, has named the next step for each of these for a long time, so
// these tests hold yolo-serial to it (docs/reference/happy-path-principle.md, rule 1):
// every message names the endpoint that failed and says to relaunch the jail, and none
// of them quotes the endpoint file, which carries this jail's bearer token.

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// captureStdio swaps os.Stdout and os.Stderr for pipes while fn runs and returns what
// was written to each. The client writes to the real fds (that is its contract with the
// person in the jail), so the honest way to assert on a message is to read them.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	read := func(f **os.File) func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		prev := *f
		*f = w
		done := make(chan string, 1)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			*f = prev
			_ = w.Close()
			out := <-done
			_ = r.Close()
			return out
		}
	}
	endOut, endErr := read(&os.Stdout), read(&os.Stderr)
	fn()
	return endOut(), endErr()
}

// privateDir is a 0700 scratch dir. os.MkdirTemp, not t.TempDir(): svcendpoint refuses
// to publish a credential into the 0755 directory t.TempDir() creates.
func privateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-serial-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// deadBridgeFront publishes a REAL front (svcendpoint.ServeFront) whose upstream socket
// is never bound: the state a crashed or killed host bridge leaves behind while the
// launch keeps yolo's front up. The serial loophole is `publishes: "socket"`, so this,
// and not a dial failure, is what a dead bridge looks like from the jail.
func deadBridgeFront(t *testing.T) string {
	t.Helper()
	dir := privateDir(t)
	endpoint := filepath.Join(dir, "serial.endpoint")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", filepath.Join(dir, "never-bound.sock"), stop)
	}()
	t.Cleanup(func() { close(stop); <-done })
	deadline := time.Now().Add(10 * time.Second)
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("the front never published a usable endpoint file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return endpoint
}

// assertNoToken fails if stderr carries any long field of a published endpoint file.
func assertNoToken(t *testing.T, endpoint, stderr string) {
	t.Helper()
	published, err := os.ReadFile(endpoint)
	if err != nil {
		return // nothing published, so nothing to leak
	}
	for _, field := range strings.Fields(string(published)) {
		if len(field) >= 16 && strings.Contains(stderr, field) {
			t.Fatalf("the diagnostic echoed %d bytes of the endpoint file, which carries "+
				"this jail's bearer token", len(field))
		}
	}
}

func TestEveryBridgeFailureNamesTheNextStep(t *testing.T) {
	cases := []struct {
		name     string
		endpoint func(t *testing.T) string
		// also is what the message must carry beyond the endpoint and the relaunch.
		also string
	}{
		{"no endpoint file", func(t *testing.T) string {
			return filepath.Join(privateDir(t), "serial.endpoint")
		}, "no endpoint published"},
		{"malformed endpoint file", func(t *testing.T) string {
			p := filepath.Join(privateDir(t), "serial.endpoint")
			// Two fields: an older publication, or a truncated one.
			if err := os.WriteFile(p, []byte("127.0.0.1:1 c2VyaWFs\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}, "malformed"},
		{"token rejected", func(t *testing.T) string {
			ep, err := svcendpoint.Read(deadBridgeFront(t))
			if err != nil {
				t.Fatal(err)
			}
			ep.Token = strings.Repeat("0", 64)
			stale := filepath.Join(privateDir(t), "serial.endpoint")
			if err := svcendpoint.Publish(stale, ep); err != nil {
				t.Fatal(err)
			}
			return stale
		}, "rejected this jail's token"},
		{"bridge dead behind a live front", deadBridgeFront,
			"host-service-serial.log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint(t)
			var rc int
			_, stderr := captureStdio(t, func() {
				rc = run([]string{"list", "--endpoint", endpoint})
			})
			if rc == 0 {
				t.Errorf("rc = 0: the request failed and the exit said it worked; stderr = %q", stderr)
			}
			for _, want := range []string{endpoint, "relaunch the jail", tc.also} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr is missing %q:\n%s", want, stderr)
				}
			}
			assertNoToken(t, endpoint, stderr)
		})
	}
}

// TestPtyNamesTheSameNextStep: `pty` dials on its own path rather than through call(),
// and it printed the bare dial error (`dial daemon failed: …`). It must attribute the
// fault the way every other subcommand does.
func TestPtyNamesTheSameNextStep(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pty allocation is Linux-only (openPty); the dial is never reached elsewhere")
	}
	endpoint := filepath.Join(privateDir(t), "serial.endpoint")
	var rc int
	_, stderr := captureStdio(t, func() {
		// Flags BEFORE the device: the flag package stops at the first positional.
		rc = run([]string{"pty", "--endpoint", endpoint, "/dev/ttyUSB0"})
	})
	if rc == 0 {
		t.Errorf("rc = 0 with no endpoint; stderr = %q", stderr)
	}
	for _, want := range []string{endpoint, "no endpoint published", "relaunch the jail"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
}
