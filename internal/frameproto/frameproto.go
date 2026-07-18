// Package frameproto is the frame protocol v1 used by unix-socket loophole
// daemons, ported from src/host_service.py. It is a frozen interop contract
// (seam #6): the wire format must stay byte-identical so a Go daemon and a
// Python client (or vice versa) interoperate during the mixed-era rollout.
//
// Wire format (docs/design/loophole-protocol.md):
//
//	request:  <4-byte BE length><length bytes of UTF-8 JSON>   (client-first)
//	response: repeated frames, each <1-byte stream_id><4-byte BE length><payload>
//	          stream_id 0=stdout, 1=stderr, 2=exit
//	          the exit frame payload is a single BE SIGNED int32 return code
//
// Note the header is ">BI" (unsigned byte, unsigned 32-bit length) but the
// EXIT payload is ">i" (SIGNED 32-bit) — a negative rc (e.g. a signal death)
// must round-trip. The journal bridge uses the SAME framing but DISTINCT
// stream IDs 1/2/3 (see paths / the journal daemon) — do not conflate.
//
// Source of truth: src/host_service.py.
package frameproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Protocol stream IDs.
const (
	ProtocolVersion = 1

	StreamStdout = 0
	StreamStderr = 1
	StreamExit   = 2
)

// ErrClosedBeforeRequest is returned by ReadRequest on a clean EOF before any
// complete request arrived (Python's _read_request returning None).
var ErrClosedBeforeRequest = errors.New("frameproto: connection closed before a request")

// ReadExact reads exactly n bytes, or returns ErrClosedBeforeRequest on a
// clean EOF before n bytes (mirrors host_service._read_exact returning None).
func ReadExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, ErrClosedBeforeRequest
		}
		return nil, err
	}
	return buf, nil
}

// ReadRequestBytes reads a single length-prefixed request frame and returns
// the raw JSON body bytes (undecoded). Mirrors the framing half of
// host_service._read_request; JSON decoding is the caller's job so it can
// choose an order-preserving decoder.
func ReadRequestBytes(r io.Reader) ([]byte, error) {
	header, err := ReadExact(r, 4)
	if err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	body, err := ReadExact(r, int(length))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// WriteRequest frames body as a request (4-byte BE length prefix + body) and
// writes it. This is the client side (yolo-ps, terminator).
func WriteRequest(w io.Writer, body []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// Frame is a single decoded response frame.
type Frame struct {
	StreamID byte
	Payload  []byte
}

// WriteFrame writes one response frame: ">BI" header (stream_id, length) then
// payload. Mirrors Session._send_frame.
func WriteFrame(w io.Writer, streamID byte, payload []byte) (int, error) {
	var hdr [5]byte
	hdr[0] = streamID
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	n, err := w.Write(hdr[:])
	if err != nil {
		return n, err
	}
	total := n
	if len(payload) > 0 {
		m, err := w.Write(payload)
		total += m
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// WriteExit writes the exit frame with a SIGNED int32 return code (">i"),
// mirroring Session.exit. rc is masked to 32 bits the same way struct.pack
// does (Python raises on out-of-range, but daemons only ever pass small codes).
func WriteExit(w io.Writer, rc int) (int, error) {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(int32(rc)))
	return WriteFrame(w, StreamExit, payload[:])
}

// ReadFrame reads one response frame (5-byte header + payload). Returns
// io.EOF when the stream ends cleanly on a frame boundary.
func ReadFrame(r io.Reader) (Frame, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{StreamID: header[0], Payload: payload}, nil
}

// ExitCode decodes an exit-frame payload as a signed int32 (">i").
func ExitCode(payload []byte) (int, error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("frameproto: exit payload is %d bytes, want 4", len(payload))
	}
	return int(int32(binary.BigEndian.Uint32(payload))), nil
}

// HandlerErrorText formats the stderr line a daemon emits when its handler
// raises, byte-identical to host_service._handle_one ("handler error: <e>\n").
func HandlerErrorText(err error) string {
	return "handler error: " + err.Error() + "\n"
}

// AccessLogLine formats the structured access-log line every request emits,
// byte-identical to host_service._handle_one's log.info format string.
// Operators grep this, so the field order and separators are frozen.
//
//	jail=%s keys=%s rc=%s elapsed_ms=%d bytes_out=%d
//
// keys is the comma-joined sorted request key list, or "-" when empty; rc is
// the string form of the return code, or "None" when the handler never set one
// (Python logs the literal None).
func AccessLogLine(jailID, keys string, rc *int, elapsedMs, bytesOut int) string {
	if keys == "" {
		keys = "-"
	}
	rcStr := "None"
	if rc != nil {
		rcStr = fmt.Sprintf("%d", *rc)
	}
	return fmt.Sprintf("jail=%s keys=%s rc=%s elapsed_ms=%d bytes_out=%d",
		jailID, keys, rcStr, elapsedMs, bytesOut)
}
