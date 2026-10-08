package hostservice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
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

type eofStartupConn struct{ reader *bytes.Reader }

func (c *eofStartupConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *eofStartupConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (c *eofStartupConn) Close() error                     { return nil }
func (c *eofStartupConn) LocalAddr() net.Addr              { return testNetAddr("local") }
func (c *eofStartupConn) RemoteAddr() net.Addr             { return testNetAddr("remote") }
func (c *eofStartupConn) SetDeadline(time.Time) error      { return nil }
func (c *eofStartupConn) SetReadDeadline(time.Time) error  { return nil }
func (c *eofStartupConn) SetWriteDeadline(time.Time) error { return nil }

type deadlineFailureConn struct {
	closed chan struct{}
	reads  int
}

func newDeadlineFailureConn() *deadlineFailureConn {
	return &deadlineFailureConn{closed: make(chan struct{})}
}

func (c *deadlineFailureConn) Read([]byte) (int, error) {
	c.reads++
	<-c.closed
	return 0, io.ErrClosedPipe
}
func (c *deadlineFailureConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (c *deadlineFailureConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func (c *deadlineFailureConn) LocalAddr() net.Addr         { return testNetAddr("local") }
func (c *deadlineFailureConn) RemoteAddr() net.Addr        { return testNetAddr("remote") }
func (c *deadlineFailureConn) SetDeadline(time.Time) error { return nil }
func (c *deadlineFailureConn) SetReadDeadline(time.Time) error {
	return errors.New("deadline unavailable")
}
func (c *deadlineFailureConn) SetWriteDeadline(time.Time) error { return nil }

type testNetAddr string

func (a testNetAddr) Network() string { return "test" }
func (a testNetAddr) String() string  { return string(a) }

func TestReadStartupReasonOutcomeDistinguishesAbsenceFaultDeadlineAndCancellation(t *testing.T) {
	t.Run("eof before prefix is no record", func(t *testing.T) {
		left := &eofStartupConn{reader: bytes.NewReader(nil)}
		got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "attempt", time.Now().Add(time.Second))
		if got.Kind != StartupReasonReadNoRecord || got.Phase != StartupReasonReadPhasePrefix ||
			got.Cause != StartupReasonReadCauseEOF || got.Fault != StartupReasonFaultNone || got.Bytes != 0 {
			t.Fatalf("EOF outcome = %+v, want empty-prefix no-record", got)
		}
	})

	t.Run("deadline with no prefix is distinct no record", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "attempt", time.Now().Add(20*time.Millisecond))
		if got.Kind != StartupReasonReadNoRecord || got.Cause != StartupReasonReadCauseDeadline ||
			got.Phase != StartupReasonReadPhasePrefix || got.Bytes != 0 {
			t.Fatalf("empty deadline outcome = %+v, want deadline no-record", got)
		}
	})

	t.Run("partial prefix EOF is fault", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		go func() { _, _ = right.Write([]byte{0, 0}); _ = right.Close() }()
		got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "attempt", time.Now().Add(time.Second))
		if got.Kind != StartupReasonReadChannelFault || got.Fault != StartupReasonFaultPartialPrefix ||
			got.Phase != StartupReasonReadPhasePrefix || got.Bytes != 2 {
			t.Fatalf("partial-prefix outcome = %+v", got)
		}
	})

	t.Run("partial prefix deadline is fault", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		go func() { _, _ = right.Write([]byte{0, 0}) }()
		got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "attempt", time.Now().Add(20*time.Millisecond))
		if got.Kind != StartupReasonReadChannelFault || got.Fault != StartupReasonFaultPartialPrefix ||
			got.Cause != StartupReasonReadCauseDeadline || got.Bytes != 2 {
			t.Fatalf("partial-prefix deadline outcome = %+v", got)
		}
	})

	t.Run("partial body deadline is fault", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		go func() {
			var prefix [4]byte
			binary.BigEndian.PutUint32(prefix[:], 8)
			_, _ = right.Write(prefix[:])
			_, _ = right.Write([]byte("{}"))
		}()
		got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "attempt", time.Now().Add(20*time.Millisecond))
		if got.Kind != StartupReasonReadChannelFault || got.Fault != StartupReasonFaultPartialBody ||
			got.Cause != StartupReasonReadCauseDeadline || got.Phase != StartupReasonReadPhaseBody || got.Bytes != 6 {
			t.Fatalf("partial-body deadline outcome = %+v", got)
		}
	})

	t.Run("owner cancellation is not channel fault", func(t *testing.T) {
		left, right := net.Pipe()
		defer right.Close()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan StartupReasonReadOutcome, 1)
		go func() {
			result <- ReadStartupReasonOutcome(ctx, left, "fixture", "attempt", time.Now().Add(time.Second))
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case got := <-result:
			if got.Kind != StartupReasonReadCancelled || got.Fault != StartupReasonFaultNone {
				t.Fatalf("owner cancellation outcome = %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("owner cancellation did not close the reader")
		}
	})

	t.Run("deadline installation failure closes without reading", func(t *testing.T) {
		conn := newDeadlineFailureConn()
		got := ReadStartupReasonOutcome(context.Background(), conn, "fixture", "attempt", time.Now().Add(time.Second))
		if got.Kind != StartupReasonReadChannelFault || got.Fault != StartupReasonFaultDeadlineInstall ||
			got.Phase != StartupReasonReadPhasePrefix {
			t.Fatalf("deadline-install outcome = %+v", got)
		}
		if conn.reads != 0 {
			t.Fatalf("read continued after SetReadDeadline failure: reads=%d", conn.reads)
		}
		select {
		case <-conn.closed:
		default:
			t.Fatal("connection was not closed after deadline setup failure")
		}
	})
}

func TestReadStartupReasonOutcomeClassifiesFrameAndAttributionFaults(t *testing.T) {
	valid := StartupReason{Version: 1, Service: "fixture", Attempt: "expected", Class: "permission", Reason: "safe reason"}
	validBody, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	wrongAttempt := valid
	wrongAttempt.Attempt = "opaque-other-token"
	wrongAttemptBody, err := json.Marshal(wrongAttempt)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		frame []byte
		fault StartupReasonReadFault
	}{
		{name: "wrong attempt", frame: frameStartupReason(t, wrongAttemptBody), fault: StartupReasonFaultAttribution},
		{name: "malformed json", frame: frameStartupReason(t, []byte("{bad")), fault: StartupReasonFaultMalformedRecord},
		{name: "oversize frame", frame: []byte{0, 0, 0x10, 0x01}, fault: StartupReasonFaultFrameLength},
		{name: "valid current attempt", frame: frameStartupReason(t, validBody), fault: StartupReasonFaultNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &eofStartupConn{reader: bytes.NewReader(tc.frame)}
			got := ReadStartupReasonOutcome(context.Background(), conn, "fixture", "expected", time.Now().Add(time.Second))
			if tc.fault == StartupReasonFaultNone {
				if got.Kind != StartupReasonReadRecord || got.ReasonClass != "permission" || got.Reason != "safe reason" {
					t.Fatalf("valid typed result = %+v", got)
				}
				return
			}
			if got.Kind != StartupReasonReadChannelFault || got.Fault != tc.fault {
				t.Fatalf("typed protocol fault = %+v, want %s", got, tc.fault)
			}
			if strings.Contains(got.Reason+got.Remedy, "opaque-other-token") {
				t.Fatalf("attribution token escaped typed fault: %+v", got)
			}
		})
	}
}

func frameStartupReason(t *testing.T, body []byte) []byte {
	t.Helper()
	frame := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	return append(frame, body...)
}

func TestReadStartupReasonOutcomeKeepsSafeRecordWithoutOpaqueAttemptToken(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go sendStartupFrame(t, right, StartupReason{Version: 1, Service: "fixture", Attempt: "opaque-secret-token",
		Class: "permission", Reason: "permission denied\x1b[31m", Remedy: "request the assigned permission set"})
	got := ReadStartupReasonOutcome(context.Background(), left, "fixture", "opaque-secret-token", time.Now().Add(time.Second))
	if got.Kind != StartupReasonReadRecord || got.ReasonClass != "permission" ||
		got.Reason != "permission denied [31m" || got.Remedy != "request the assigned permission set" {
		t.Fatalf("typed record outcome = %+v", got)
	}
	if strings.Contains(got.Reason+got.Remedy, "opaque-secret-token") {
		t.Fatal("opaque attempt token escaped the typed read result")
	}
}
