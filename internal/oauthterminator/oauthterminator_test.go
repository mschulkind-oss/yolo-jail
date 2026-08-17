package oauthterminator

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
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

// TestAskHostBrokerMissingEndpointIsRelayLayer: an unpublished endpoint file is
// the relay layer ("relay unreachable — ...").
//
// The attribution rides on ENOENT surviving svcendpoint's error wrapping — the
// errno gate here is unchanged from the Unix-socket era, and it only keeps working
// because Read wraps the *PathError alongside its own sentinel. This test is what
// notices if that ever stops being true.
func TestAskHostBrokerMissingEndpointIsRelayLayer(t *testing.T) {
	_, err := AskHostBroker(filepath.Join(privateDir(t), "nope.endpoint"), singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("err = %q, want relay-layer prefix", err.Error())
	}
}

// TestAskHostBrokerDeadListenerIsRelayLayer: a COMPLETE, well-formed endpoint file
// whose listener is gone is the relay layer too — the second errno in the gate.
//
// This is the most likely real-world relay failure and it had no test. The file
// outliving its listener is not hypothetical: svcendpoint publishes at 0600 and
// unlinks on Close, but a SIGKILLed relay never runs that unlink, and stopLoopholes
// deliberately leaves the directory alone whenever a relaunch holds the workspace
// flock or the container is still running. So the steady state after a crash is
// exactly this: a parseable endpoint naming an address nobody is on.
//
// Only ENOENT was covered, which is the branch a file that never existed takes.
// ECONNREFUSED is a different syscall on a different code path through
// svcendpoint.Dial (the TLS dial, wrapped in *net.OpError), and the whole point of
// isRelayLayerDialErr's two-errno gate is that both mean "the relay is down". Drop
// ECONNREFUSED from it and this case silently becomes the generic
// "host broker endpoint …" message, which reads like a configuration fault and
// sends the reader to the file rather than to the process.
func TestAskHostBrokerDeadListenerIsRelayLayer(t *testing.T) {
	path := filepath.Join(privateDir(t), "claude-oauth-broker.endpoint")
	ln, err := svcendpoint.Listen(path, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := svcendpoint.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	// Kill the listener, then restore its file — a SIGKILLed relay's leftovers.
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svcendpoint.Publish(path, ep); err != nil {
		t.Fatal(err)
	}
	if !svcendpoint.Probe(path) {
		t.Fatal("premise lost: the restored endpoint file must still parse as COMPLETE, " +
			"otherwise this exercises the malformed-file branch instead")
	}

	_, err = AskHostBroker(path, singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("err = %q, want the relay-down layer: the endpoint parses and the "+
			"listener is gone", err.Error())
	}
	if strings.Contains(err.Error(), "unreachable through the relay") {
		t.Errorf("a dead relay was misattributed to the BROKER layer: %q", err.Error())
	}
	if strings.HasPrefix(err.Error(), "relay auth rejected") {
		t.Errorf("a dead relay was misattributed to the AUTH layer: %q", err.Error())
	}
}

// TestAskHostBrokerAuthRejectedIsItsOwnLayer: a token mismatch is reported as AUTH,
// not as the broker layer.
//
// This is the defect #32 leaves behind. A mismatch is a post-accept drop, so
// without a distinct signal it surfaces as EOF-before-exit-frame and gets reported
// as "host broker unreachable through the relay" — telling the operator to go look
// at the broker when the actual fault is a stale endpoint file. svcendpoint's
// one-byte accept ack is what makes the two distinguishable: it arrives before any
// request is written, so an EOF at that point can only be a refused token.
func TestAskHostBrokerAuthRejectedIsItsOwnLayer(t *testing.T) {
	double := startRelayDouble(t, respondOnce(func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("pong", true)
		return m
	}))
	// Republish the SAME address and certificate with a DIFFERENT token — exactly
	// what a stale file left by a predecessor looks like.
	ep, err := svcendpoint.Read(double.endpointPath)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := svcendpoint.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	ep.Token = wrong
	if err := svcendpoint.Publish(double.endpointPath, ep); err != nil {
		t.Fatal(err)
	}

	_, err = AskHostBroker(double.endpointPath, singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "relay auth rejected") {
		t.Errorf("err = %q, want the auth layer", err.Error())
	}
	// The two frozen layers must not claim it.
	if strings.Contains(err.Error(), "unreachable through the relay") {
		t.Errorf("auth failure misattributed to the BROKER layer: %q", err.Error())
	}
	if strings.HasPrefix(err.Error(), "relay unreachable") {
		t.Errorf("auth failure misattributed to the relay-down layer: %q", err.Error())
	}
}

// TestAskHostBrokerMalformedEndpointIsNotMisattributed: a 2-field (older-format or
// truncated) endpoint file is neither layer — it names its own fault.
//
// Putting it in the relay-down branch would send someone hunting a dead process;
// putting it in the broker branch would send them to the broker. The file itself is
// the problem and the message has to say so.
func TestAskHostBrokerMalformedEndpointIsNotMisattributed(t *testing.T) {
	path := filepath.Join(privateDir(t), "stale.endpoint")
	if err := os.WriteFile(path, []byte("127.0.0.1:1 Y29zdA==\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := AskHostBroker(path, singleton("action", "ping"))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.HasPrefix(err.Error(), "relay unreachable") ||
		strings.Contains(err.Error(), "unreachable through the relay") {
		t.Errorf("a malformed endpoint file was attributed to a layer: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "malformed endpoint file") {
		t.Errorf("err = %q, want it to name the malformed file", err.Error())
	}
}

// TestAskHostBrokerBrokerLayerEOF: a relay that AUTHENTICATES, accepts, then closes
// WITHOUT an exit frame is the broker layer ("host broker unreachable through the
// relay (connection closed without an exit frame)").
//
// Unchanged in assertion, and that is the point: the frozen broker-layer
// attribution must survive the transport swap. Only the double moved onto the real
// transport — it now authenticates first, which is exactly what distinguishes this
// from the auth failure above.
func TestAskHostBrokerBrokerLayerEOF(t *testing.T) {
	double := startRelayDouble(t, func(c net.Conn) {
		// Read the request, then close without any response frame.
		_, _ = readFramedRequest(c)
	})
	_, err := AskHostBroker(double.endpointPath, singleton("action", "refresh"))
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

// TestAskHostBrokerOverEndpoint: the success path over the real transport — read
// the endpoint file, pin the certificate, present the token, then speak the frame
// protocol.
//
// It also pins the ORDERING, not merely that a token was presented: the first bytes
// the daemon side ever sees are the request's own length prefix, because the
// transport consumed the token frame BEFORE handing the connection over. A daemon
// therefore cannot forget to authenticate — the unauthenticated connection is never
// offered to it.
func TestAskHostBrokerOverEndpoint(t *testing.T) {
	gotFirst := make(chan []byte, 1)
	double := startRelayDouble(t, func(c net.Conn) {
		body, err := readFramedRequest(c)
		if err != nil {
			return
		}
		gotFirst <- body
		writeFramedResponse(c, []byte(`{"pong": true}`))
	})
	resp, err := AskHostBroker(double.endpointPath, singleton("action", "ping"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resp.Get("pong"); v != true {
		t.Errorf("resp pong = %v", v)
	}
	select {
	case body := <-gotFirst:
		// The daemon's FIRST read is the request. Had the token frame still been in
		// the stream, this would be 64 hex bytes instead.
		if !strings.Contains(string(body), `"action"`) {
			t.Errorf("the daemon's first framed read was %q, want the request — the "+
				"token frame must be consumed by the transport, before the handler", body)
		}
		if hex64.MatchString(string(body)) {
			t.Errorf("a token reached the daemon's protocol stream: %q", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon side never read a request")
	}
}

// TestProxyUpstreamMapping: a broker {error} response maps to 502.
func TestProxyUpstream502OnBrokerError(t *testing.T) {
	double := startRelayDouble(t, respondOnce(func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "upstream_unreachable")
		m.Set("message", "no DNS")
		return m
	}))
	res := ProxyUpstream(double.endpointPath, "GET", "/foo", map[string]string{}, nil)
	if res.Status != 502 {
		t.Errorf("status = %d, want 502", res.Status)
	}
}

// TestProxyUpstreamPassthrough: a well-formed proxy response passes the
// upstream status/body through verbatim.
func TestProxyUpstreamPassthrough(t *testing.T) {
	double := startRelayDouble(t, respondOnce(func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("status", jsonx.IntValue(418))
		h := jsonx.NewOrderedMap()
		h.Set("X-Test", "yes")
		m.Set("headers", h)
		m.Set("body_b64", "aGVsbG8=") // "hello"
		return m
	}))
	res := ProxyUpstream(double.endpointPath, "GET", "/foo", map[string]string{}, nil)
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
	errDouble := startRelayDouble(t, respondOnce(func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("error", "no_refresh_token")
		return m
	}))
	if res := Refresh(errDouble.endpointPath); res.Status != 400 {
		t.Errorf("error refresh status = %d, want 400", res.Status)
	}

	okDouble := startRelayDouble(t, respondOnce(func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("access_token", "AT")
		m.Set("token_type", "Bearer")
		return m
	}))
	res := Refresh(okDouble.endpointPath)
	if res.Status != 200 {
		t.Errorf("ok refresh status = %d, want 200", res.Status)
	}
	if !strings.Contains(string(res.Body), `"access_token": "AT"`) {
		t.Errorf("ok refresh body = %q", res.Body)
	}
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
