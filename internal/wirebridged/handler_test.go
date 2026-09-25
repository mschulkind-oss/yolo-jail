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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
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

	requestWithSystemReminder := `{"model":"qwen-3.8-27b","max_tokens":64,"system":"be brief","messages":[{"role":"user","content":"hello"},{"role":"system","content":"follow the repository conventions"}]}`
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(requestWithSystemReminder))
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
	if len(translated.Messages) != 3 ||
		translated.Messages[0].Role != "system" || translated.Messages[0].Content != "be brief" ||
		translated.Messages[1].Role != "user" || translated.Messages[1].Content != "hello" ||
		translated.Messages[2].Role != "system" || translated.Messages[2].Content != "follow the repository conventions" {
		t.Errorf("system messages must preserve their expected positions: %+v", translated.Messages)
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

func TestResponsesNonStreamRoundTrip(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: `{"id":"resp_1","model":"terra","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi from responses"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewResponsesHandler(upSrv.URL, "test-key"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"terra","max_tokens":64,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"follow the repository conventions"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if up.gotPath != "/responses" || up.gotAuth != "Bearer test-key" {
		t.Fatalf("upstream route = %s auth = %q", up.gotPath, up.gotAuth)
	}
	var request struct {
		Model string `json:"model"`
		Max   int    `json:"max_output_tokens"`
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(up.gotBody, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "terra" || request.Max != 64 || len(request.Input) != 2 || request.Input[0].Type != "message" || request.Input[1].Type != "message" || request.Input[1].Role != "developer" || len(request.Input[1].Content) != 1 || request.Input[1].Content[0].Type != "input_text" || request.Input[1].Content[0].Text != "follow the repository conventions" {
		t.Fatalf("responses request = %s", up.gotBody)
	}
	if !strings.Contains(string(body), `"text":"hi from responses"`) {
		t.Fatalf("anthropic response = %s", body)
	}
}

func TestResponsesRouteAcceptsAdaptiveThinking(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "application/json", body: `{"id":"resp_1","model":"terra","status":"completed","output":[]}`}
	upSrv := httptest.NewServer(up.handler())
	defer upSrv.Close()
	srv := httptest.NewServer(NewResponsesHandler(upSrv.URL, "test-key"))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"terra","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var request struct {
		Reasoning json.RawMessage `json:"reasoning"`
	}
	if err := json.Unmarshal(up.gotBody, &request); err != nil {
		t.Fatal(err)
	}
	if request.Reasoning != nil {
		t.Fatalf("adaptive thinking must not pin an OpenAI reasoning effort: %s", up.gotBody)
	}
}

func TestTranslateCodexResponsesRequestOmitsSubscriptionUnsupportedFields(t *testing.T) {
	out, err := translateCodexResponsesRequest([]byte(`{"model":"gpt-5.6-terra","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, present := got["max_output_tokens"]; present {
		t.Fatalf("Codex subscription request retained unsupported max_output_tokens: %s", out)
	}
	if string(got["store"]) != "false" {
		t.Fatalf("Codex subscription request must set store=false: %s", out)
	}
}

// TestCodexResponsesLiveSmoke exercises the production subscription route
// without launching an agent. It is deliberately opt-in: it spends a small
// subscription request and needs the jail's access-only broker endpoint. The
// broker token remains in the handler process and never reaches test output.
func TestCodexResponsesLiveSmoke(t *testing.T) {
	if os.Getenv("YOLO_TEST_CODEX_RESPONSES") != "1" {
		t.Skip("set YOLO_TEST_CODEX_RESPONSES=1 to run the authenticated Codex Responses smoke test")
	}
	endpoint := os.Getenv(openauthclient.EndpointEnv)
	if endpoint == "" {
		t.Skipf("%s is not available", openauthclient.EndpointEnv)
	}
	srv := httptest.NewServer(NewCodexResponsesHandler(CodexResponsesBaseURL, endpoint))
	defer srv.Close()

	for _, model := range []string{"gpt-6-luna", "gpt-6-sol", "gpt-6-astra"} {
		t.Run(model, func(t *testing.T) {
			bodyJSON := `{"model":"` + model + `","max_tokens":64,"stream":true,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"Reply with OK."}]}`
			resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(bodyJSON))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.StatusCode, body)
			}
			if strings.Contains(string(body), "event: error") {
				t.Fatalf("bridge reported an upstream stream translation error: %s", body)
			}
			for _, want := range []string{"event: message_start", "event: message_stop"} {
				if !strings.Contains(string(body), want) {
					t.Fatalf("bridge stream is missing %q: %s", want, body)
				}
			}
		})
	}
	t.Run("hosted web search", func(t *testing.T) {
		bodyJSON := `{"model":"gpt-6-sol","max_tokens":64,"stream":true,"tools":[{"type":"web_search_20260318","name":"web_search"}],"messages":[{"role":"user","content":"Search the web for the current UTC offset of New York and answer in one sentence."}]}`
		resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(bodyJSON))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.StatusCode, body)
		}
		if strings.Contains(string(body), "event: error") {
			t.Fatalf("bridge reported an upstream hosted-web-search error: %s", body)
		}
		for _, want := range []string{"event: message_start", "event: message_stop"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("bridge stream is missing %q: %s", want, body)
			}
		}
	})
}

func TestResponsesRouteRetriesUnauthorizedOnceWithFreshAccessView(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer old" {
				t.Errorf("first token = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh" {
			t.Errorf("retried token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","model":"terra","status":"completed","output":[]}`)
	}))
	defer up.Close()
	h := newHandler(up.URL, "/responses", "", wirebridge.TranslateResponsesRequest, wirebridge.TranslateResponsesResponse,
		func() streamTranslator { return wirebridge.NewResponsesStreamTranslator() }).(*bridgeHandler)
	views := 0
	h.accessToken = func() (string, string, error) {
		views++
		if views == 1 {
			return "old", "acct", nil
		}
		return "fresh", "acct", nil
	}
	h.retryUnauthorized = true
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"terra","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || calls != 2 || views != 2 {
		t.Fatalf("status/calls/views = %d/%d/%d, want 200/2/2", resp.StatusCode, calls, views)
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

// usageAfterFinishStream is a recorded OpenAI chat-completions stream from an
// upstream that honored stream_options.include_usage: the finish_reason chunk
// carries no usage, and the usage arrives in one more chunk, "choices": [],
// before the [DONE] sentinel.
var usageAfterFinishStream = strings.Join([]string{
	`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}],"usage":null}`,
	"",
	`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
	"",
	`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":42,"total_tokens":1242,"prompt_tokens_details":{"cached_tokens":1000}}}`,
	"",
	"data: [DONE]",
	"",
}, "\n")

// sseEvents splits a relayed anthropic stream into (name, data) pairs, in order.
func sseEvents(t *testing.T, body string) []wirebridge.Event {
	t.Helper()
	var out []wirebridge.Event
	for _, frame := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		var ev wirebridge.Event
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = []byte(strings.TrimPrefix(line, "data: "))
			}
		}
		out = append(out, ev)
	}
	return out
}

// TestStreamedUsageReachesClaude is the end-to-end pin for the streaming-usage
// defect (docs/design/agent-footer.md, "Later: cost and failover"): the bridge
// ASKS for usage on a streamed chat-completions request, READS PAST the finish
// chunk to the usage chunk, and reports input, cache-read and output tokens in
// message_delta, the event whose usage Claude Code merges into its cost and
// context figures. Before the fix every one of those numbers reached Claude as 0.
func TestStreamedUsageReachesClaude(t *testing.T) {
	up := &stubUpstream{t: t, status: 200, contentType: "text/event-stream", body: usageAfterFinishStream}
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

	var sent struct {
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(up.gotBody, &sent); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	if sent.StreamOptions == nil || !sent.StreamOptions.IncludeUsage {
		t.Errorf("a streamed request must ask the upstream for usage "+
			"(stream_options.include_usage), or a compliant upstream never sends it: %s", up.gotBody)
	}

	evs := sseEvents(t, string(body))
	var names []string
	for _, ev := range evs {
		names = append(names, ev.Name)
	}
	if len(evs) < 2 || evs[len(evs)-2].Name != "message_delta" || evs[len(evs)-1].Name != "message_stop" {
		t.Fatalf("the stream must end message_delta, message_stop; got %v:\n%s", names, body)
	}
	var delta struct {
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal(evs[len(evs)-2].Data, &delta); err != nil {
		t.Fatalf("message_delta is not JSON: %v", err)
	}
	for field, want := range map[string]int{
		"input_tokens":            200, // prompt_tokens 1200 minus the 1000 it served from cache
		"cache_read_input_tokens": 1000,
		"output_tokens":           42,
	} {
		if got, ok := delta.Usage[field]; !ok || got != want {
			t.Errorf("message_delta usage %s = %d (present %v), want %d: %s",
				field, got, ok, want, evs[len(evs)-2].Data)
		}
	}
	if strings.Count(string(body), "event: message_stop") != 1 {
		t.Errorf("exactly one message_stop expected:\n%s", body)
	}
}

// TestServeCarriesTheRoutesStreamUsageFactToTheUpstream drives the PRODUCTION
// serve path, not the handler constructor: serve builds the chat handler from
// the boot route, so this is the test that fails if serve stops passing
// route.OmitStreamUsage (or asks for usage when told not to). One stub upstream
// per case records the streamed request body the bridge sent it.
func TestServeCarriesTheRoutesStreamUsageFactToTheUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		omit     bool
		wantSent bool
	}{
		{"the default asks for usage", false, true},
		{"a provider declaring supports_usage_in_streaming false is not asked", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noReadinessPipe(t)
			up := &stubUpstream{t: t, status: 200, contentType: "text/event-stream", body: usageAfterFinishStream}
			upSrv := httptest.NewServer(up.handler())
			defer upSrv.Close()
			endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
			old := EndpointFile
			EndpointFile = endpointFile
			t.Cleanup(func() { EndpointFile = old })

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan int, 1)
			go func() {
				done <- serve(ctx, route{ProviderName: "p", ListenAddr: "127.0.0.1:0",
					UpstreamBaseURL: upSrv.URL, OmitStreamUsage: tc.omit},
					entrypoint.NewEnv(map[string]string{"JAIL_HOME": t.TempDir()}))
			}()
			defer func() { cancel(); <-done }()
			var addr string
			waitFor(t, func() bool {
				b, err := os.ReadFile(endpointFile)
				addr = strings.TrimSpace(string(b))
				return err == nil && addr != ""
			})

			resp, err := http.Post("http://"+addr+"/v1/messages", "application/json",
				strings.NewReader(`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			var sent map[string]json.RawMessage
			if err := json.Unmarshal(up.gotBody, &sent); err != nil {
				t.Fatalf("upstream body not JSON: %v\n%s", err, up.gotBody)
			}
			if _, got := sent["stream_options"]; got != tc.wantSent {
				t.Errorf("stream_options sent = %v, want %v: %s", got, tc.wantSent, up.gotBody)
			}
		})
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
