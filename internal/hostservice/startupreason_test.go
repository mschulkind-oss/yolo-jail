package hostservice

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func sendStartupFrame(t *testing.T, conn net.Conn, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Error(err)
		return
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(prefix[:]); err != nil {
		t.Error(err)
		return
	}
	if _, err := conn.Write(body); err != nil {
		t.Error(err)
		return
	}
}

func TestReadStartupReasonValidatesAttributionAndSanitizes(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go sendStartupFrame(t, right, StartupReason{Version: 1, Service: "fixture", Attempt: "attempt-1",
		Class: "configuration", Reason: "bad\nsetting", Remedy: "fix [the] setting"})
	reason, err := ReadStartupReason(left, "fixture", "attempt-1", time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if reason.Reason != "bad setting" || reason.Remedy != "fix [the] setting" {
		t.Fatalf("reason was not sanitized as a single line: %+v", reason)
	}
}

func TestReadStartupReasonRejectsMalformedAttributionAndOversize(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
	}{
		{"wrong-attempt", StartupReason{Version: 1, Service: "fixture", Attempt: "other", Class: "configuration", Reason: "refused"}},
		{"wrong-service", StartupReason{Version: 1, Service: "other", Attempt: "attempt-1", Class: "configuration", Reason: "refused"}},
		{"unknown-class", StartupReason{Version: 1, Service: "fixture", Attempt: "attempt-1", Class: "secret", Reason: "refused"}},
		{"unsupported-version", StartupReason{Version: 2, Service: "fixture", Attempt: "attempt-1", Class: "configuration", Reason: "refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			go sendStartupFrame(t, right, tc.body)
			if _, err := ReadStartupReason(left, "fixture", "attempt-1", time.Now().Add(time.Second)); err == nil {
				t.Fatal("invalid record was accepted")
			}
		})
	}

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], StartupReasonMaxBytes+1)
		_, _ = right.Write(prefix[:])
	}()
	if _, err := ReadStartupReason(left, "fixture", "attempt-1", time.Now().Add(time.Second)); err == nil {
		t.Fatal("oversized frame was accepted")
	}
}

func TestReadStartupReasonRejectsMalformedFramingAndTrailingData(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(net.Conn)
	}{
		{name: "empty frame", write: func(c net.Conn) { sendRawStartupFrame(c, nil) }},
		{name: "truncated body", write: func(c net.Conn) { sendRawStartupFrame(c, []byte(`{"version":1`)); _ = c.Close() }},
		{name: "unknown field", write: func(c net.Conn) {
			body, _ := json.Marshal(map[string]any{"version": 1, "service": "fixture", "attempt": "attempt-1",
				"class": "configuration", "reason": "refused", "secret": "must-not-render"})
			sendRawStartupFrame(c, body)
		}},
		{name: "trailing json", write: func(c net.Conn) {
			body := []byte(`{"version":1,"service":"fixture","attempt":"attempt-1","class":"configuration","reason":"refused"}{}`)
			sendRawStartupFrame(c, body)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			go tc.write(right)
			if _, err := ReadStartupReason(left, "fixture", "attempt-1", time.Now().Add(time.Second)); err == nil {
				t.Fatal("malformed record was accepted")
			}
		})
	}
}

func sendRawStartupFrame(conn net.Conn, body []byte) {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	_, _ = conn.Write(prefix[:])
	if len(body) > 0 {
		_, _ = conn.Write(body)
	}
}

func TestReadStartupReasonRejectsARecordThatArrivesAfterItsDeadline(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() {
		time.Sleep(40 * time.Millisecond)
		sendRawStartupFrame(right, []byte(`{"version":1,"service":"fixture","attempt":"attempt-1","class":"configuration","reason":"late refusal"}`))
	}()
	start := time.Now()
	if _, err := ReadStartupReason(left, "fixture", "attempt-1", start.Add(10*time.Millisecond)); err == nil {
		t.Fatal("late startup reason was accepted")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("late reason read exceeded its deadline: %s", elapsed)
	}
}

func TestReadStartupReasonNeverWaitsForPeerEOF(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close() // remains open without sending or closing
	start := time.Now()
	_, err := ReadStartupReason(left, "fixture", "attempt-1", start.Add(30*time.Millisecond))
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("read without record did not honor deadline: err=%v elapsed=%s", err, time.Since(start))
	}
}

func TestStartupReasonTextIsBounded(t *testing.T) {
	text := safeStartupText(strings.Repeat("x", 1000))
	if len([]rune(text)) > startupReasonTextMax {
		t.Fatalf("text length %d exceeds limit %d", len([]rune(text)), startupReasonTextMax)
	}
}
