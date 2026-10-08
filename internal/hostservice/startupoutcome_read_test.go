package hostservice

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestReadStartupReasonOutcomeReadsOneFrameUpToTheCap(t *testing.T) {
	body, _ := json.Marshal(StartupReason{Version: 1, Service: "fixture", Attempt: "attempt", Class: "internal", Reason: "safe"})
	second := frameStartupReason(t, []byte("not-json"))
	first := frameStartupReason(t, body)
	r := bytes.NewReader(append(first, second...))
	c := &eofStartupConn{reader: r}
	got := ReadStartupReasonOutcome(context.Background(), c, "fixture", "attempt", time.Now().Add(time.Second))
	if got.Kind != StartupReasonReadRecord || r.Len() != len(second) {
		t.Fatalf("one-frame observation changed: result=%+v remaining=%d", got, r.Len())
	}
	for _, n := range []int{StartupReasonMaxBytes, StartupReasonMaxBytes + 1} {
		full := append(append([]byte{}, body...), []byte(strings.Repeat(" ", n-len(body)))...)
		c := &eofStartupConn{reader: bytes.NewReader(frameStartupReason(t, full))}
		got := ReadStartupReasonOutcome(context.Background(), c, "fixture", "attempt", time.Now().Add(time.Second))
		if n == StartupReasonMaxBytes && got.Kind != StartupReasonReadRecord {
			t.Fatalf("4096-byte valid frame refused: %+v", got)
		}
		if n > StartupReasonMaxBytes && got.Fault != StartupReasonFaultFrameLength {
			t.Fatalf("4097-byte frame accepted: %+v", got)
		}
	}
}
func TestReadStartupReasonOutcomeTypesEveryInvalidRecord(t *testing.T) {
	valid := StartupReason{Version: 1, Service: "fixture", Attempt: "attempt", Class: "configuration", Reason: "safe"}
	for _, field := range []string{"version", "service", "attempt", "class", "reason", "unknown", "trailing", "partial-body"} {
		t.Run(field, func(t *testing.T) {
			record := valid
			want := StartupReasonFaultAttribution
			switch field {
			case "version":
				record.Version = 2
			case "service":
				record.Service = "other"
			case "attempt":
				record.Attempt = "other"
			case "class":
				record.Class = "open"
			case "reason":
				record.Reason = " \n"
				want = StartupReasonFaultEmptyReason
			}
			body, _ := json.Marshal(record)
			if field == "unknown" {
				body = append(body[:len(body)-1], []byte(",\"unknown\":true}")...)
				want = StartupReasonFaultMalformedRecord
			}
			if field == "trailing" {
				body = append(body, []byte("{}")...)
				want = StartupReasonFaultMalformedRecord
			}
			frame := frameStartupReason(t, body)
			if field == "partial-body" {
				frame = frame[:len(frame)-1]
				want = StartupReasonFaultPartialBody
			}
			got := ReadStartupReasonOutcome(context.Background(), &eofStartupConn{reader: bytes.NewReader(frame)}, "fixture", "attempt", time.Now().Add(time.Second))
			if got.Kind != StartupReasonReadChannelFault || got.Fault != want {
				t.Fatalf("typed invalid record %s = %+v want %s", field, got, want)
			}
		})
	}
}

// partialThenCancelledConn returns a few bytes of a frame, then cancels its owner's context and
// fails the way a connection the owner closed does — the teardown racing a partial frame.
type partialThenCancelledConn struct {
	eofStartupConn
	data   []byte
	cancel context.CancelFunc
}

func (c *partialThenCancelledConn) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		c.cancel()
		return 0, net.ErrClosed
	}
	n := copy(p, c.data)
	c.data = c.data[n:]
	return n, nil
}

// Owner cancellation is "nothing arrived" only before the first byte; a frame cut off by the
// owner's close is still a partial-frame fault.
func TestReadStartupReasonOutcomeCancellationNeverHidesAPartialFrame(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		kind StartupReasonReadKind
		want StartupReasonReadFault
	}{
		{"nothing", nil, StartupReasonReadCancelled, StartupReasonFaultNone},
		{"partial prefix", []byte{0, 0}, StartupReasonReadChannelFault, StartupReasonFaultPartialPrefix},
		{"partial body", []byte{0, 0, 0, 9, '{'}, StartupReasonReadChannelFault, StartupReasonFaultPartialBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &partialThenCancelledConn{data: tc.data, cancel: cancel}
			got := ReadStartupReasonOutcome(ctx, c, "fixture", "attempt", time.Now().Add(time.Second))
			if got.Kind != tc.kind || got.Fault != tc.want {
				t.Fatalf("%s = %+v, want %s/%s", tc.name, got, tc.kind, tc.want)
			}
		})
	}
}
