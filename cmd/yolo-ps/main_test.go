package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The request this client puts on the wire is now a pure statement of what was
// ASKED — a mode, and for pid mode a number. Everything below exists to pin the
// absence of the fourth field it used to carry, jail_id, because an absence has
// no other way to fail loudly: re-adding it would break no assertion that
// existed before this file, and the symptom in production would be an audit
// column quietly reverting to a value the client chose for itself.
//
// Byte-exact rather than key-wise on purpose. json.Marshal of a map emits keys
// in sorted order, so the encoding is deterministic and a whole-body comparison
// costs nothing while catching a field nobody thought to look for.

func TestRequestBodyCarriesTheModeAndNothingElse(t *testing.T) {
	cases := []struct {
		name   string
		pidSet bool
		pid    int
		tree   bool
		want   string
	}{
		{"list is the default", false, 0, false, `{"mode":"list"}`},
		{"tree", false, 0, true, `{"mode":"tree"}`},
		{"pid", true, 4242, false, `{"mode":"pid","pid":4242}`},
		// --pid 0 is a query about pid 0, not an absent flag: run() detects the
		// flag by presence (flag.Visit), so the zero value must survive here.
		{"pid 0 is a real pid", true, 0, false, `{"mode":"pid","pid":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(requestBody(buildRequest(tc.pidSet, tc.pid, tc.tree)))
			if got != tc.want {
				t.Errorf("request body = %s, want %s", got, tc.want)
			}
			if strings.Contains(got, "jail_id") {
				t.Errorf("the client named its own jail: %s\n"+
					"The host asserts the jail's identity in the connection preamble "+
					"(internal/svcendpoint/preamble.go); a client-supplied jail_id is "+
					"overridden there and must not be sent.", got)
			}
		})
	}
}

// TestPidBeatsTree pins the priority order, which is only observable when both
// selectors are set at once.
func TestPidBeatsTree(t *testing.T) {
	got := string(requestBody(buildRequest(true, 7, true)))
	if got != `{"mode":"pid","pid":7}` {
		t.Errorf("--pid --tree together = %s, want the pid query", got)
	}
}

// TestRequestReadsNoIdentityFromTheEnvironment. The two variables that used to
// feed jail_id are set here to values a leak would spell out verbatim.
//
// $YOLO_JAIL_ID is set by NOTHING in this repo, so the old code's value was
// always $HOSTNAME — which in a nested jail (forced --net=host) is the HOST's
// hostname. The field was wrong in a real configuration, not just redundant.
func TestRequestReadsNoIdentityFromTheEnvironment(t *testing.T) {
	t.Setenv("YOLO_JAIL_ID", "jail-id-from-the-environment")
	t.Setenv("HOSTNAME", "hostname-from-the-environment")

	for _, req := range []map[string]any{
		buildRequest(false, 0, false),
		buildRequest(false, 0, true),
		buildRequest(true, 1, false),
	} {
		got := string(requestBody(req))
		if strings.Contains(got, "from-the-environment") {
			t.Errorf("request body carries an environment-derived identity: %s", got)
		}
	}
}

// captureStderr swaps os.Stderr for a pipe while fn runs and returns what was
// written to it. The client writes its diagnostics to os.Stderr directly — that
// is the contract a baked jail binary has with its user — so the only honest way
// to assert on one is to read the real fd.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = prev
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestDeadDaemonBehindALiveFrontIsAttributed is the failure half of the
// publishes:"socket" flip, and it is a REGRESSION TEST: the success path was
// pinned before the flip landed and did not move, but the failure path did.
//
// While the daemon published its own endpoint file, a dead daemon was a DIAL
// failure — the file was gone, or named a port nobody was on — so the client
// printed one of its three attributed messages and exited 2. Now yolo owns the
// listener: the front authenticates the jail from a file that is perfectly
// valid, then cannot reach the daemon and hangs up. The dial SUCCEEDS and the
// stream ends with no exit frame, which used to be `return 1` with no output at
// all — indistinguishable, from inside the jail, from a query that matched
// nothing.
//
// The front here is real (svcendpoint.ServeFront) with an upstream socket that
// is deliberately never bound, which is exactly the state a crashed or SIGKILLed
// host daemon leaves behind while `yolo run` keeps the front up.
func TestDeadDaemonBehindALiveFrontIsAttributed(t *testing.T) {
	// os.MkdirTemp is 0700; svcendpoint REFUSES to publish a credential into the
	// 0755 directory t.TempDir() creates.
	dir, err := os.MkdirTemp("/tmp", "yj-ps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	endpoint := filepath.Join(dir, "host-processes.endpoint")
	upstream := filepath.Join(dir, "never-bound.sock")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", "", upstream, stop)
	}()
	t.Cleanup(func() { close(stop); <-done })
	deadline := time.Now().Add(10 * time.Second)
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("the front never published a usable endpoint file")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var rc int
	stderr := captureStderr(t, func() {
		rc = call(endpoint, buildRequest(false, 0, false))
	})
	if rc == 0 {
		t.Fatalf("rc = 0 with no daemon behind the front; stderr = %q", stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("a dial that authenticated and then produced no exit frame said NOTHING. " +
			"The daemon is behind yolo's front now, so its death is no longer a dial " +
			"error — without a message here the whole failure is a silent non-zero exit.")
	}
	if !strings.Contains(stderr, endpoint) {
		t.Errorf("the message does not name the endpoint that failed: %q", stderr)
	}
	// The endpoint file is this jail's bearer token. Naming its path is the fix;
	// quoting its contents would be a credential leak in a terminal.
	published, err := os.ReadFile(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range strings.Fields(string(published)) {
		if len(field) < 16 {
			continue // the host:port is short and is not a secret
		}
		if strings.Contains(stderr, field) {
			t.Fatalf("the diagnostic echoed %d bytes of the endpoint file, which carries "+
				"this jail's bearer token", len(field))
		}
	}
}
