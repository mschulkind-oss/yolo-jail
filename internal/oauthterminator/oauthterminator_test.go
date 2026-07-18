package oauthterminator

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestIsRefreshGrant(t *testing.T) {
	cases := map[string]bool{
		`{"grant_type":"refresh_token"}`:           true,
		`{"grant_type":"refresh_token","extra":1}`: true,
		`{"grant_type":"authorization_code"}`:      false,
		`{"grant_type":"refresh_token"} trailing`:  false, // trailing data -> not parseable
		`{}`:                false,
		``:                  false,
		`not json`:          false,
		`["refresh_token"]`: false, // not an object
		`"refresh_token"`:   false,
	}
	for body, want := range cases {
		if got := IsRefreshGrant([]byte(body)); got != want {
			t.Errorf("IsRefreshGrant(%q) = %v, want %v", body, got, want)
		}
	}
}

// shortDir: AF_UNIX path cap.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-term-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// TestAskHostBrokerRelayLayerMissingSocket: a missing socket path is the relay
// layer ("relay unreachable — ...").
func TestAskHostBrokerRelayLayerMissingSocket(t *testing.T) {
	_, err := AskHostBroker(filepath.Join(shortDir(t), "nope.sock"), singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("err = %q, want relay-layer prefix", err.Error())
	}
}

// TestAskHostBrokerBrokerLayerEOF: a relay that accepts then closes WITHOUT an
// exit frame is the broker layer ("host broker unreachable through the relay
// (connection closed without an exit frame)").
func TestAskHostBrokerBrokerLayerEOF(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Read the request, then close without any response frame.
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		c.Close()
	}()
	_, err = AskHostBroker(sock, singleton("action", "refresh"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unreachable through the relay") ||
		!strings.Contains(err.Error(), "without an exit frame") {
		t.Errorf("err = %q, want broker-layer EOF message", err.Error())
	}
	// Crucially, it must NOT be the relay-layer wording.
	if strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("broker-down misattributed to relay layer: %q", err.Error())
	}
}

// TestAskHostBrokerSuccess: a relay that returns a framed JSON stdout + exit 0
// yields the parsed object.
func TestAskHostBrokerSuccess(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		// stdout frame with a JSON body, then exit 0.
		payload := []byte(`{"pong": true}`)
		fh := make([]byte, 5)
		fh[0] = streamStdout
		binary.BigEndian.PutUint32(fh[1:], uint32(len(payload)))
		c.Write(fh)
		c.Write(payload)
		ex := make([]byte, 5) // stream 2, len 4
		ex[0] = streamExit
		binary.BigEndian.PutUint32(ex[1:], 4)
		c.Write(ex)
		c.Write([]byte{0, 0, 0, 0})
	}()
	resp, err := AskHostBroker(sock, singleton("action", "ping"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resp.Get("pong"); v != true {
		t.Errorf("resp pong = %v", v)
	}
}

// TestProxyUpstreamMapping: a broker {error} response maps to 502.
func TestProxyUpstream502OnBrokerError(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	serveOnce(t, sock, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "upstream_unreachable")
		m.Set("message", "no DNS")
		return m
	})
	res := ProxyUpstream(sock, "GET", "/foo", map[string]string{}, nil)
	if res.Status != 502 {
		t.Errorf("status = %d, want 502", res.Status)
	}
}

// TestProxyUpstreamPassthrough: a well-formed proxy response passes the
// upstream status/body through verbatim.
func TestProxyUpstreamPassthrough(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "relay.sock")
	serveOnce(t, sock, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("status", jsonx.IntValue(418))
		h := jsonx.NewOrderedMap()
		h.Set("X-Test", "yes")
		m.Set("headers", h)
		m.Set("body_b64", "aGVsbG8=") // "hello"
		return m
	})
	res := ProxyUpstream(sock, "GET", "/foo", map[string]string{}, nil)
	if res.Status != 418 {
		t.Errorf("status = %d, want 418", res.Status)
	}
	if string(res.Body) != "hello" {
		t.Errorf("body = %q, want hello", res.Body)
	}
	if res.Headers["X-Test"] != "yes" {
		t.Errorf("header X-Test = %q", res.Headers["X-Test"])
	}
}

// TestRefreshMapping: broker {error} -> 400; success -> 200.
func TestRefreshMapping(t *testing.T) {
	dir := shortDir(t)
	sockErr := filepath.Join(dir, "err.sock")
	serveOnce(t, sockErr, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "no_refresh_token")
		return m
	})
	if res := Refresh(sockErr); res.Status != 400 {
		t.Errorf("error refresh status = %d, want 400", res.Status)
	}

	sockOK := filepath.Join(dir, "ok.sock")
	serveOnce(t, sockOK, func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("access_token", "AT")
		m.Set("token_type", "Bearer")
		return m
	})
	res := Refresh(sockOK)
	if res.Status != 200 {
		t.Errorf("ok refresh status = %d, want 200", res.Status)
	}
	if !strings.Contains(string(res.Body), `"access_token": "AT"`) {
		t.Errorf("ok refresh body = %q", res.Body)
	}
}

// serveOnce starts a one-shot relay double that reads a framed request and
// replies with respFn()'s object framed as stdout + exit 0.
func serveOnce(t *testing.T, sock string, respFn func() *jsonx.OrderedMap) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hdr := make([]byte, 4)
		io.ReadFull(c, hdr)
		length := binary.BigEndian.Uint32(hdr)
		io.ReadFull(c, make([]byte, length))
		body, _ := json.Marshal(mapOf(respFn()))
		fh := make([]byte, 5)
		fh[0] = streamStdout
		binary.BigEndian.PutUint32(fh[1:], uint32(len(body)))
		c.Write(fh)
		c.Write(body)
		ex := make([]byte, 5)
		ex[0] = streamExit
		binary.BigEndian.PutUint32(ex[1:], 4)
		c.Write(ex)
		c.Write([]byte{0, 0, 0, 0})
	}()
	// Give the listener a moment.
	time.Sleep(20 * time.Millisecond)
}

// mapOf flattens an OrderedMap to a plain map for json.Marshal in the test
// double (order doesn't matter for the double's response — the terminator
// decodes it with jsonx anyway).
func mapOf(m *jsonx.OrderedMap) map[string]any {
	out := map[string]any{}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = plain(v)
	}
	return out
}

func plain(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return mapOf(t)
	default:
		// jsonx.IntValue etc. re-encode via DumpsCompact; for the double just
		// pass strings/bools through and stringify the rest.
		switch t.(type) {
		case string, bool, float64:
			return t
		default:
			s, _ := jsonx.DumpsCompact(t)
			// numeric literal -> number
			var n json.Number
			if err := json.Unmarshal([]byte(s), &n); err == nil {
				if i, err := n.Int64(); err == nil {
					return i
				}
			}
			return s
		}
	}
}
