// Package journald is the Go port of the builtin journal-bridge daemon
// (src/cli/loopholes_runtime.py: _journal_handle_client). It forwards an
// allowlisted `journalctl` invocation from the jail and streams its output back
// framed.
//
// Frozen contract (go-port plan Stage 7): the frame format is ">BI" (like the
// loophole protocol) but the stream IDs are DELIBERATELY 1=stdout, 2=stderr,
// 3=exit — distinct from frameproto v1's 0/1/2. Do NOT conflate them. Also
// frozen: the newline-terminated JSON request header, the arg validation
// (≤64 args, each ≤1024 bytes), the "user" mode --user prepend, and the exit
// codes (2 malformed/invalid, 127 journalctl-not-found, 1 spawn-failure).
//
// Source of truth: src/cli/loopholes_runtime.py.
package journald

import (
	"encoding/binary"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Journal stream IDs — 1/2/3, NOT frameproto's 0/1/2.
const (
	FrameStdout = 1
	FrameStderr = 2
	FrameExit   = 3

	MaxArgs   = 64
	MaxArgLen = 1024
)

// WriteFrame writes a journal frame: ">BI" header (stream, length) + payload.
// Mirrors _journal_send_frame.
func WriteFrame(w io.Writer, stream byte, payload []byte) error {
	hdr := make([]byte, 5)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// WriteExit writes the exit frame (signed int32 rc). Mirrors
// struct.pack(">i", rc) on FrameExit.
func WriteExit(w io.Writer, rc int) error {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(int32(rc)))
	return WriteFrame(w, FrameExit, payload[:])
}

// ValidatedArgs is the result of validating a journal request: the cleaned
// argv (with the "user"-mode --user prepend applied), or an error whose text +
// exit code the caller frames back verbatim.
type ValidatedArgs struct {
	Args     []string
	ErrText  string // stderr text to send if != ""
	ExitCode int    // exit code to send when ErrText != ""
}

// ParseRequest parses the newline-terminated JSON request header and validates
// args, applying the mode-specific --user prepend. Mirrors the validation half
// of _journal_handle_client. `header` is the bytes up to (not including) the
// first newline; mode is "user" or "full".
func ParseRequest(header []byte, mode string) ValidatedArgs {
	decoded, err := jsonx.Decode(header)
	if err != nil {
		return ValidatedArgs{ErrText: "yolo-journal: invalid JSON: " + err.Error() + "\n", ExitCode: 2}
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return ValidatedArgs{ErrText: "yolo-journal: invalid JSON: not an object\n", ExitCode: 2}
	}

	var rawArgs []any
	if v, ok := m.Get("args"); ok && v != nil {
		arr, isArr := v.([]any)
		if !isArr {
			return ValidatedArgs{
				ErrText:  "yolo-journal: args must be a list of ≤64 strings\n",
				ExitCode: 2,
			}
		}
		rawArgs = arr
	}
	if len(rawArgs) > MaxArgs {
		return ValidatedArgs{
			ErrText:  "yolo-journal: args must be a list of ≤64 strings\n",
			ExitCode: 2,
		}
	}
	clean := make([]string, 0, len(rawArgs))
	for _, a := range rawArgs {
		s, ok := a.(string)
		if !ok || len(s) > MaxArgLen {
			return ValidatedArgs{
				ErrText:  "yolo-journal: each arg must be a string under 1024 bytes\n",
				ExitCode: 2,
			}
		}
		clean = append(clean, s)
	}
	if mode == "user" {
		clean = append([]string{"--user"}, clean...)
	}
	return ValidatedArgs{Args: clean}
}
