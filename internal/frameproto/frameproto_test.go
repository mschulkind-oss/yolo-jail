package frameproto

import (
	"bytes"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	body := []byte(`{"action":"ping","jail_id":"j"}`)
	var buf bytes.Buffer
	if err := WriteRequest(&buf, body); err != nil {
		t.Fatal(err)
	}
	// Wire: 4-byte BE length then body.
	if buf.Len() != 4+len(body) {
		t.Fatalf("framed len = %d, want %d", buf.Len(), 4+len(body))
	}
	got, err := ReadRequestBytes(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("round-trip body = %q, want %q", got, body)
	}
}

func TestFrameHeaderBytes(t *testing.T) {
	// A stdout frame carrying "hi": header 00 00 00 00 02, then "hi".
	var buf bytes.Buffer
	if _, err := WriteFrame(&buf, StreamStdout, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x00, 0x00, 0x00, 0x02, 'h', 'i'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("frame bytes = % x, want % x", buf.Bytes(), want)
	}
}

func TestExitSignedRoundTrip(t *testing.T) {
	for _, rc := range []int{0, 1, 2, 124, 255, -1, -15, 127} {
		var buf bytes.Buffer
		if _, err := WriteExit(&buf, rc); err != nil {
			t.Fatal(err)
		}
		f, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if f.StreamID != StreamExit {
			t.Errorf("exit stream id = %d, want %d", f.StreamID, StreamExit)
		}
		got, err := ExitCode(f.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if got != rc {
			t.Errorf("exit rc round-trip = %d, want %d (signed int32)", got, rc)
		}
	}
}

func TestExitNegativeIsSigned(t *testing.T) {
	// rc=-1 must be 0xFFFFFFFF (signed), not rejected — a signal death.
	var buf bytes.Buffer
	if _, err := WriteExit(&buf, -1); err != nil {
		t.Fatal(err)
	}
	want := []byte{StreamExit, 0x00, 0x00, 0x00, 0x04, 0xff, 0xff, 0xff, 0xff}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("exit -1 bytes = % x, want % x", buf.Bytes(), want)
	}
}

func TestHandlerErrorText(t *testing.T) {
	got := HandlerErrorText(errString("boom"))
	if got != "handler error: boom\n" {
		t.Errorf("HandlerErrorText = %q", got)
	}
}

func TestAccessLogLine(t *testing.T) {
	rc := 0
	got := AccessLogLine("jail-abc", "action,jail_id", &rc, 12, 345)
	want := "jail=jail-abc keys=action,jail_id rc=0 elapsed_ms=12 bytes_out=345"
	if got != want {
		t.Errorf("AccessLogLine = %q, want %q", got, want)
	}
	// Empty keys -> "-"; nil rc -> "None" (Python logs literal None).
	got = AccessLogLine("unknown", "", nil, 0, 0)
	want = "jail=unknown keys=- rc=None elapsed_ms=0 bytes_out=0"
	if got != want {
		t.Errorf("AccessLogLine empty = %q, want %q", got, want)
	}
}

func TestReadRequestCleanEOF(t *testing.T) {
	// Empty stream -> ErrClosedBeforeRequest (Python _read_request -> None).
	_, err := ReadRequestBytes(bytes.NewReader(nil))
	if err != ErrClosedBeforeRequest {
		t.Errorf("err = %v, want ErrClosedBeforeRequest", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
