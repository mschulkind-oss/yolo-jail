package wirebridged

// handler_test.go is the §8 integration-tier shape at unit scale: an
// httptest stub upstream plays the chat-completions provider, NewHandler is
// constructed DIRECTLY (upstream URL + key in hand, no environment — that is
// why the constructor exists), and anthropic-shaped requests go in and
// anthropic-shaped bytes come out. No agent binary runs; no real API is
// dialed. This is the mutation-proof tier for the serving surface: delete the
// handler, the translation call, the bearer header or the status mapping and
// one of these goes red.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubUpstream is a chat-completions provider that records what the bridge
// dialed and answers with the given status/body/headers.
type stubUpstream struct {
	t               *testing.T
	gotPath         string
	gotAuth         string
	gotBody         []byte
	gotStreamAccept bool
	status          int
	contentType     string
	body            string
}

func (s *stubUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gotPath = r.URL.Path
		s.gotAuth = r.Header.Get("Authorization")
		_, s.gotStreamAccept = r.Header["Accept"]
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.t.Fatalf("stub reading request: %v", err)
		}
		s.gotBody = body
		w.Header().Set("Content-Type", s.contentType)
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	})
}

const anthropicReq = `{"model":"qwen-3.8-27b","max_tokens":64,
	"system":"be brief",
	"messages":[{"role":"user","content":"hello"}]}`

const openaiResp = `{"id":"c1","model":"qwen-3.8-27b",
	"choices":[{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`

func TestNonStreamRoundTrip(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()

	srv := httptest.NewServer(NewHandler(upSrv.URL, "test-key-123"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(anthropicReq))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200:\n%s", resp.StatusCode, body)
	}
	// The upstream received the TRANSLATED request at the translated path,
	// carrying the boot-read key as the bearer.
	if up.gotPath != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions", up.gotPath)
	}
	if up.gotAuth != "Bearer test-key-123" {
		t.Errorf("upstream Authorization = %q, want the boot-read key as a bearer", up.gotAuth)
	}
	var translated struct {
		Model    string `json:"model"`
		Stream   *bool  `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal(up.gotBody, &translated); err != nil {
		t.Fatalf("upstream body is not openai JSON: %v\n%s", err, up.gotBody)
	}
	if translated.Model != "qwen-3.8-27b" {
		t.Errorf("model must pass through verbatim, got %q", translated.Model)
	}
	if translated.MaxTokens != 64 {
		t.Errorf("max_tokens must map, got %d", translated.MaxTokens)
	}
	if len(translated.Messages) != 2 ||
		translated.Messages[0].Role != "system" || translated.Messages[0].Content != "be brief" ||
		translated.Messages[1].Role != "user" || translated.Messages[1].Content != "hello" {
		t.Errorf("system must flatten to the leading system message: %+v", translated.Messages)
	}
	if translated.Stream != nil && *translated.Stream {
		t.Errorf("a non-stream request must go upstream as non-stream")
	}
	// The answer came back anthropic-shaped.
	var out struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("response is not anthropic JSON: %v\n%s", err, body)
	}
	if out.Type != "message" || out.Role != "assistant" {
		t.Errorf("message envelope wrong: %s", body)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "text" || out.Content[0].Text != "hi there" {
		t.Errorf("content blocks wrong: %s", body)
	}
	if out.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn mapped from finish_reason stop", out.StopReason)
	}
}

func TestStreamRoundTrip(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "text/event-stream", body: strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()

	srv := httptest.NewServer(NewHandler(upSrv.URL, "k"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d:\n%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	// Event-for-event: message_start opens the anthropic grammar, the text
	// deltas carry the upstream content, message_stop closes it — each as a
	// full "event:"/"data:" pair.
	got := string(body)
	for _, want := range []string{
		"event: message_start\n",
		`"text_delta","text":"He"`,
		`"text_delta","text":"llo"`,
		"event: content_block_stop\n",
		`"stop_reason":"end_turn"`,
		"event: message_stop\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("streamed bytes missing %q:\n%s", want, got)
		}
	}
	// The upstream saw stream:true carried through the translated body and the
	// SSE accept hint.
	if !up.gotStreamAccept {
		t.Errorf("a streamed request should set Accept: text/event-stream upstream")
	}
	var translated struct {
		Stream *bool `json:"stream"`
	}
	if err := json.Unmarshal(up.gotBody, &translated); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	if translated.Stream == nil || !*translated.Stream {
		t.Errorf("the stream flag must pass through to the upstream body: %s", up.gotBody)
	}
	// [DONE] must end the relay — nothing anthropic-shaped is written after
	// message_stop.
	if strings.Count(got, "event: message_stop") != 1 {
		t.Errorf("exactly one message_stop expected:\n%s", got)
	}
}

// WB-D14: count_tokens refuses 404 — no estimate, no zero-stub — and so does
// everything else on the surface, including the wrong method.
func TestCountTokensAndEverythingElseRefused(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewHandler(upSrv.URL, "k"))
	defer srv.Close()

	for _, tc := range []struct {
		method, path string
	}{
		{"POST", "/v1/messages/count_tokens"},
		{"GET", "/v1/messages"},
		{"POST", "/v1/complete"},
		{"GET", "/healthz"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s %s = %d, want 404 (WB-D14: the refusal sends claude to its own estimator)", tc.method, tc.path, resp.StatusCode)
		}
		if strings.Contains(string(respBody), `"input_tokens"`) {
			t.Errorf("%s %s must not be answered with a token count: %s", tc.method, tc.path, respBody)
		}
	}
	if up.gotBody != nil {
		t.Errorf("a refused request must make NO upstream call")
	}
}

func TestUpstreamFailuresMapToAnthropicShapes(t *testing.T) {
	cases := []struct {
		name            string
		upstreamStatus  int
		upstreamBody    string
		wantStatus      int
		wantStatusInMsg bool
	}{
		{"4xx same status", 429, `{"error":{"message":"rate limited"}}`, 429, false},
		{"5xx becomes 502", 500, `{"error":{"message":"upstream exploded"}}`, 502, false},
		{"non-JSON 5xx becomes 502 with a status line", 503, `<html>boom</html>`, 502, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &stubUpstream{t: t, status: tc.upstreamStatus, contentType: "application/json", body: tc.upstreamBody}
			upSrv := httptest.NewServer(up.handler())
			defer upSrv.Close()
			srv := httptest.NewServer(NewHandler(upSrv.URL, "k"))
			defer srv.Close()

			resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
				strings.NewReader(anthropicReq))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d:\n%s", resp.StatusCode, tc.wantStatus, body)
			}
			var shape struct {
				Type  string `json:"type"`
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &shape); err != nil {
				t.Fatalf("error body is not the anthropic shape: %s", body)
			}
			if shape.Type != "error" || shape.Error.Type != "api_error" {
				t.Errorf("error envelope wrong: %s", body)
			}
			if !tc.wantStatusInMsg && shape.Error.Message != "rate limited" &&
				shape.Error.Message != "upstream exploded" && !strings.Contains(tc.upstreamBody, "boom") {
				t.Errorf("the upstream's own message should be forwarded when it parses: %s", body)
			}
			if tc.wantStatusInMsg && !strings.Contains(shape.Error.Message, "503") {
				t.Errorf("a non-JSON error body must degrade to a status line, got: %s", body)
			}
		})
	}
}

// WB-D5: an unknown block fails CLOSED — a 400 that NAMES the block, made to an
// upstream that is never dialed.
func TestUnknownBlockFailsClosedWith400(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewHandler(upSrv.URL, "k"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user",
			"content":[{"type":"mega_block","text":"mystery"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400:\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "mega_block") {
		t.Errorf("the 400 must name the unrecognized block (WB-D5): %s", body)
	}
	if up.gotBody != nil {
		t.Errorf("a fail-closed request must never reach the upstream")
	}
}

// WB-D4: the inbound Authorization header is ignored — a bogus token is served
// exactly like the real one, because the jail is the boundary and the bridge
// authenticates nothing.
func TestInboundAuthorizationIgnored(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewHandler(upSrv.URL, "k"))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/v1/messages", strings.NewReader(anthropicReq))
	req.Header.Set("Authorization", "Bearer definitely-not-the-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("a garbage inbound token must change nothing (WB-D4): %d", resp.StatusCode)
	}
	if up.gotAuth != "Bearer k" {
		t.Errorf("upstream Authorization = %q, want the boot-read key, never the inbound one", up.gotAuth)
	}
}

// A provider that names no credential serves upstream WITHOUT the header —
// the honest answer for a provider that declares none, never a guessed one.
func TestNoKeyMeansNoAuthorizationHeader(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: openaiResp}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewHandler(upSrv.URL, ""))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(anthropicReq))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if up.gotAuth != "" {
		t.Errorf("no credential declared: the header must be absent, got %q", up.gotAuth)
	}
}
