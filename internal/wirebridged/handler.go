package wirebridged

// handler.go is what the bridge serves (wire-bridge.md §4 and §5): exactly
// what Claude Code sends — POST /v1/messages, streamed or not — translated
// through internal/wirebridge against ONE upstream chat-completions endpoint
// fixed at boot. count_tokens refuses 404 (WB-D14 — no estimate, no zero-stub;
// the refusal is what sends claude to its own estimator). Everything else is
// 404 too: the bridge implements a surface, not the Anthropic API.
//
// Failure posture, all of it from §4/§5 and none of it invented here:
//   - a request that fails translation fails CLOSED — a 400 naming the
//     unrecognized block (WB-D5), never a silently-mistranslated request;
//   - an upstream 4xx is relayed same-status in the anthropic error shape
//     (claude renders it and decides — its retry loop IS the retry policy);
//   - an upstream 5xx, timeout or unreachable dial is a 502 in the same
//     shape; the bridge adds no retries of its own;
//   - the inbound Authorization header is ignored (WB-D4 — the jail is the
//     boundary), and the outbound key is never logged;
//   - one stderr line per request — method, path, status, duration — and
//     never a body.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

// upstreamTimeout is the ONE timeout the daemon adds beyond net/http's
// defaults (§5): ten minutes, sized for qwen's long agentic turns. Client-side
// timeouts are none — a jail-local dial never needs one, and claude owns the
// wall clock it is willing to wait out.
const upstreamTimeout = 10 * time.Minute

// NewHandler returns the bridge's http.Handler against one upstream
// chat-completions base URL with one bearer key. Exported so a test can
// construct the whole serving surface without touching the environment — the
// daemon's Main is a thin boot (route → key → bind → publish → Serve) around
// exactly this handler, built through newChatHandler with the options the boot
// route read off the provider. NewHandler takes the default ChatOptions, which
// ask a streamed request's upstream for its usage.
func NewHandler(upstreamBaseURL, apiKey string) http.Handler {
	return newChatHandler(upstreamBaseURL, apiKey, wirebridge.ChatOptions{})
}

// newChatHandler is NewHandler for an upstream whose ChatOptions are not the
// defaults (route.OmitStreamUsage, from the provider's declared
// supports_usage_in_streaming).
func newChatHandler(upstreamBaseURL, apiKey string, opts wirebridge.ChatOptions) http.Handler {
	return newHandler(upstreamBaseURL, "/chat/completions", apiKey,
		func(body []byte) ([]byte, error) { return wirebridge.TranslateRequestWith(body, opts) },
		wirebridge.TranslateResponse,
		func() streamTranslator { return wirebridge.NewStreamTranslator() })
}

// NewResponsesHandler is the Codex route's equivalent of NewHandler. It has a
// separate constructor so the upstream path and both translation directions
// are selected together; a Responses route can never accidentally inherit the
// chat-completions encoder.
func NewResponsesHandler(upstreamBaseURL, apiKey string) http.Handler {
	return newHandler(upstreamBaseURL, "/responses", apiKey,
		wirebridge.TranslateResponsesRequest, wirebridge.TranslateResponsesResponse,
		func() streamTranslator { return wirebridge.NewResponsesStreamTranslator() })
}

// NewCodexResponsesHandler obtains an access-only view for every request from
// the OpenAI credential service. The callback is intentionally here, at the
// network boundary: no bridge route ever writes an auth file or receives a
// refresh token, and a 401 gets exactly one new view before the error is
// relayed to Claude.
func NewCodexResponsesHandler(upstreamBaseURL, brokerEndpoint string) http.Handler {
	h := newHandler(upstreamBaseURL, "/responses", "",
		translateCodexResponsesRequest, wirebridge.TranslateResponsesResponse,
		func() streamTranslator { return wirebridge.NewResponsesStreamTranslator() }).(*bridgeHandler)
	h.accessToken = func() (string, string, error) {
		// The second argument is the credential service's own DIAGNOSTIC channel
		// (its stderr frames, forwarded as they arrive), and it used to be
		// io.Discard: every explanation the broker offered for a refused or
		// degraded token request was thrown away, leaving only the wrapped
		// "obtain OpenAI access-token view" error. It is a diagnostic stream, not
		// a credential one — the access token arrives on the stdout frame this
		// callback returns — so forwarding it to the daemon log leaks nothing and
		// is the difference between a diagnosable 401 and a mysterious one.
		view, err := openauthclient.RequestAccessToken(brokerEndpoint,
			logWriter("OpenAI credential service: "))
		if err != nil {
			return "", "", err
		}
		return view.Token, view.AccountID, nil
	}
	h.retryUnauthorized = true
	return h
}

// translateCodexResponsesRequest adapts the generic Responses request to the
// ChatGPT subscription endpoint. That endpoint rejects max_output_tokens;
// Claude's cap therefore cannot be expressed on this route and is omitted.
func translateCodexResponsesRequest(body []byte) ([]byte, error) {
	translated, err := wirebridge.TranslateResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(translated, &request); err != nil {
		return nil, fmt.Errorf("decode translated Codex Responses request: %w", err)
	}
	delete(request, "max_output_tokens")
	out, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode Codex Responses request: %w", err)
	}
	return out, nil
}

// streamTranslator is one request's upstream-stream translation. End is called
// exactly once, when the upstream stream ends (the [DONE] sentinel, the body
// closing, or a read error), because a translator may be holding the close of
// the anthropic grammar for a usage chunk that has not come.
type streamTranslator interface {
	Chunk([]byte) ([]wirebridge.Event, error)
	End() ([]wirebridge.Event, error)
}

func newHandler(upstreamBaseURL, path, apiKey string,
	translateRequest func([]byte) ([]byte, error),
	translateResponse func([]byte) ([]byte, error), newStream func() streamTranslator) http.Handler {
	return &bridgeHandler{
		upstreamURL: strings.TrimSuffix(upstreamBaseURL, "/") + path,
		apiKey:      apiKey, client: &http.Client{Timeout: upstreamTimeout},
		translateRequest: translateRequest, translateResponse: translateResponse, newStream: newStream,
	}
}

type bridgeHandler struct {
	// upstreamURL is the full chat-completions URL — the boot-selected base
	// with /chat/completions appended (§4's row: the bridge POSTes
	// <openai-base>/chat/completions and dials nothing else, ever).
	upstreamURL       string
	apiKey            string
	client            *http.Client
	translateRequest  func([]byte) ([]byte, error)
	translateResponse func([]byte) ([]byte, error)
	newStream         func() streamTranslator
	accessToken       func() (token, accountID string, err error)
	retryUnauthorized bool
}

func (h *bridgeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		// THE STATUS IS WHAT THE BRIDGE INTENDED, NOT WHAT CLAUDE RECEIVED, and
		// those diverge whenever a body write fails: the old line logged "200"
		// for a response that never arrived, which is the discarded-error half of
		// this file's defect class reported as a success. statusRecorder now
		// keeps the first write error for every path (the JSON relay, the SSE
		// relay, the error renderer), so one line covers all of them.
		if rec.writeErr != nil {
			logf("%s %s %d %s — RESPONSE DELIVERY FAILED after %d bytes: %v (the status is what "+
				"the bridge intended to send; the client did not receive it)",
				r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond),
				rec.wrote, rec.writeErr)
			return
		}
		logf("%s %s %d %s", r.Method, r.URL.Path, rec.status,
			time.Since(start).Round(time.Millisecond))
	}()

	// count_tokens lands here (404, WB-D14), as does every method and path the
	// surface does not implement. One refusal message covers both, naming the
	// rule rather than staging a guess.
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		writeAnthropicError(rec, http.StatusNotFound, "not_found_error",
			"wire-bridge: only POST /v1/messages is served; count_tokens is refused so "+
				"claude uses its own estimator (wire-bridge.md WB-D14)")
		return
	}
	// The inbound Authorization header is IGNORED (WB-D4): r.Header is never
	// read for credentials. Whatever token claude's derive emits rides along
	// and is dropped here.

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(rec, http.StatusBadRequest, "invalid_request_error",
			"wire-bridge: reading request body: "+err.Error())
		return
	}
	// The stream flag is read off the ANTHROPIC body before translation: it
	// decides how the upstream's answer is relayed. TranslateRequest carries
	// the same flag into the openai body (stream passes through per §4), so
	// the upstream mode always matches the client's.
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe) // unparseable JSON fails in TranslateRequest with a better error

	translated, err := h.translateRequest(body)
	if err != nil {
		// FAIL CLOSED, naming what was not understood (WB-D5) — the 400 is the
		// design's own failure mode for a shape the table does not map.
		writeAnthropicError(rec, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	resp, err := h.doUpstream(r, translated)
	if err != nil {
		writeAnthropicError(rec, http.StatusBadGateway, "api_error",
			"wire-bridge: upstream unavailable: "+err.Error())
		return
	}
	if resp.StatusCode == http.StatusUnauthorized && h.retryUnauthorized {
		// A RETRY IS A DECISION, so it is reported: without this line a route
		// whose access-token view is refreshed on every single request looks
		// exactly like one that never refreshes, and the request line's duration
		// silently covers two upstream round trips.
		logf("upstream answered 401; requesting a fresh access-token view and retrying once "+
			"(%s)", h.upstreamURL)
		// The 401's body is discarded unread: nothing in it survives the retry —
		// the relay below reports the SECOND attempt's status, and the body of an
		// attempt that is about to be replaced would only be logged as noise. The
		// Close error itself has no consumer; what it would cost is a connection
		// this handler is done with either way.
		_ = resp.Body.Close()
		resp, err = h.doUpstream(r, translated)
		if err != nil {
			writeAnthropicError(rec, http.StatusBadGateway, "api_error", "wire-bridge: upstream unavailable: "+err.Error())
			return
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// Exactly one new view (NewCodexResponsesHandler's rule), so this is
			// where the route gives up. Saying so distinguishes "the credential
			// service handed back a stale token" from "the upstream rejects this
			// account", which the relayed 401 alone does not.
			logf("upstream answered 401 again with a freshly minted access-token view; relaying " +
				"the refusal (the bridge retries once, by design)")
		}
	}
	// The response body is closed on every exit path. A Close error on a body
	// whose useful bytes have already been relayed reports nothing actionable and
	// cannot be surfaced to the client, whose response is finished by then.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		h.relayUpstreamError(rec, resp)
		return
	}
	if probe.Stream {
		h.relayStream(rec, resp)
		return
	}

	upBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeAnthropicError(rec, http.StatusBadGateway, "api_error",
			"wire-bridge: reading upstream response: "+err.Error())
		return
	}
	translatedResp, err := h.translateResponse(upBody)
	if err != nil {
		// A 200 that is not openai-shaped is an upstream fault, not a client
		// one: the 502 family, anthropic-shaped (§5).
		writeAnthropicError(rec, http.StatusBadGateway, "api_error",
			"wire-bridge: upstream response did not translate: "+err.Error())
		return
	}
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusOK)
	// Discarded here and reported there: statusRecorder.Write keeps the error and
	// ServeHTTP's deferred line says the response was not delivered. There is
	// nothing else this site could do — the header is already on the wire.
	_, _ = rec.Write(translatedResp)
}

// doUpstream constructs a fresh request for each attempt. Reusing an HTTP
// request body after a 401 would turn the retry into an empty completion.
func (h *bridgeHandler) doUpstream(in *http.Request, translated []byte) (*http.Response, error) {
	key, accountID := h.apiKey, ""
	if h.accessToken != nil {
		var err error
		key, accountID, err = h.accessToken()
		if err != nil {
			return nil, fmt.Errorf("obtain OpenAI access-token view: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(in.Context(), http.MethodPost, h.upstreamURL, bytes.NewReader(translated))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if accountID != "" {
		// Subscription traffic is charged to the selected ChatGPT account, not
		// an API-key project. The id comes only from the access-only broker view.
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}
	var probe struct {
		Stream bool `json:"stream"`
	}
	// Discarded on purpose: `translated` is this process's own encoder output, so
	// a decode failure here would be an internal-consistency bug rather than
	// input, and the only thing riding on it is one request header. If it ever
	// did fail, the upstream answers non-streaming and the relay's
	// no-message_stop report (relayStream) is what says so.
	_ = json.Unmarshal(translated, &probe)
	if probe.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return h.client.Do(req)
}

// relayUpstreamError maps an upstream failure onto the anthropic error shape
// (§5): 4xx same-status — claude renders it and owns the retry decision — and
// 5xx as 502, the overload family claude backs off on. The upstream's own
// error message is extracted and forwarded (it goes to CLAUDE, which displays
// it) but never to the log, and a body that does not parse is replaced by a
// status line rather than quoted: an HTML error page in a JSON field helps
// nobody.
func (h *bridgeHandler) relayUpstreamError(rec *statusRecorder, resp *http.Response) {
	// The read error is discarded because it cannot change the outcome: whatever
	// bytes arrived are passed to upstreamErrorMessage, which falls back to a
	// status line for anything it cannot parse — a truncated body and an
	// unparseable one take the identical path. The status that IS the diagnosis
	// reaches the log through ServeHTTP's request line either way.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBody))
	status := resp.StatusCode
	if status >= 500 {
		// A DOWNGRADE, so it is named: claude's retry policy reads the status, and
		// an upstream 503 arriving at the client as 502 is a mapping a reader of
		// this log should not have to infer from §5.
		logf("upstream returned %d; relaying it as %d (5xx maps to the anthropic overload "+
			"family claude backs off on)", resp.StatusCode, http.StatusBadGateway)
		status = http.StatusBadGateway
	}
	writeAnthropicError(rec, status, "api_error", upstreamErrorMessage(body, resp.StatusCode))
}

func upstreamErrorMessage(body []byte, status int) string {
	var shape struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &shape) == nil && shape.Error.Message != "" {
		return shape.Error.Message
	}
	return fmt.Sprintf("wire-bridge: upstream returned %d", status)
}

// relayStream forwards the upstream SSE stream through the translator,
// event-for-event (§4's streaming rows). The FRAMING is entirely here — strip
// the "data: " prefix, recognize the [DONE] sentinel, feed one payload at a
// time to the StreamTranslator and write each Event.Format() verbatim —
// because the library is I/O-free by design. After the sentinel the relay
// stops reading, and the translator tolerates (drops) any straggler chunk a
// chatty provider sends after message_stop.
//
// ⚠ THE RELAY READS PAST THE FINISH CHUNK, and tells the translator when the
// stream ends. An upstream asked for stream_options.include_usage sends its
// usage in a chunk AFTER the finish_reason chunk, so the chat-completions
// translator holds message_delta + message_stop until that chunk arrives. When
// the stream ends first — [DONE], EOF, or a read error after the finish — the
// relay calls End, which closes the message with whatever usage was reported.
// Skipping End turns every usage-less upstream's finished answer into the
// truncation report below.
//
// ⚠ THE SCAN LOOP HAD TWO SILENT EXITS, and both produced the same symptom: a
// client waiting on a message_stop that was never coming, with nothing in any log
// and a request line reporting 200.
//
//   - bufio.Scanner's error was never read. A read failure mid-stream, or a
//     `data:` line over maxSSELine (ErrTooLong — 4 MiB, which one tool-call
//     argument blob can reach), ends the loop exactly like a clean finish.
//   - an upstream that closes early — a dropped connection, a provider that
//     stops without a finish_reason — ends it the same way.
//
// So the relay now tracks whether the translator ever emitted message_stop, which
// is the event that CLOSES the anthropic grammar (wirebridge's StreamTranslator
// emits it once a finish_reason and the usage have arrived, or at End after a
// finish_reason), and a stream that ends
// without one is reported to both audiences: an `error` event to the client, the
// only legal way to fail inside SSE, and a line naming the cause to the log.
func (h *bridgeHandler) relayStream(rec *statusRecorder, resp *http.Response) {
	rec.Header().Set("Content-Type", "text/event-stream")
	rec.Header().Set("Cache-Control", "no-cache")
	rec.WriteHeader(http.StatusOK)
	flusher, _ := rec.ResponseWriter.(http.Flusher)

	tr := h.newStream()
	sc := bufio.NewScanner(resp.Body)
	// Upstream data lines carry whole content deltas — tool-call arguments can
	// be tens of KB in one chunk — so the token ceiling is raised well past
	// bufio's 64 KiB default.
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	sawDone, sawStop, chunks := false, false, 0
	// write is the one path an event takes to the client. It reports false when
	// the client hung up: the recorder kept the error and ServeHTTP's request line
	// reports it, so returning is not a silent exit — there is simply nobody left
	// to tell.
	write := func(evs []wirebridge.Event) bool {
		for _, ev := range evs {
			if ev.Name == "message_stop" {
				sawStop = true
			}
			if _, err := rec.Write(ev.Format()); err != nil {
				return false
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // SSE comments, "event:" lines, blank separators
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			sawDone = true
			break
		}
		chunks++
		evs, err := tr.Chunk([]byte(payload))
		var lost *wirebridge.UsageLostError
		if errors.As(err, &lost) {
			// The chunk after the finish, where the usage would be, did not
			// decode. The answer is whole and evs closes it, so it is written
			// below like any other; what is lost is the usage, and closing with
			// zero usage silently is the class the no-usage report below exists
			// to end. Once per upstream, for the same reason as that report.
			logOnce("undecodable-stream-usage:"+h.upstreamURL, "upstream %s sent a stream chunk "+
				"after the finish that did not decode (%v), so the answer was closed without the "+
				"usage that chunk may have carried and Claude counts zero tokens for such turns; "+
				"reported once", h.upstreamURL, lost.Err)
			err = nil
		}
		if err != nil {
			// A chunk that does not decode is a mid-stream upstream fault:
			// close the stream with an anthropic error EVENT (the only legal
			// way to fail inside SSE), never a plain HTTP status.
			logf("upstream stream failed to translate at chunk %d of %s: %v — closing the "+
				"client's stream with an error event", chunks, h.upstreamURL, err)
			h.failStream(rec, flusher, "wire-bridge: upstream stream did not translate: "+err.Error())
			return
		}
		if !write(evs) {
			return
		}
	}
	scanErr := sc.Err()
	// The upstream stream has ended, whichever way it ended, so the translator
	// hears it: one that saw a finish_reason is holding message_stop for a usage
	// chunk, and End is where it stops waiting and closes the message. When End
	// emits anything, that is what it means — the answer finished and no usage
	// was ever reported.
	endEvs, endErr := tr.End()
	if endErr != nil {
		// End errs only when it had a message to close, so the client's stream is
		// still open and an error event is still the legal way to end it.
		logf("upstream stream could not be closed after %d chunks of %s: %v — closing the "+
			"client's stream with an error event", chunks, h.upstreamURL, endErr)
		h.failStream(rec, flusher, "wire-bridge: upstream stream did not translate: "+endErr.Error())
		return
	}
	if !write(endEvs) {
		return
	}
	closedWithoutUsage := len(endEvs) > 0
	if closedWithoutUsage && scanErr == nil {
		// Once per upstream, not per request: it is a fact about the upstream,
		// and it is the silent-zero class this whole change exists to end, so it
		// is not left silent either.
		logOnce("no-stream-usage:"+h.upstreamURL, "upstream %s finished a stream without "+
			"reporting its usage (it ignored stream_options.include_usage, or its provider "+
			"declares supports_usage_in_streaming \"false\" so the bridge did not ask), so Claude "+
			"counts zero tokens for such turns; reported once", h.upstreamURL)
	}
	if scanErr != nil {
		// A read failure or an over-long data: line. Either way the stream the
		// client is holding is INCOMPLETE unless it had already finished, and the
		// ceiling is worth naming because ErrTooLong is a configuration fact
		// rather than an upstream one.
		detail := scanErr.Error()
		if errors.Is(scanErr, bufio.ErrTooLong) {
			detail = fmt.Sprintf("a single upstream data: line exceeded the bridge's %d-byte "+
				"ceiling (maxSSELine): %v", maxSSELine, scanErr)
		}
		if closedWithoutUsage {
			detail += " — the answer had already finished, so it was closed without the usage " +
				"the upstream had not yet sent"
		}
		logf("upstream stream ended in an error after %d chunks (%s): %s", chunks, h.upstreamURL, detail)
		if !sawStop {
			h.failStream(rec, flusher, "wire-bridge: upstream stream ended in an error: "+detail)
		}
		return
	}
	if !sawStop {
		// The grammar never closed. This is the truncated-stream case that used
		// to leave claude waiting on a message_stop with no record anywhere of
		// why — the one shape a 200 request line actively misdescribes.
		logf("upstream stream ended after %d chunks without a finish_reason (sentinel [DONE] "+
			"%s, upstream %s), so no message_stop closed the anthropic stream; relaying an "+
			"error event rather than leaving the client waiting",
			chunks, map[bool]string{true: "seen", false: "absent"}[sawDone], h.upstreamURL)
		h.failStream(rec, flusher,
			"wire-bridge: upstream ended the stream before it completed (no finish_reason, so no "+
				"message_stop); the response above is partial")
	}
}

// failStream closes a streaming response the only way SSE allows: an anthropic
// `error` event, flushed. Its callers have already logged WHY — the event is what
// the client can act on, the log line is what a human can.
func (h *bridgeHandler) failStream(rec *statusRecorder, flusher http.Flusher, message string) {
	errEv := wirebridge.Event{Name: "error", Data: []byte(anthropicErrorJSON("api_error", message))}
	// Discarded and reported: statusRecorder keeps the error and ServeHTTP's line
	// says the delivery failed. A failure to deliver the failure has no remedy.
	_, _ = rec.Write(errEv.Format())
	if flusher != nil {
		flusher.Flush()
	}
}

const (
	// maxUpstreamErrorBody bounds how much of an error response is read before
	// message extraction — error bodies are small, and an unbounded read of a
	// wedged upstream is the memory story the timeouts above are supposed to
	// close.
	maxUpstreamErrorBody = 1 << 20
	// maxSSELine is one upstream data: line's ceiling.
	maxSSELine = 4 << 20
)

// statusRecorder captures the response status for the one-line request log,
// defaulting to 200 when a handler writes without an explicit WriteHeader.
// It is also the ONE place a failed body write is noticed. Every write in this
// file goes through it — the translated JSON response, each SSE event, the
// anthropic error renderer — and each of those call sites used to discard its
// error, so a client that hung up mid-response, or a broken pipe on the loopback,
// produced a log line claiming the status the bridge had chosen and no hint that
// the bytes never landed. Recording the FIRST error (and the byte count) here,
// rather than at each site, is what makes ServeHTTP's single line able to say so.
type statusRecorder struct {
	http.ResponseWriter
	status   int
	wroteHdr bool
	wrote    int
	writeErr error
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHdr {
		r.status = code
		r.wroteHdr = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHdr = true
	n, err := r.ResponseWriter.Write(b)
	r.wrote += n
	if err != nil && r.writeErr == nil {
		r.writeErr = err
	}
	return n, err
}

// writeAnthropicError renders the one error shape every failure takes:
// {"type":"error","error":{"type":...,"message":...}}. encoding/json sorts map
// keys, so the bytes are deterministic for tests and logs-adjacent eyeballs
// alike.
// It takes the *statusRecorder rather than an http.ResponseWriter so the write
// below cannot be aimed at an unrecorded writer: the discard is only safe because
// the recorder keeps the error for ServeHTTP's request line, and a plain
// ResponseWriter here would silently reinstate the defect.
func writeAnthropicError(w *statusRecorder, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(anthropicErrorJSON(typ, message)))
}

func anthropicErrorJSON(typ, message string) string {
	body, err := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    typ,
			"message": message,
		},
	})
	if err != nil {
		return `{"type":"error","error":{"type":"api_error","message":"wire-bridge: internal error"}}`
	}
	return string(body)
}
