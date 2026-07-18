package brokerrelay

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// shortDir returns a short per-test dir under /tmp — AF_UNIX paths cap at 108
// bytes on Linux, and t.TempDir() is too long. Mirrors the relay_dir fixture.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-gorelay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// startRelay starts Serve in a goroutine and waits until the socket is
// connectable (past the bind->listen race).
func startRelay(t *testing.T, socketPath, brokerPath, jail string) (stop func()) {
	t.Helper()
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = Serve(socketPath, brokerPath, jail, stopCh)
		close(done)
	}()
	waitConnectable(t, socketPath)
	return func() {
		close(stopCh)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("relay did not shut down")
		}
	}
}

func waitConnectable(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never connectable", path)
}

// fakeBroker is a framed-JSON broker double: reads one 4-byte-BE length-
// prefixed JSON request per connection, records it, replies with a pong frame
// (stream 0) + exit frame (stream 2). Mirrors the Python FakeBroker.
type fakeBroker struct {
	path     string
	ln       net.Listener
	mu       sync.Mutex
	requests []map[string]any
}

func startFakeBroker(t *testing.T, path string) *fakeBroker {
	t.Helper()
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{path: path, ln: ln}
	go b.serve()
	return b
}

func (b *fakeBroker) serve() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.handle(conn)
	}
}

func (b *fakeBroker) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(header)
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return
	}
	b.mu.Lock()
	b.requests = append(b.requests, req)
	b.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{"pong": true, "jail_id_seen": req["jail_id"]})
	writeFrame(conn, 0, payload)
	exit := make([]byte, 4) // rc=0
	writeFrame(conn, 2, exit)
}

func (b *fakeBroker) stop() {
	b.ln.Close()
	os.Remove(b.path)
}

func (b *fakeBroker) lastRequest() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) == 0 {
		return nil
	}
	return b.requests[len(b.requests)-1]
}

func writeFrame(w io.Writer, streamID byte, payload []byte) {
	hdr := make([]byte, 5)
	hdr[0] = streamID
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	w.Write(hdr)
	w.Write(payload)
}

// framedRoundtrip sends one framed request through the relay and returns the
// first stdout-frame JSON of the response.
func framedRoundtrip(t *testing.T, path string, request map[string]any) map[string]any {
	t.Helper()
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	body, _ := json.Marshal(request)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	c.Write(hdr)
	c.Write(body)
	for {
		fh := make([]byte, 5)
		if _, err := io.ReadFull(c, fh); err != nil {
			t.Fatalf("EOF before a response frame: %v", err)
		}
		sid := fh[0]
		length := binary.BigEndian.Uint32(fh[1:])
		payload := make([]byte, length)
		if _, err := io.ReadFull(c, payload); err != nil {
			t.Fatalf("truncated response frame: %v", err)
		}
		if sid == 0 {
			var m map[string]any
			json.Unmarshal(payload, &m)
			return m
		}
		if sid == 2 {
			t.Fatal("exit frame before any stdout frame")
		}
	}
}

// TestJailIDStampedAndClientValueOverridden mirrors the Python test of the
// same name: the relay stamps jail_id host-side, overriding any client value.
func TestJailIDStampedAndClientValueOverridden(t *testing.T) {
	d := shortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	broker := startFakeBroker(t, brokerPath)
	defer broker.stop()
	stop := startRelay(t, relayPath, brokerPath, "jail-abc")
	defer stop()

	reply := framedRoundtrip(t, relayPath, map[string]any{"action": "ping"})
	if reply["pong"] != true {
		t.Errorf("pong = %v", reply["pong"])
	}
	if got := broker.lastRequest()["jail_id"]; got != "jail-abc" {
		t.Errorf("stamped jail_id = %v, want jail-abc", got)
	}

	reply = framedRoundtrip(t, relayPath, map[string]any{"action": "ping", "jail_id": "spoofed"})
	if reply["jail_id_seen"] != "jail-abc" {
		t.Errorf("jail_id_seen = %v, want jail-abc (client value must be overridden)", reply["jail_id_seen"])
	}
	if got := broker.lastRequest()["jail_id"]; got != "jail-abc" {
		t.Errorf("overridden jail_id = %v, want jail-abc", got)
	}
}

// rawUpstream is a byte-level upstream double for the verbatim path: it
// accumulates received bytes and, once `expect` bytes arrive, replies "PONG".
type rawUpstream struct {
	path     string
	ln       net.Listener
	expect   int
	mu       sync.Mutex
	received []byte
}

func startRawUpstream(t *testing.T, path string, expect int) *rawUpstream {
	t.Helper()
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	u := &rawUpstream{path: path, ln: ln, expect: expect}
	go u.serve()
	return u
}

func (u *rawUpstream) serve() {
	for {
		conn, err := u.ln.Accept()
		if err != nil {
			return
		}
		go u.handle(conn)
	}
}

func (u *rawUpstream) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65536)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			u.mu.Lock()
			u.received = append(u.received, buf[:n]...)
			done := len(u.received) >= u.expect
			u.mu.Unlock()
			if done {
				conn.Write([]byte("PONG"))
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (u *rawUpstream) stop() { u.ln.Close(); os.Remove(u.path) }

func (u *rawUpstream) got() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]byte(nil), u.received...)
}

// TestUnparseableFirstMessageForwardedVerbatim: garbage (not a framed JSON
// request) must reach the broker verbatim and the pipe keeps working.
func TestUnparseableFirstMessageForwardedVerbatim(t *testing.T) {
	garbage := []byte("NOT A FRAME AT ALL") // "NOT " decodes to a >1GB length
	d := shortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	up := startRawUpstream(t, brokerPath, len(garbage))
	defer up.stop()
	stop := startRelay(t, relayPath, brokerPath, "jail-v")
	defer stop()

	c, err := net.DialTimeout("unix", relayPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write(garbage)
	resp := make([]byte, 4)
	io.ReadFull(c, resp)
	c.Close()
	if string(resp) != "PONG" {
		t.Errorf("resp = %q, want PONG", resp)
	}
	if string(up.got()) != string(garbage) {
		t.Errorf("upstream received %q, want %q", up.got(), garbage)
	}
}

// TestValidFrameNonJSONBodyForwardedVerbatim: a well-framed but non-JSON body
// takes the other verbatim path — the complete frame (header included) is
// forwarded untouched.
func TestValidFrameNonJSONBodyForwardedVerbatim(t *testing.T) {
	body := []byte{0x01, 0x02, ' ', 'n', 'o', 't', ' ', 'j', 's', 'o', 'n', ' ', 0xff}
	msg := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(msg[:4], uint32(len(body)))
	copy(msg[4:], body)

	d := shortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	up := startRawUpstream(t, brokerPath, len(msg))
	defer up.stop()
	stop := startRelay(t, relayPath, brokerPath, "jail-v")
	defer stop()

	c, err := net.DialTimeout("unix", relayPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write(msg)
	resp := make([]byte, 4)
	io.ReadFull(c, resp)
	c.Close()
	if string(resp) != "PONG" {
		t.Errorf("resp = %q, want PONG", resp)
	}
	if string(up.got()) != string(msg) {
		t.Errorf("upstream received %q, want %q (whole frame verbatim)", up.got(), msg)
	}
}

// TestBrokerDownClientSeesEOF: relay up, broker down. The relay ends the
// connection with a clean EOF and zero frames — NOT ECONNRESET. The client
// sends its framed request first (like the real terminator); the dial-failure
// path must drain it before closing.
func TestBrokerDownClientSeesEOF(t *testing.T) {
	d := shortDir(t)
	relayPath := filepath.Join(d, "relay.sock")
	stop := startRelay(t, relayPath, filepath.Join(d, "no-broker.sock"), "jail-e")
	defer stop()

	c, err := net.DialTimeout("unix", relayPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	body, _ := json.Marshal(map[string]any{"action": "refresh"})
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	c.Write(hdr)
	c.Write(body)

	buf := make([]byte, 1)
	n, err := c.Read(buf)
	c.Close()
	if n != 0 || err != io.EOF {
		t.Errorf("expected clean EOF (n=0, err=EOF), got n=%d err=%v — a non-EOF error means the request wasn't drained (ECONNRESET regression)", n, err)
	}
}

// TestBrokerRestartNewInode mirrors the Round-2 regression: the relay dials
// the broker path PER CONNECTION, so killing the broker, re-binding the same
// path (new inode), and sending a second request through the SAME relay must
// succeed.
func TestBrokerRestartNewInode(t *testing.T) {
	d := shortDir(t)
	brokerPath := filepath.Join(d, "broker.sock")
	relayPath := filepath.Join(d, "relay.sock")
	broker := startFakeBroker(t, brokerPath)
	stop := startRelay(t, relayPath, brokerPath, "jail-r")
	defer stop()

	reply := framedRoundtrip(t, relayPath, map[string]any{"action": "ping"})
	if reply["pong"] != true {
		t.Fatal("ping through a fresh relay must work")
	}
	broker.stop() // old inode gone

	broker2 := startFakeBroker(t, brokerPath) // same path, NEW inode
	defer broker2.stop()
	reply = framedRoundtrip(t, relayPath, map[string]any{"action": "ping"})
	if reply["pong"] != true {
		t.Error("the same relay process must reach the restarted broker")
	}
	if broker2.lastRequest() == nil {
		t.Error("second ping never reached the new broker")
	}
}

// TestSigtermUnlinksOwnSocket: after Serve shuts down, its socket file is gone
// (a stopped relay reads as "socket absent", not "socket dead").
func TestSigtermUnlinksOwnSocket(t *testing.T) {
	d := shortDir(t)
	relayPath := filepath.Join(d, "relay.sock")
	stop := startRelay(t, relayPath, filepath.Join(d, "broker.sock"), "jail-x")
	if _, err := os.Stat(relayPath); err != nil {
		t.Fatalf("socket should exist while serving: %v", err)
	}
	stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(relayPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("relay did not unlink its socket on shutdown")
}
