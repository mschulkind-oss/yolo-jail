package wirebridged

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// via_test.go pins the via side of the bridge (docs/design/wire-bridge-gateway.md
// OQ-WG6/WG7, Part 3): per-agent pass-through routes on one declared address, told apart
// by /agent/<name>/, each with its own credential, the body forwarded unchanged.

const viaProviders = `{
  "zai": {"api_key_env_name": "ZAI_API_KEY", "endpoints": {
    "openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}}},
  "bed": {"endpoints": {
    "openai": {"base_url": "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"}}},
  "resp": {"endpoints": {
    "openai": {"base_url": "https://resp.example/v1", "wire_api": "openai-responses"}}},
  "anthro": {"endpoints": {
    "anthropic": {"base_url": "https://anthro.example", "wire_api": "anthropic"}}},
  "openai-codex": {"endpoints": {
    "openai-responses": {"base_url": "https://chatgpt.com/backend-api/codex", "wire_api": "openai-responses"}}},
  "both": {"api_key_env_name": "BOTH_KEY", "endpoints": {
    "openai": {"base_url": "https://chat.both.example/v1", "wire_api": "openai-chat-completions"},
    "openai-responses": {"base_url": "https://resp.both.example/v1", "wire_api": "openai-responses"}}},
  "bedresp": {"endpoints": {
    "openai": {"base_url": "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1", "wire_api": "openai-responses"}}}
}`

func viaResolved(base string) map[string]packload.ResolvedProfile {
	return map[string]packload.ResolvedProfile{
		"pz":     {Provider: "zai", Via: ServiceName, ViaBase: base},
		"pb":     {Provider: "bed", Via: ServiceName, ViaBase: base},
		"native": {Provider: "zai"},
		"other":  {Provider: "zai", Via: "some-other-service", ViaBase: base},
		"nobase": {Provider: "zai", Via: ServiceName},
		"presp":  {Provider: "resp", Via: ServiceName, ViaBase: base},
		"panth":  {Provider: "anthro", Via: ServiceName, ViaBase: base},
		"psub":   {Provider: "openai-codex", Via: ServiceName, ViaBase: base},
		"pboth":  {Provider: "both", Via: ServiceName, ViaBase: base},
		"pbr":    {Provider: "bedresp", Via: ServiceName, ViaBase: base},
	}
}

// TestViaRoutesForBuildsOneRoutePerViaAgent: a profile is a via route exactly when its
// resolved via is this service and the launch resolved the address; the upstream is the
// provider's own openai endpoint, and a Bedrock host gets a signing region.
func TestViaRoutesForBuildsOneRoutePerViaAgent(t *testing.T) {
	plan := viaRoutesFor(mustProviders(t, viaProviders),
		map[string]string{"pi": "pz", "opencode": "pb", "omp": "native", "x": "other"},
		viaResolved("http://127.0.0.1:8216"))
	if plan.ListenAddr != "127.0.0.1:8216" {
		t.Fatalf("ListenAddr = %q, want the via address", plan.ListenAddr)
	}
	if len(plan.Routes) != 2 {
		t.Fatalf("routes = %+v, want pi and opencode only (omp is native, x names another service)", plan.Routes)
	}
	byAgent := map[string]viaRoute{}
	for _, r := range plan.Routes {
		byAgent[r.Agent] = r
	}
	if r := byAgent["pi"]; r.Chat.BaseURL != "https://api.z.ai/api/paas/v4" || r.KeyEnvName != "ZAI_API_KEY" ||
		r.Chat.SignRegion != "" || r.Responses.BaseURL != "" {
		t.Errorf("pi route = %+v, want zai's chat-completions endpoint and no Responses upstream", r)
	}
	// bed's openai endpoint declares no wire_api, so it is both wires' upstream.
	if r := byAgent["opencode"]; r.Chat.SignRegion != "us-east-1" || r.Chat != r.Responses || r.KeyEnvName != "" {
		t.Errorf("opencode (Bedrock) route = %+v, want one signed upstream for both wires and no key", r)
	}
}

// TestViaRoutesForNamesWhatItSkips: every via profile that cannot be served is named,
// never silently dropped — including the ChatGPT subscription, whose credential a via
// route does not carry (WG-I21), and a provider offering neither OpenAI wire.
func TestViaRoutesForNamesWhatItSkips(t *testing.T) {
	plan := viaRoutesFor(mustProviders(t, viaProviders),
		map[string]string{"a": "nobase", "c": "panth", "codex": "psub"}, viaResolved("http://127.0.0.1:8216"))
	if len(plan.Routes) != 0 || plan.ListenAddr != "" {
		t.Fatalf("plan = %+v, want nothing to serve", plan)
	}
	joined := strings.Join(plan.Skipped, "\n")
	for _, want := range []string{"resolved no via_address", "declares no chat-completions or Responses endpoint",
		"openai-codex (via for codex) is the ChatGPT subscription"} {
		if !strings.Contains(joined, want) {
			t.Errorf("skip reasons lack %q:\n%s", want, joined)
		}
	}
}

// TestWillServeForAViaRouteAlone: the launcher's witness registration asks WillServe, so a
// jail whose only route is a via route must say yes (and a jail with none, no).
func TestWillServeForAViaRouteAlone(t *testing.T) {
	providers := mustProviders(t, viaProviders)
	if !WillServe(providers, map[string]string{"pi": "pz"}, viaResolved("http://127.0.0.1:8216")) {
		t.Error("WillServe = false for a jail whose only route is pi's via route")
	}
	if WillServe(providers, map[string]string{"pi": "native"}, viaResolved("http://127.0.0.1:8216")) {
		t.Error("WillServe = true for a jail with no via profile and no adapter route")
	}
}

// TestViaMuxRefusesAnUnknownPrefix: a request with no known /agent/<name>/ prefix is
// refused in OpenAI's error shape, never routed to a default (OQ-WG4).
func TestViaMuxRefusesAnUnknownPrefix(t *testing.T) {
	served := false
	mux := &viaMux{routes: map[string]http.Handler{"pi": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served = true
	})}}
	for _, path := range []string{"/chat/completions", "/pi/chat/completions", "/agent/", "/agent/nobody/chat/completions", "/agentpi/x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
		var doc struct {
			Error struct{ Message, Type string } `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || !strings.Contains(doc.Error.Message, "no via route") {
			t.Errorf("%s: body %q is not an OpenAI-shaped error naming the refusal", path, rec.Body)
		}
	}
	if served {
		t.Error("an unknown prefix reached a route")
	}
}

// TestViaMuxStripsThePrefix: the route sees only the remainder, which it appends to the
// upstream base.
func TestViaMuxStripsThePrefix(t *testing.T) {
	var got string
	mux := &viaMux{routes: map[string]http.Handler{"pi": http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
	})}}
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/agent/pi/chat/completions", nil))
	if got != "/chat/completions" {
		t.Errorf("route saw %q, want /chat/completions", got)
	}
}

func writeKeyChannel(t *testing.T, home string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userEnvFilePath(home), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func freeLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// startPlan runs servePlan with a private endpoint file and returns when it is published.
func startPlan(t *testing.T, p plan, home string) {
	t.Helper()
	noReadinessPipe(t)
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- servePlan(ctx, p, entrypoint.NewEnv(map[string]string{"JAIL_HOME": home})) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool {
		b, err := os.ReadFile(endpointFile)
		return err == nil && strings.TrimSpace(string(b)) != ""
	})
}

func postTo(t *testing.T, url, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestServePlanServesEveryViaRoute is the production serve path for two via routes at
// once: each prefix reaches its own upstream with its own credential, the body arrives
// byte-identical, the agent's own Authorization is never forwarded, a non-Bedrock
// upstream carries its bearer and a Bedrock one a SigV4 signature that verifies against
// exactly what was sent.
func TestServePlanServesEveryViaRoute(t *testing.T) {
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("ZAI_API_KEY", "")
	up := withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home,
		"export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}",
		"export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIDVIA'}",
		"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'via-secret'}")
	addr := freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaProviders),
		map[string]string{"pi": "pz", "opencode": "pb"}, viaResolved("http://"+addr))
	startPlan(t, plan{via: vp}, home)

	const piBody = `{"model":"glm-5.3","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	const ocBody = `{"model":"openai.gpt-oss-120b","messages":[{"role":"user","content":"yo"}]}`
	if resp, b := postTo(t, "http://"+addr+"/agent/pi/chat/completions?x=1", piBody,
		map[string]string{"Authorization": "Bearer agents-own-key"}); resp.StatusCode != 200 {
		t.Fatalf("pi: %d %s", resp.StatusCode, b)
	}
	if resp, b := postTo(t, "http://"+addr+"/agent/opencode/chat/completions", ocBody, nil); resp.StatusCode != 200 {
		t.Fatalf("opencode: %d %s", resp.StatusCode, b)
	}
	if up.calls() != 2 {
		t.Fatalf("upstream calls = %d, want 2", up.calls())
	}
	for i, r := range up.requests {
		switch r.URL.Host {
		case "api.z.ai":
			if r.URL.Path != "/api/paas/v4/chat/completions" || r.URL.RawQuery != "x=1" {
				t.Errorf("pi upstream = %s, want the provider base + the path remainder + query", r.URL)
			}
			if string(up.bodies[i]) != piBody {
				t.Errorf("pi body changed:\n got %s\nwant %s", up.bodies[i], piBody)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer zai-key" {
				t.Errorf("pi Authorization = %q, want the provider's key (never the agent's)", got)
			}
		case "bedrock-runtime.us-east-1.amazonaws.com":
			if string(up.bodies[i]) != ocBody {
				t.Errorf("opencode body changed")
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDVIA/") {
				t.Fatalf("opencode Authorization = %q, want SigV4", auth)
			}
			when, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
			if err != nil {
				t.Fatal(err)
			}
			check, _ := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(up.bodies[i]))
			for k, vs := range r.Header {
				if k != "Authorization" && k != "X-Amz-Date" {
					check.Header[k] = vs
				}
			}
			if err := sigv4.Sign(check, up.bodies[i], sigv4.Credentials{AccessKeyID: "AKIDVIA",
				SecretAccessKey: "via-secret"}, sigv4.Options{Region: "us-east-1",
				Service: sigv4.BedrockService, Time: when}); err != nil {
				t.Fatal(err)
			}
			if check.Header.Get("Authorization") != auth {
				t.Errorf("the via route's signature does not verify against what was sent")
			}
		default:
			t.Errorf("unexpected upstream %s", r.URL)
		}
	}
}

// TestAViaRouteWithNoCredentialIdlesAlone: a route whose key is missing answers with an
// OpenAI-shaped error naming the variable, while the other route still serves (WG7 (e)).
func TestAViaRouteWithNoCredentialIdlesAlone(t *testing.T) {
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("ZAI_API_KEY", "")
	up := withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home,
		"export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIDVIA'}",
		"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'via-secret'}")
	addr := freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaProviders),
		map[string]string{"pi": "pz", "opencode": "pb"}, viaResolved("http://"+addr))
	startPlan(t, plan{via: vp}, home)

	resp, body := postTo(t, "http://"+addr+"/agent/pi/chat/completions", `{}`, nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "ZAI_API_KEY") ||
		!strings.Contains(body, `"error"`) {
		t.Errorf("pi without its key: %d %s, want a 503 OpenAI error naming $ZAI_API_KEY", resp.StatusCode, body)
	}
	if resp, body := postTo(t, "http://"+addr+"/agent/opencode/chat/completions", `{}`, nil); resp.StatusCode != 200 {
		t.Errorf("opencode must still serve: %d %s", resp.StatusCode, body)
	}
	if up.calls() != 1 {
		t.Errorf("upstream calls = %d, want only opencode's", up.calls())
	}
}

// TestViaPassthroughStreamsUnchanged: an SSE response reaches the agent byte for byte,
// flushed as it arrives, with its content type.
func TestViaPassthroughStreamsUnchanged(t *testing.T) {
	up := withUpstream(t)
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\ndata: [DONE]\n\n"
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(sse))}
	}}
	h := newPassthroughHandler("pi", "https://api.z.ai/v4", "k", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"stream":true}`)))
	if rec.Body.String() != sse {
		t.Errorf("stream changed:\n got %q\nwant %q", rec.Body.String(), sse)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" || !rec.Flushed {
		t.Errorf("content type %q flushed %v, want text/event-stream and flushed",
			rec.Header().Get("Content-Type"), rec.Flushed)
	}
}

// TestViaPassthroughErrorsAreOpenAIShaped: an unreachable upstream and a credential
// failure both reach the agent as OpenAI errors (the agent speaks OpenAI, not Anthropic).
func TestViaPassthroughErrorsAreOpenAIShaped(t *testing.T) {
	old := upstreamTransport
	upstreamTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	})
	t.Cleanup(func() { upstreamTransport = old })
	h := newPassthroughHandler("pi", "https://api.z.ai/v4", "k", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{}`)))
	var doc struct {
		Error struct{ Message, Type string } `json:"error"`
	}
	if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &doc) != nil ||
		!strings.Contains(doc.Error.Message, "could not be reached") {
		t.Errorf("unreachable upstream: %d %s", rec.Code, rec.Body)
	}

	signer := &bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{Env: sigv4.Env{}}}
	h = newPassthroughHandler("pi", bedrockBase, "", signer)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error"`) ||
		strings.Contains(rec.Body.String(), `"type":"error"`) {
		t.Errorf("no credential: %d %s, want a 401 in OpenAI's shape", rec.Code, rec.Body)
	}
}

// stubStreamBody is an upstream response body the test drives: Read yields what the
// test writes to w, then the error it closes w with; Close reports that the handler is
// done with it (the handler closes the body on its way out, after any log line).
type stubStreamBody struct {
	r      *io.PipeReader
	closed chan struct{}
}

func (b *stubStreamBody) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *stubStreamBody) Close() error {
	close(b.closed)
	return b.r.Close()
}

func newStubStream() (*stubStreamBody, *io.PipeWriter) {
	pr, pw := io.Pipe()
	return &stubStreamBody{r: pr, closed: make(chan struct{})}, pw
}

func waitClosed(t *testing.T, b *stubStreamBody) {
	t.Helper()
	select {
	case <-b.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never finished with the upstream body")
	}
}

// TestViaAbortsAStreamItsUpstreamCutShort pins WG-I24's first half: an upstream that
// fails after the status line went out makes the agent's read FAIL, and the daemon log
// names the cut and its cause. Before it the agent read a clean end of stream, which a
// chat-completions client takes as a finished reply, and nothing was logged.
func TestViaAbortsAStreamItsUpstreamCutShort(t *testing.T) {
	logs := captureDiag(t)
	up := withUpstream(t)
	body, pw := newStubStream()
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	}}
	addr := startResponsesPlan(t, map[string]string{"pi": "pz"})
	const ev = "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"
	go func() {
		_, _ = io.WriteString(pw, ev)
		_ = pw.CloseWithError(fmt.Errorf("read tcp: connection reset by peer"))
	}()

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/agent/pi/chat/completions", strings.NewReader(`{"stream":true}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, rerr := io.ReadAll(resp.Body)
	if rerr == nil {
		t.Errorf("the agent read %q and then a clean end of stream; want a failed read, so a truncated reply is never taken as finished", got)
	}
	if string(got) != ev {
		t.Errorf("bytes before the cut = %q, want %q", got, ev)
	}
	waitClosed(t, body)
	if l := logs(); !strings.Contains(l, "via route for pi: upstream https://api.z.ai/api/paas/v4: the response stream ended early after") ||
		!strings.Contains(l, "connection reset by peer") {
		t.Errorf("the daemon log does not name the cut and its cause:\n%s", l)
	}
}

// TestViaIsSilentWhenTheAgentClosesAStream: the agent closing its own request mid-stream
// (an interrupted turn) is no fault, so it is neither aborted nor logged as one (WG-I24).
func TestViaIsSilentWhenTheAgentClosesAStream(t *testing.T) {
	logs := captureDiag(t)
	up := withUpstream(t)
	body, pw := newStubStream()
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	}}
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})
	go func() { _, _ = io.WriteString(pw, "event: response.created\ndata: {}\n\n") }()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/agent/codex/responses", strings.NewReader(codexResponsesBody))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.Body.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-up.requests[0].Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's close never reached the upstream request")
	}
	_ = pw.CloseWithError(fmt.Errorf("upstream request canceled"))
	waitClosed(t, body)
	if l := logs(); strings.Contains(l, "ended early") {
		t.Errorf("the agent's own close was logged as an upstream fault:\n%s", l)
	}
}

func withViaHeaderTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := viaHeaderTimeout
	viaHeaderTimeout = d
	t.Cleanup(func() { viaHeaderTimeout = old })
}

// TestViaBoundsTheWaitForResponseHeaders pins WG-I24's second half: an upstream that
// sends no response headers within viaHeaderTimeout gets the agent an OpenAI-shaped 504
// saying so, and the daemon log names it.
func TestViaBoundsTheWaitForResponseHeaders(t *testing.T) {
	logs := captureDiag(t)
	withViaHeaderTimeout(t, 50*time.Millisecond)
	old := upstreamTransport
	upstreamTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	t.Cleanup(func() { upstreamTransport = old })
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+addr+"/agent/codex/responses", "application/json", strings.NewReader(codexResponsesBody))
	if err != nil {
		t.Fatalf("the route never answered an upstream that sends no headers: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var doc struct {
		Error struct{ Message, Type string } `json:"error"`
	}
	if resp.StatusCode != http.StatusGatewayTimeout || json.Unmarshal(b, &doc) != nil || doc.Error.Type != "api_error" ||
		!strings.Contains(doc.Error.Message, "https://router.example/api/v1 sent no response headers in time (50ms)") {
		t.Errorf("got %d %s, want an OpenAI-shaped 504 naming the upstream and the bound", resp.StatusCode, b)
	}
	if l := logs(); !strings.Contains(l, "via route for codex: upstream https://router.example/api/v1 sent no response headers in time") {
		t.Errorf("the daemon log does not name the timeout:\n%s", l)
	}
}

// TestViaLetsAStreamOutliveTheHeaderTimeout: once headers arrive, the stream runs for as
// long as the upstream keeps sending (WG-I24). Before it the whole exchange sat under a
// ten-minute Client.Timeout, which net/http applies to reading the body too, so a long
// reply was cut mid-stream.
func TestViaLetsAStreamOutliveTheHeaderTimeout(t *testing.T) {
	withViaHeaderTimeout(t, 50*time.Millisecond)
	up := withUpstream(t)
	body, pw := newStubStream()
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	}}
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})
	const ev1 = "event: response.created\ndata: {}\n\n"
	const ev2 = "event: response.completed\ndata: {}\n\n"
	go func() {
		_, _ = io.WriteString(pw, ev1)
		time.Sleep(300 * time.Millisecond) // six times the header bound
		_, _ = io.WriteString(pw, ev2)
		_ = pw.Close()
	}()

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/agent/codex/responses", strings.NewReader(codexResponsesBody))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rerr != nil || string(got) != ev1+ev2 {
		t.Errorf("got %d %q (read error %v), want the whole stream past the header bound", resp.StatusCode, got, rerr)
	}
	// A whole-exchange Client.Timeout of ANY length cuts a stream that outlives it, and
	// ten minutes cannot be waited out in a unit test, so the production constructor's
	// client is checked for one as well.
	if c := newPassthroughHandler("codex", "https://router.example/api/v1", "", nil).client; c.Timeout != 0 {
		t.Errorf("the via pass-through's client has Timeout %s, which also bounds the body's read", c.Timeout)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestServePlanKeepsTheAdapterRouteBesideViaRoutes: claude's adapter route keeps the root
// of its own port while the via routes serve on theirs (WG7 (a)).
func TestServePlanKeepsTheAdapterRouteBesideViaRoutes(t *testing.T) {
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("CEREBRAS_API_KEY", "")
	up := withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home, "export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}",
		"export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'cb-key'}")
	adapterAddr, viaAddr := freeLoopback(t), freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaProviders), map[string]string{"pi": "pz"}, viaResolved("http://"+viaAddr))
	adapter := route{ProviderName: "cerebras", ListenAddr: adapterAddr,
		UpstreamBaseURL: "https://api.cerebras.ai/v1", KeyEnvName: "CEREBRAS_API_KEY"}
	startPlan(t, plan{adapter: &adapter, via: vp}, home)

	if resp, b := postTo(t, "http://"+adapterAddr+"/v1/messages",
		`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, nil); resp.StatusCode != 200 {
		t.Fatalf("adapter route: %d %s", resp.StatusCode, b)
	}
	if resp, b := postTo(t, "http://"+viaAddr+"/agent/pi/chat/completions", `{"model":"m"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("via route: %d %s", resp.StatusCode, b)
	}
	if up.calls() != 2 || up.requests[0].URL.Host != "api.cerebras.ai" || up.requests[1].URL.Host != "api.z.ai" {
		t.Fatalf("upstreams = %v", up.requests)
	}
	if got := up.requests[0].Header.Get("Authorization"); got != "Bearer cb-key" {
		t.Errorf("adapter credential = %q", got)
	}
}

// TestRunServesAViaRouteFromTheChannel is the whole boot path: the three tables arrive in
// the environment exactly as the launcher writes them (YOLO_PROFILES carrying the via
// pair under its reserved keys), run() decides, binds the via address and serves.
func TestRunServesAViaRouteFromTheChannel(t *testing.T) {
	noReadinessPipe(t)
	t.Setenv("ZAI_API_KEY", "")
	up := withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home, "export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}")
	addr := freeLoopback(t)
	// The launcher's own writer, so the daemon is measured against what the host emits.
	profiles, err := jsonx.DumpsCompact(packload.ProfilesWireTable(map[string]packload.ResolvedProfile{
		"pz": {Provider: "zai", Via: ServiceName, ViaBase: "http://" + addr},
	}))
	if err != nil {
		t.Fatal(err)
	}
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, entrypoint.NewEnv(map[string]string{
			"JAIL_HOME":         home,
			"YOLO_PROVIDERS":    viaProviders,
			"YOLO_PROFILES":     profiles,
			"YOLO_USE_PROFILES": `{"pi":"pz"}`,
		}), time.Hour)
	}()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool {
		b, err := os.ReadFile(endpointFile)
		return err == nil && strings.TrimSpace(string(b)) == addr
	})
	if resp, b := postTo(t, "http://"+addr+"/agent/pi/chat/completions", `{"model":"m"}`, nil); resp.StatusCode != 200 {
		t.Fatalf("via route through run(): %d %s", resp.StatusCode, b)
	}
	if up.calls() != 1 || up.requests[0].URL.Host != "api.z.ai" {
		t.Fatalf("upstream = %v", up.requests)
	}
}

// TestViaRoutesSignOnlyExactBedrockHosts: the SigV4 decision is the shared exact-host
// predicate, so a look-alike host carries no signer and gets the bearer path instead.
func TestViaRoutesSignOnlyExactBedrockHosts(t *testing.T) {
	for host, want := range map[string]string{
		"https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1":       "us-west-2",
		"https://bedrock-runtime.us-west-2.amazonaws.com.evil.example/v1": "",
		"https://evil.example/bedrock-runtime.us-west-2.amazonaws.com/v1": "",
		"https://api.z.ai/api/paas/v4":                                    "",
	} {
		providers := mustProviders(t, `{"p":{"endpoints":{"openai":{"base_url":"`+host+
			`","wire_api":"openai-chat-completions"}}}}`)
		plan := viaRoutesFor(providers, map[string]string{"pi": "pp"},
			map[string]packload.ResolvedProfile{"pp": {Provider: "p", Via: ServiceName, ViaBase: "http://127.0.0.1:8216"}})
		if len(plan.Routes) != 1 || plan.Routes[0].Chat.SignRegion != want {
			t.Errorf("%s: routes %+v, want sign region %q", host, plan.Routes, want)
		}
	}
}

// TestViaPassthroughForwardsWhatItIsGiven: the upstream path is the provider's base plus
// whatever remainder the agent sent — not a hard-coded chat path — the body crosses byte
// for byte (whitespace included), and on a route with NO credential of its own the
// agent's Authorization is still not forwarded (WB-D4): nothing overwrites it there, so
// this is the case that proves the header is dropped rather than replaced.
func TestViaPassthroughForwardsWhatItIsGiven(t *testing.T) {
	up := withUpstream(t)
	h := newPassthroughHandler("pi", "http://127.0.0.1:8080/v1", "", nil)
	const body = "  {\"model\": \"qwen\"}\n"
	req := httptest.NewRequest(http.MethodPost, "/embeddings?dims=8", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer local")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || up.calls() != 1 {
		t.Fatalf("status %d calls %d", rec.Code, up.calls())
	}
	got := up.requests[0]
	if got.URL.String() != "http://127.0.0.1:8080/v1/embeddings?dims=8" {
		t.Errorf("upstream URL = %s, want the base + the remainder + the query", got.URL)
	}
	if string(up.bodies[0]) != body {
		t.Errorf("body changed: %q, want %q", up.bodies[0], body)
	}
	if a := got.Header.Get("Authorization"); a != "" {
		t.Errorf("the agent's Authorization was forwarded: %q", a)
	}
	if ct := got.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestABedrockViaRouteWithNoCredentialIdlesAlone is the idle test's other half: the
// Bedrock route, with no AWS credential source, idles naming what it needs, and the
// bearer route beside it still serves.
func TestABedrockViaRouteWithNoCredentialIdlesAlone(t *testing.T) {
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("ZAI_API_KEY", "")
	up := withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home, "export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}")
	addr := freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaProviders),
		map[string]string{"pi": "pz", "opencode": "pb"}, viaResolved("http://"+addr))
	startPlan(t, plan{via: vp}, home)

	resp, body := postTo(t, "http://"+addr+"/agent/opencode/chat/completions", `{}`, nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "via route for opencode") ||
		!strings.Contains(body, "AWS_ACCESS_KEY_ID") {
		t.Errorf("opencode without AWS: %d %s, want a 503 naming the route and its sources", resp.StatusCode, body)
	}
	if resp, body := postTo(t, "http://"+addr+"/agent/pi/chat/completions", `{}`, nil); resp.StatusCode != 200 {
		t.Errorf("pi must still serve: %d %s", resp.StatusCode, body)
	}
	if up.calls() != 1 || up.requests[0].URL.Host != "api.z.ai" {
		t.Errorf("upstream = %v, want only pi's", up.requests)
	}
}
