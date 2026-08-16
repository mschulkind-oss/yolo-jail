package brokerrelay

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// hex64 matches a bearer token's shape (64 lowercase hex). A token must never
// appear anywhere but the 0600 endpoint file — not on an argv, not in a log.
var hex64 = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

// privateShortDir is shortDir plus the 0700 the endpoint publisher requires.
// os.MkdirTemp already creates 0700, but asserting it here keeps the requirement
// visible: svcendpoint REFUSES to publish a credential into a group- or
// world-accessible directory, so a looser dir fails the front rather than the test
// it is standing in for.
func privateShortDir(t *testing.T) string {
	t.Helper()
	d := shortDir(t)
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// startRelayWithFront starts Serve with a loopback-TLS front and waits until BOTH
// halves are up: the Unix socket accepts, and the endpoint file parses as complete.
func startRelayWithFront(t *testing.T, socketPath, brokerPath, jail, endpointPath string) (stop func()) {
	t.Helper()
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(Config{
			SocketPath: socketPath, BrokerPath: brokerPath, JailID: jail,
			EndpointPath: endpointPath, AdvertiseHost: "127.0.0.1",
		}, stopCh)
		close(done)
	}()
	waitConnectable(t, socketPath)
	waitPublished(t, endpointPath)
	return func() {
		close(stopCh)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("relay did not shut down")
		}
	}
}

func waitPublished(t *testing.T, endpointPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(endpointPath) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("front never published a complete endpoint at %s", endpointPath)
}

// framedRoundtripFront sends one framed request THROUGH THE FRONT (pinned TLS +
// token, exactly as the in-jail terminator does) and returns the first
// stdout-frame JSON of the response.
func framedRoundtripFront(t *testing.T, endpointPath string, request map[string]any) map[string]any {
	t.Helper()
	c, err := svcendpoint.DialLocal(endpointPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial front: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	body, _ := json.Marshal(request)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	if _, err := c.Write(append(hdr, body...)); err != nil {
		t.Fatal(err)
	}
	for {
		fh := make([]byte, 5)
		if _, err := io.ReadFull(c, fh); err != nil {
			t.Fatalf("EOF before a response frame: %v", err)
		}
		payload := make([]byte, binary.BigEndian.Uint32(fh[1:]))
		if _, err := io.ReadFull(c, payload); err != nil {
			t.Fatalf("truncated response frame: %v", err)
		}
		if fh[0] == 0 {
			var m map[string]any
			_ = json.Unmarshal(payload, &m)
			return m
		}
		if fh[0] == 2 {
			t.Fatal("exit frame before any stdout frame")
		}
	}
}

// TestRelayFrontPreservesJailIDStamp: a request that arrives through the front
// reaches the broker with the HOST-SIDE jail_id stamp intact, and a client value is
// still overridden.
//
// This is the whole point of splicing rather than re-typing the relay core: the
// stamp, the per-connection dial and the failure semantics all live below the
// transport and must not notice it.
//
// THE `action` ASSERTION IS NOT DECORATION. Every other check here is satisfied by
// the wrong message: if yolo's connection preamble were prepended to this front,
// the relay would read THE PREAMBLE as its first frame, stamp jail_id onto it
// (it is a JSON object, so the stamp succeeds) and forward that — and the broker
// would answer `pong` with `jail_id_seen: "jail-tcp"` while the terminator's real
// request sat behind it. MEASURED: with brokerrelay's NoPreamble opt-out removed,
// this whole package stayed green until this line existed. Naming the request the
// broker received is what makes the test's title true.
func TestRelayFrontPreservesJailIDStamp(t *testing.T) {
	d := privateShortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	endpointPath := filepath.Join(d, "relay.endpoint")
	fake := startFakeBroker(t, brokerPath)
	defer fake.stop()
	stop := startRelayWithFront(t, relayPath, brokerPath, "jail-tcp", endpointPath)
	defer stop()

	reply := framedRoundtripFront(t, endpointPath, map[string]any{"action": "ping"})
	if reply["pong"] != true {
		t.Errorf("pong = %v", reply["pong"])
	}
	if got := fake.lastRequest()["action"]; got != "ping" {
		t.Errorf("the broker's first framed message was %v, want the terminator's own "+
			"request (action=ping) — something is being written to the broker ahead of it",
			fake.lastRequest())
	}
	if got := fake.lastRequest()["jail_id"]; got != "jail-tcp" {
		t.Errorf("stamped jail_id = %v, want jail-tcp", got)
	}
	reply = framedRoundtripFront(t, endpointPath, map[string]any{"action": "ping", "jail_id": "spoofed"})
	if reply["jail_id_seen"] != "jail-tcp" {
		t.Errorf("jail_id_seen = %v, want jail-tcp (client value must be overridden)", reply["jail_id_seen"])
	}
}

// TestRelayFrontPrependsNothingToTheBrokerStream is the tripwire for the ONE
// deliberate NoPreamble opt-out in the tree (brokerrelay.go), stated at the byte
// level because it sits on the credential path.
//
// svcendpoint's front prepends a CONNECTION PREAMBLE to every connection it
// carries: one 4-byte-BE-framed JSON object, host→daemon, at connection open
// (svcendpoint/preamble.go). This relay must not receive one, because it is not a
// dumb pipe — handle → readFirstMessage consumes the FIRST framed message and
// stampJailID rewrites it. With a preamble in front, that first message is yolo's
// own frame: the relay stamps and forwards THAT, the broker answers it, and the
// terminator's refresh request is answered by nobody. Every jail's Claude OAuth
// refresh fails, and a burned single-use refresh token logs the user out of all of
// them, so this is the most expensive silent failure this transport can produce.
//
// A RAW upstream rather than the framed fakeBroker, deliberately: "nothing was
// prepended" is a claim about BYTES, and a JSON-level double can be satisfied by
// the wrong object — which is exactly how the preamble bug hid until
// TestRelayFrontPreservesJailIDStamp learned to name the request it expects.
//
// Mutation-verified: dropping `NoPreamble: true` from brokerrelay.Serve's
// ServeFrontWithOptions call left `go test -short ./...` entirely green before this
// test and TestRelayFrontPreservesJailIDStamp's action assertion existed.
func TestRelayFrontPrependsNothingToTheBrokerStream(t *testing.T) {
	// Deliberately NOT a framed JSON request: an unparseable first message takes
	// readFirstMessage's verbatim downgrade, so what the upstream receives is
	// exactly what crossed the transport — no stamp re-serialization in between to
	// explain a difference away.
	garbage := []byte("NOT A FRAME AT ALL")
	d := privateShortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	endpointPath := filepath.Join(d, "relay.endpoint")
	up := startRawUpstream(t, brokerPath, len(garbage))
	defer up.stop()
	stop := startRelayWithFront(t, relayPath, brokerPath, "jail-raw", endpointPath)
	defer stop()

	c, err := svcendpoint.DialLocal(endpointPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial front: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(garbage); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 4)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatalf("upstream never replied: %v", err)
	}
	if string(resp) != "PONG" {
		t.Errorf("resp = %q, want PONG", resp)
	}
	if got := up.got(); string(got) != string(garbage) {
		t.Errorf("the broker received %q, want %q verbatim.\nAnything ahead of the client's "+
			"own bytes is yolo's connection preamble, which this relay would stamp and forward "+
			"in place of the terminator's request — restore FrontOptions{NoPreamble: true} in "+
			"brokerrelay.Serve.", got, garbage)
	}
}

// slowMultiFrameUpstream replies with SEVERAL frames spread over time, after the
// client has stopped writing. It is the shape that catches a splice which returns
// on whichever direction finishes first.
type slowMultiFrameUpstream struct {
	ln     net.Listener
	frames int
	gap    time.Duration
}

func startSlowMultiFrameUpstream(t *testing.T, path string, frames int, gap time.Duration) *slowMultiFrameUpstream {
	t.Helper()
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	u := &slowMultiFrameUpstream{ln: ln, frames: frames, gap: gap}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go u.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close(); os.Remove(path) })
	return u
}

func (u *slowMultiFrameUpstream) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, binary.BigEndian.Uint32(header))); err != nil {
		return
	}
	for i := 0; i < u.frames; i++ {
		time.Sleep(u.gap)
		writeFrame(conn, 0, []byte(strings.Repeat("x", 64)))
	}
	exit := make([]byte, 4)
	writeFrame(conn, 2, exit)
}

// TestRelayFrontResponseNotTruncated: a multi-frame response arrives COMPLETE even
// though the client has already half-closed its write side.
//
// The half-close is the whole point, and getting it wrong is the single most likely
// subtle bug in the splice. The request direction is `io.Copy(up, client)`; a client
// that says "I am done sending" ends it immediately. If the splice waited on
// whichever direction finished first — or propagated that EOF upstream — it would
// close the Unix connection to the relay, whose pipe() tears down BOTH of its
// sockets on either EOF (frozen parity), and the response still in flight would be
// cut off mid-stream. So the request direction must run UNWAITED and its end must
// mean nothing. #32 spends nine comment lines on this and tests none of it.
//
// Verified by mutation: waiting on the first direction to finish makes this fail
// with a partial frame count, and an earlier version of this test that never
// half-closed did NOT catch it.
func TestRelayFrontResponseNotTruncated(t *testing.T) {
	d := privateShortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	endpointPath := filepath.Join(d, "relay.endpoint")
	startSlowMultiFrameUpstream(t, brokerPath, 5, 60*time.Millisecond)
	stop := startRelayWithFront(t, relayPath, brokerPath, "jail-slow", endpointPath)
	defer stop()

	c, err := svcendpoint.DialLocal(endpointPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	body, _ := json.Marshal(map[string]any{"action": "ping"})
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	if _, err := c.Write(append(hdr, body...)); err != nil {
		t.Fatal(err)
	}
	// HALF-CLOSE: end the request direction while the response is still being
	// produced. A tls.Conn has CloseWrite (and no CloseRead) — which is also why the
	// relay core cannot simply be re-typed onto it.
	cw, ok := c.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("the dialed conn has no CloseWrite; this test cannot end the request direction")
	}
	if err := cw.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	stdout := 0
	for {
		fh := make([]byte, 5)
		if _, err := io.ReadFull(c, fh); err != nil {
			t.Fatalf("stream ended after %d/%d stdout frames and no exit frame — "+
				"the splice truncated the response: %v", stdout, 5, err)
		}
		payload := make([]byte, binary.BigEndian.Uint32(fh[1:]))
		if _, err := io.ReadFull(c, payload); err != nil {
			t.Fatalf("truncated frame body after %d stdout frames: %v", stdout, err)
		}
		if fh[0] == 0 {
			stdout++
		}
		if fh[0] == 2 {
			break
		}
	}
	if stdout != 5 {
		t.Errorf("received %d stdout frames, want 5 — the response was cut short", stdout)
	}
}

// TestRelayFrontFailureDoesNotFailServe: an unusable publish path leaves the Unix
// relay serving.
//
// The front is fire-and-forget on purpose. Failing Serve would take down a relay a
// same-host caller can still use, and would report a publication problem as "the
// relay is dead". The host-side health gate handles the real consequence by seeing
// no endpoint and respawning.
func TestRelayFrontFailureDoesNotFailServe(t *testing.T) {
	d := privateShortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	// A publish path whose parent is a REGULAR FILE: MkdirAll fails, so the front
	// cannot start at all.
	blocker := filepath.Join(d, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpointPath := filepath.Join(blocker, "relay.endpoint")

	var logs bytes.Buffer
	orig := Logger
	Logger = log.New(&logs, "", 0)
	t.Cleanup(func() { Logger = orig })

	fake := startFakeBroker(t, brokerPath)
	defer fake.stop()
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(Config{
			SocketPath: relayPath, BrokerPath: brokerPath, JailID: "jail-nofront",
			EndpointPath: endpointPath, AdvertiseHost: "127.0.0.1",
		}, stopCh)
		close(done)
	}()
	waitConnectable(t, relayPath)
	// The Unix relay still works end to end.
	reply := framedRoundtrip(t, relayPath, map[string]any{"action": "ping"})
	if reply["pong"] != true {
		t.Errorf("the unix relay stopped serving because its front failed: %v", reply)
	}
	if _, err := os.Stat(endpointPath); err == nil {
		t.Error("an endpoint file exists at an unusable path")
	}
	close(stopCh)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("relay did not shut down")
	}
	if !strings.Contains(logs.String(), "not published") {
		t.Errorf("front failure was not logged; got %q", logs.String())
	}
}

// TestFrontStopUnlinksEndpoint: stopping the relay retires the credential.
//
// A published endpoint file naming a dead port is worse than none: the health gate
// parses content, but a client would dial and fail with a connection error rather
// than the clear "not published".
func TestFrontStopUnlinksEndpoint(t *testing.T) {
	d := privateShortDir(t)
	endpointPath := filepath.Join(d, "relay.endpoint")
	stop := startRelayWithFront(t, filepath.Join(d, "relay.sock"), filepath.Join(d, "broker.sock"),
		"jail-gone", endpointPath)
	stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(endpointPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the endpoint file survived the relay; a stopped relay must retire its credential")
}

// TestNoEndpointMeansNoFront: the zero Config publishes nothing and binds no TCP
// port. Every relay before this change is that shape, and the package's own tests
// still use it.
func TestNoEndpointMeansNoFront(t *testing.T) {
	d := privateShortDir(t)
	relayPath := filepath.Join(d, "relay.sock")
	stop := startRelay(t, relayPath, filepath.Join(d, "broker.sock"), "jail-nofront")
	defer stop()
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".endpoint") {
			t.Errorf("a relay configured with no EndpointPath published %s", e.Name())
		}
	}
}

// TestRelayFlagErrorEndpointWithoutSocket: --endpoint with no --socket is a usage
// error naming the real mistake, not the generic required-flags list. A front with
// nothing behind it authenticates a jail and then drops it, which reads as a broker
// failure — the most misleading outcome available.
func TestRelayFlagErrorEndpointWithoutSocket(t *testing.T) {
	msg := relayFlagError("", "/tmp/broker.sock", "jail-x", "/tmp/x.endpoint")
	if msg == "" {
		t.Fatal("--endpoint without --socket was accepted")
	}
	if !strings.Contains(msg, "--endpoint requires --socket") {
		t.Errorf("message = %q, want it to name the endpoint/socket dependency", msg)
	}
	// And Main turns it into exit 2.
	if rc := Main([]string{"--endpoint", "/tmp/x.endpoint", "--broker", "/tmp/b.sock", "--jail", "j"}); rc != 2 {
		t.Errorf("Main rc = %d, want 2", rc)
	}
	// The other combinations still behave: complete is accepted, incomplete is not.
	if msg := relayFlagError("/tmp/r.sock", "/tmp/b.sock", "j", "/tmp/x.endpoint"); msg != "" {
		t.Errorf("a complete flag set was rejected: %q", msg)
	}
	if msg := relayFlagError("/tmp/r.sock", "", "j", ""); msg == "" {
		t.Error("a missing --broker was accepted")
	}
}

// TestNoTokenInRelayArgvOrLogs: the relay takes no token on argv and writes none to
// its log.
//
// #32 passed a --token-file path precisely so the secret would not show in `ps`;
// under token-in-file there is no second artifact at all, so the property to hold
// is the stronger one — nothing token-shaped exists outside the 0600 endpoint file.
func TestNoTokenInRelayArgvOrLogs(t *testing.T) {
	d := privateShortDir(t)
	endpointPath := filepath.Join(d, "relay.endpoint")
	var logs bytes.Buffer
	orig := Logger
	Logger = log.New(&logs, "", 0)
	t.Cleanup(func() { Logger = orig })
	// svcendpoint logs the listen line; capture it in the same buffer.
	origSvc := svcendpoint.Logger
	svcendpoint.Logger = log.New(&logs, "", 0)
	t.Cleanup(func() { svcendpoint.Logger = origSvc })

	brokerPath := filepath.Join(d, "broker.sock")
	fake := startFakeBroker(t, brokerPath)
	defer fake.stop()
	stop := startRelayWithFront(t, filepath.Join(d, "relay.sock"), brokerPath, "jail-quiet", endpointPath)
	framedRoundtripFront(t, endpointPath, map[string]any{"action": "ping"})
	stop()

	ep, err := os.ReadFile(endpointPath)
	if err == nil && strings.Contains(logs.String(), strings.TrimSpace(string(ep))) {
		t.Error("the endpoint line was written to the log")
	}
	out := logs.String()
	if hex64.MatchString(out) {
		t.Errorf("a 64-hex run (a token) appears in the relay log:\n%s", out)
	}
}
