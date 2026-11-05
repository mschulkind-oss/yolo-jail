package hostservice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

func TestHTTPHostIngressRejectsDuplicateJSONFieldsBeforeOrderedDecode(t *testing.T) {
	logs := captureLog(t)
	advertiseLoopback(t)
	dir, err := os.MkdirTemp("", "yj-http-duplicates-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	endpoint := filepath.Join(dir, "http.endpoint")
	var handlerInvocations atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = ServeHTTPBrokerEndpoint(func(s *Session) {
			handlerInvocations.Add(1)
			s.Stdout("entered\n")
		}, endpoint, stop)
		close(done)
	}()
	waitForEndpoint(t, endpoint)
	t.Cleanup(func() { close(stop); <-done })

	for _, tc := range []struct{ name, raw string }{
		{"top-level", `{"body_present":false,"body_present":"secret-value-must-not-be-logged"}`},
		{"nested object", `{"request":{"body_present":false,"body_present":true}}`},
		{"escaped key in array", `{"request":[{"body_present":false,"body\u005fpresent":true}]}`},
		{"trailing JSON", `{"request":{"body_present":false}} {}`},
		{"non-object", `[]`},
	} {
		conn := dialEndpoint(t, endpoint)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if err := frameproto.WriteRequest(conn, []byte(tc.raw)); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		_, readErr := frameproto.ReadFrame(conn)
		_ = conn.Close()
		if readErr == nil {
			t.Fatalf("rejected %s frame reached handler", tc.name)
		}
		if ne, ok := readErr.(interface{ Timeout() bool }); ok && ne.Timeout() {
			t.Fatalf("HTTP ingress did not immediately reject %s frame: %v", tc.name, readErr)
		}
		if n := handlerInvocations.Load(); n != 0 {
			t.Fatalf("handler invocations=%d after rejected %s frame, want zero", n, tc.name)
		}
	}

	// Identical names in distinct objects are valid and must not be mistaken for
	// a duplicate in one object.
	valid := `{"left":{"body_present":false},"right":{"body_present":true}}`
	conn := dialEndpoint(t, endpoint)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := frameproto.WriteRequest(conn, []byte(valid)); err != nil {
		t.Fatal(err)
	}
	frame, err := frameproto.ReadFrame(conn)
	if err != nil || frame.StreamID != frameproto.StreamStdout || string(frame.Payload) != "entered\n" {
		t.Fatalf("valid repeated names were not preserved as separate objects: frame=%+v err=%v", frame, err)
	}
	if n := handlerInvocations.Load(); n != 1 {
		t.Fatalf("handler invocations=%d after valid frame, want 1", n)
	}
	if strings.Contains(logs.String(), "secret-value-must-not-be-logged") || strings.Contains(logs.String(), "body_present") {
		t.Fatalf("request content leaked into host-service log: %s", logs.String())
	}
}

func TestHTTPDuplicateJSONFieldScannerUsesDecodedObjectKeys(t *testing.T) {
	for _, raw := range []string{
		`{"body_present":false,"body\u005fpresent":true}`,
		`{"outer":[{"b":1,"\u0062":2}]}`,
	} {
		if err := rejectDuplicateHTTPJSONFields([]byte(raw)); !errors.Is(err, errDuplicateHTTPJSONField) {
			t.Errorf("duplicate decoded JSON field err=%v, want duplicate-field refusal", err)
		}
	}
	if err := rejectDuplicateHTTPJSONFields([]byte(`{"left":{"b":1},"right":{"b":2}}`)); err != nil {
		t.Errorf("same field in different objects rejected: %v", err)
	}
	if err := rejectDuplicateHTTPJSONFields([]byte(`{"large":1e400}`)); err != nil {
		t.Errorf("valid large JSON number rejected by structural scan: %v", err)
	}
	for _, raw := range []string{`{"x":1}{"y":2}`, `[] trailing`, `{"x":1`} {
		if err := rejectDuplicateHTTPJSONFields([]byte(raw)); err == nil {
			t.Errorf("invalid or trailing JSON accepted: %s", raw)
		}
	}
}
