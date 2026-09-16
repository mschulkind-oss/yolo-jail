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
	"fmt"
	"io"
	"net/http"
	"os"
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
// exactly this handler.
func NewHandler(upstreamBaseURL, apiKey string) http.Handler {
	return newHandler(upstreamBaseURL, "/chat/completions", apiKey,
		wirebridge.TranslateRequest, wirebridge.TranslateResponse,
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
		view, err := openauthclient.RequestAccessToken(brokerEndpoint, io.Discard)
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

type streamTranslator interface {
	Chunk([]byte) ([]wirebridge.Event, error)
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
		fmt.Fprintf(os.Stderr, "wire-bridge: %s %s %d %s\n",
			r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
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
		_ = resp.Body.Close()
		resp, err = h.doUpstream(r, translated)
		if err != nil {
			writeAnthropicError(rec, http.StatusBadGateway, "api_error", "wire-bridge: upstream unavailable: "+err.Error())
			return
		}
	}
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBody))
	status := resp.StatusCode
	if status >= 500 {
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
// stops reading: the anthropic grammar is closed by message_stop, and the
// translator tolerates (drops) any straggler chunk a chatty provider sends.
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
			break
		}
		evs, err := tr.Chunk([]byte(payload))
		if err != nil {
			// A chunk that does not decode is a mid-stream upstream fault:
			// close the stream with an anthropic error EVENT (the only legal
			// way to fail inside SSE), never a plain HTTP status.
			errEv := wirebridge.Event{Name: "error",
				Data: []byte(anthropicErrorJSON("api_error", "wire-bridge: upstream stream did not translate: "+err.Error()))}
			_, _ = rec.Write(errEv.Format())
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		for _, ev := range evs {
			if _, err := rec.Write(ev.Format()); err != nil {
				return // claude hung up; stop relaying
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
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
type statusRecorder struct {
	http.ResponseWriter
	status   int
	wroteHdr bool
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
	return r.ResponseWriter.Write(b)
}

// writeAnthropicError renders the one error shape every failure takes:
// {"type":"error","error":{"type":...,"message":...}}. encoding/json sorts map
// keys, so the bytes are deterministic for tests and logs-adjacent eyeballs
// alike.
func writeAnthropicError(w http.ResponseWriter, status int, typ, message string) {
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
