package wirebridged

// invoketranslate.go is the invoke route's TRANSLATION: Bedrock's own InvokeModel request for a
// model the provider's list declares another maker's, carried through the route's Messages
// translation to runtime's chat completions and answered in InvokeModel's own shapes. It is what
// keeps claude's everything profile (docs/design/bedrock-plumbing.md OQ-BR11, ruled 2026-09-24:
// every model on the list, Claude and GPT, in one session) on Claude Code's own Bedrock mode at the
// bridge (docs/design/model-lists-and-pickers.md OQ-MM6, ruled 2026-10-05): that mode sends
// Anthropic's request format for any model, which only an Anthropic model takes, so another maker's
// is translated here exactly as /v1/messages translates it.
//
// THE REQUEST. An InvokeModel body is the Messages request less its `model` and `stream`, which
// Bedrock carries in the path and the route, plus `anthropic_version` and `anthropic_beta`, which
// only Bedrock's Anthropic models read (SOURCED from Claude Code's gateway compatibility guide,
// https://code.claude.com/docs/en/llm-gateway-protocol). So the Messages request is the body with
// the path's model and the route's stream flag set and those two dropped (invokeAsMessages).
//
// THE ANSWER. A non-streamed one is Anthropic's message JSON, which is InvokeModel's answer for an
// Anthropic model too. A streamed one is AWS's binary event stream
// (`application/vnd.amazon.eventstream`): one `chunk` message per Anthropic event, its payload
// `{"bytes": <the event's JSON, base64>}`, which Claude Code's Bedrock client reads back into SSE
// by each event's `type` (read, not run, in the 2.1.290 binary). A failure before the stream is
// InvokeModel's error shape (writeAWSError) at the translation's status; one inside it, the `error`
// event SSE ends with, is an event-stream EXCEPTION message, which that client turns into an
// `error` event in turn. count-tokens is refused 404, as /v1/messages/count_tokens is (WB-D14).

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
)

// eventStreamContentType is AWS's event-stream media type, which InvokeModelWithResponseStream
// answers with and Claude Code's Bedrock client reads.
const eventStreamContentType = "application/vnd.amazon.eventstream"

// invokeStreamOp is the InvokeModel route that streams.
const invokeStreamOp = "invoke-with-response-stream"

// serveInvokeTranslated serves one InvokeModel request for id, vendor's model, through the route's
// Messages translation (the file comment).
func (h *bridgeHandler) serveInvokeTranslated(rec *statusRecorder, in *http.Request, id, op, vendor string, note *string) {
	*note = fmt.Sprintf(" (model %s: %s's, translated from Bedrock's %s to %s)", id, vendor, op, h.upstreamURL)
	if op == "count-tokens" {
		*note = fmt.Sprintf(" (model %s: %s's, translated, so count-tokens is refused)", id, vendor)
		writeAWSError(rec, http.StatusNotFound, "ResourceNotFoundException", fmt.Sprintf("wire-bridge: "+
			"count-tokens is not served for %s, %s's model, which the bridge translates, so claude uses its "+
			"own estimator (wire-bridge.md WB-D14)", id, vendor))
		return
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		writeAWSError(rec, http.StatusBadRequest, "ValidationException", "wire-bridge: reading request body: "+err.Error())
		return
	}
	messages, err := invokeAsMessages(body, id, op == invokeStreamOp)
	if err != nil {
		writeAWSError(rec, http.StatusBadRequest, "ValidationException", "wire-bridge: "+err.Error())
		return
	}
	tw := &invokeTranscoder{out: rec, header: http.Header{}}
	var inner string
	h.serveMessages(&statusRecorder{ResponseWriter: tw, status: http.StatusOK}, in, messages, &inner)
	tw.finish()
	*note += inner
}

// invokeAsMessages is the Messages request an InvokeModel body for id carries: the body with the
// path's model and the route's stream flag, and without the two fields only Bedrock reads.
func invokeAsMessages(body []byte, id string, stream bool) ([]byte, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil || req == nil {
		return nil, fmt.Errorf("the InvokeModel body for %s is not a JSON object", id)
	}
	delete(req, "anthropic_version")
	delete(req, "anthropic_beta")
	model, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	req["model"] = model
	if stream {
		req["stream"] = json.RawMessage("true")
	} else {
		delete(req, "stream")
	}
	return json.Marshal(req)
}

// The transcoder's modes, decided by the Messages flow's first WriteHeader.
const (
	transcodeUnset  = iota
	transcodeRelay  // a non-streamed answer: Anthropic's message JSON, relayed as written
	transcodeEvents // a streamed answer: SSE in, AWS's event stream out
	transcodeError  // a failure before any answer: held, and written in AWS's error shape at the end
)

// invokeTranscoder is the http.ResponseWriter the Messages flow writes to on the invoke route's
// translation: it re-frames each answer as InvokeModel's (the file comment) onto out, the
// request's own recorder.
type invokeTranscoder struct {
	out    *statusRecorder
	header http.Header
	mode   int
	status int
	buf    bytes.Buffer
}

func (w *invokeTranscoder) Header() http.Header { return w.header }

func (w *invokeTranscoder) WriteHeader(code int) {
	if w.mode != transcodeUnset {
		return
	}
	w.status = code
	ok := code >= 200 && code < 300
	switch {
	case ok && strings.HasPrefix(w.header.Get("Content-Type"), "text/event-stream"):
		w.mode = transcodeEvents
		w.out.Header().Set("Content-Type", eventStreamContentType)
		w.out.WriteHeader(code)
	case ok:
		w.mode = transcodeRelay
		if ct := w.header.Get("Content-Type"); ct != "" {
			w.out.Header().Set("Content-Type", ct)
		}
		w.out.WriteHeader(code)
	default:
		w.mode = transcodeError
	}
}

func (w *invokeTranscoder) Write(b []byte) (int, error) {
	if w.mode == transcodeUnset {
		w.WriteHeader(http.StatusOK)
	}
	switch w.mode {
	case transcodeRelay:
		return w.out.Write(b)
	case transcodeError:
		return w.buf.Write(b)
	}
	w.buf.Write(b)
	for {
		frame, rest, found := bytes.Cut(w.buf.Bytes(), []byte("\n\n"))
		if !found {
			return len(b), nil
		}
		msg := sseAsEventStream(frame)
		w.buf.Next(len(w.buf.Bytes()) - len(rest))
		if msg == nil {
			continue
		}
		if _, err := w.out.Write(msg); err != nil {
			return 0, err
		}
	}
}

// Flush flushes the request's own writer, so each event-stream message reaches the agent as the
// Messages flow flushes its event.
func (w *invokeTranscoder) Flush() {
	if f, ok := w.out.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish writes a held failure in InvokeModel's error shape: the anthropic error's message at its
// status, typed as AWS types that status.
func (w *invokeTranscoder) finish() {
	if w.mode != transcodeError {
		return
	}
	var shape struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := fmt.Sprintf("wire-bridge: the translation answered %d", w.status)
	if json.Unmarshal(w.buf.Bytes(), &shape) == nil && shape.Error.Message != "" {
		msg = shape.Error.Message
	}
	writeAWSError(w.out, w.status, awsErrorType(w.status), msg)
}

// awsErrorType is the error type InvokeModel answers status with.
func awsErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "UnrecognizedClientException"
	case status == http.StatusForbidden:
		return "AccessDeniedException"
	case status == http.StatusNotFound:
		return "ResourceNotFoundException"
	case status == http.StatusTooManyRequests:
		return "ThrottlingException"
	case status < 500:
		return "ValidationException"
	}
	return "ServiceUnavailableException"
}

// sseAsEventStream is one SSE event (its lines, without the blank line ending it) as an
// event-stream message: a `chunk` carrying the event's JSON, or, for the `error` event, a
// modelStreamErrorException carrying its message. nil for a frame with no data.
func sseAsEventStream(frame []byte) []byte {
	var name string
	var data []byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if v, ok := bytes.CutPrefix(line, []byte("event:")); ok {
			name = strings.TrimSpace(string(v))
		} else if v, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(v, []byte(" "))...)
		}
	}
	if len(data) == 0 {
		return nil
	}
	if name == "error" {
		var shape struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := "wire-bridge: the translated stream failed"
		if json.Unmarshal(data, &shape) == nil && shape.Error.Message != "" {
			msg = shape.Error.Message
		}
		payload, _ := json.Marshal(map[string]string{"message": msg})
		return eventStreamMessage([][2]string{{":message-type", "exception"},
			{":exception-type", "modelStreamErrorException"}, {":content-type", "application/json"}}, payload)
	}
	payload, _ := json.Marshal(struct {
		Bytes []byte `json:"bytes"`
	}{data})
	return eventStreamMessage([][2]string{{":event-type", "chunk"}, {":content-type", "application/json"},
		{":message-type", "event"}}, payload)
}

// eventStreamMessage frames one message of AWS's event stream: a prelude of the total length and
// the headers' length (big-endian uint32s) and its CRC32, the headers (each a one-byte name
// length, the name, type 7 for a string, a two-byte value length and the value), the payload,
// and the CRC32 of everything before it.
func eventStreamMessage(headers [][2]string, payload []byte) []byte {
	var hb []byte
	for _, h := range headers {
		hb = append(hb, byte(len(h[0])))
		hb = append(hb, h[0]...)
		hb = append(hb, 7)
		hb = binary.BigEndian.AppendUint16(hb, uint16(len(h[1])))
		hb = append(hb, h[1]...)
	}
	total := 12 + len(hb) + len(payload) + 4
	msg := make([]byte, 0, total)
	msg = binary.BigEndian.AppendUint32(msg, uint32(total))
	msg = binary.BigEndian.AppendUint32(msg, uint32(len(hb)))
	msg = binary.BigEndian.AppendUint32(msg, crc32.ChecksumIEEE(msg))
	msg = append(msg, hb...)
	msg = append(msg, payload...)
	return binary.BigEndian.AppendUint32(msg, crc32.ChecksumIEEE(msg))
}
