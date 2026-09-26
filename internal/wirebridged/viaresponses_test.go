package wirebridged

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// viaresponses_test.go pins the via route's OpenAI Responses wire
// (docs/design/wire-bridge-gateway.md §4.1, WG-I20): codex speaks only Responses, so a
// via route carries the provider's Responses endpoint beside its chat-completions one,
// and each request goes to the upstream its path names. Every test here runs the real
// daemon (servePlan over viaRoutesFor's plan, the production mux, split and pass-through)
// against a stubbed network.

const viaResponsesProviders = `{
  "router": {"api_key_env_name": "ROUTER_API_KEY", "endpoints": {
    "openai": {"base_url": "https://router.example/api/v1", "wire_api": "openai-responses"}}},
  "bedresp": {"endpoints": {
    "openai": {"base_url": "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1", "wire_api": "openai-responses"}}},
  "both": {"api_key_env_name": "BOTH_KEY", "endpoints": {
    "openai": {"base_url": "https://chat.both.example/v1", "wire_api": "openai-chat-completions"},
    "openai-responses": {"base_url": "https://resp.both.example/v1", "wire_api": "openai-responses"}}},
  "zai": {"api_key_env_name": "ZAI_API_KEY", "endpoints": {
    "openai": {"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}}},
  "openai-codex": {"endpoints": {
    "openai-responses": {"base_url": "https://chatgpt.com/backend-api/codex", "wire_api": "openai-responses"}}}
}`

func viaResponsesResolved(base string) map[string]packload.ResolvedProfile {
	return map[string]packload.ResolvedProfile{
		"pr":   {Provider: "router", Via: ServiceName, ViaBase: base},
		"pbr":  {Provider: "bedresp", Via: ServiceName, ViaBase: base},
		"pb":   {Provider: "both", Via: ServiceName, ViaBase: base},
		"pz":   {Provider: "zai", Via: ServiceName, ViaBase: base},
		"psub": {Provider: "openai-codex", Via: ServiceName, ViaBase: base},
	}
}

// startResponsesPlan serves the via plan viaRoutesFor builds for useProfiles, with the
// key channel holding every credential the fixtures name, and returns the via address.
func startResponsesPlan(t *testing.T, useProfiles map[string]string) string {
	t.Helper()
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	for _, v := range []string{"ROUTER_API_KEY", "BOTH_KEY", "ZAI_API_KEY"} {
		t.Setenv(v, "")
	}
	home := t.TempDir()
	writeKeyChannel(t, home,
		"export ROUTER_API_KEY=${ROUTER_API_KEY:-'router-key'}",
		"export BOTH_KEY=${BOTH_KEY:-'both-key'}",
		"export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}",
		"export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIDRESP'}",
		"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'resp-secret'}")
	addr := freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaResponsesProviders), useProfiles, viaResponsesResolved("http://"+addr))
	if len(vp.Routes) == 0 {
		t.Fatalf("viaRoutesFor served nothing for %v: %v", useProfiles, vp.Skipped)
	}
	startPlan(t, plan{via: vp}, home)
	return addr
}

const codexResponsesBody = `{"model":"openai/gpt-oss-120b","instructions":"be brief","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true,"store":false}`

// TestViaServesCodexResponsesUnderItsPrefix is the route's headline: codex's POST to
// <via>/agent/codex/responses reaches the provider's Responses endpoint plus /responses,
// the body byte-identical, the provider's key as the bearer and never the agent's, and
// no SigV4 on a non-Bedrock host.
func TestViaServesCodexResponsesUnderItsPrefix(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})

	resp, body := postTo(t, "http://"+addr+"/agent/codex/responses", codexResponsesBody,
		map[string]string{"Authorization": "Bearer agents-own-key", "Accept": "text/event-stream"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codex: %d %s", resp.StatusCode, body)
	}
	if up.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.calls())
	}
	got := up.requests[0]
	if got.URL.String() != "https://router.example/api/v1/responses" {
		t.Errorf("upstream = %s, want the provider's Responses base + /responses", got.URL)
	}
	if string(up.bodies[0]) != codexResponsesBody {
		t.Errorf("body changed:\n got %s\nwant %s", up.bodies[0], codexResponsesBody)
	}
	if a := got.Header.Get("Authorization"); a != "Bearer router-key" {
		t.Errorf("Authorization = %q, want the provider's key (never the agent's)", a)
	}
	if got.Header.Get("X-Amz-Date") != "" {
		t.Errorf("a non-Bedrock upstream was signed: %v", got.Header)
	}
	if got.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("Accept = %q, want the agent's", got.Header.Get("Accept"))
	}
}

// TestWillServeForACodexResponsesRouteAlone: the launcher's witness registration asks the
// same plan, so a jail whose only via route is codex's Responses route must say yes.
func TestWillServeForACodexResponsesRouteAlone(t *testing.T) {
	providers := mustProviders(t, viaResponsesProviders)
	resolved := viaResponsesResolved("http://127.0.0.1:8216")
	if !WillServe(providers, map[string]string{"codex": "pr"}, resolved) {
		t.Error("WillServe = false for a jail whose only route is codex's Responses route")
	}
	if WillServe(providers, map[string]string{"codex": "psub"}, resolved) {
		t.Error("WillServe = true for a via profile on the ChatGPT subscription, which no via route serves")
	}
}

// TestViaResponsesToBedrockIsSigned: a Responses upstream on an exact bedrock-runtime
// host carries a SigV4 signature for its own region that verifies against exactly what
// was sent, and no bearer.
func TestViaResponsesToBedrockIsSigned(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"codex": "pbr"})

	if resp, body := postTo(t, "http://"+addr+"/agent/codex/responses", codexResponsesBody,
		map[string]string{"Authorization": "Bearer agents-own-key"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("codex: %d %s", resp.StatusCode, body)
	}
	if up.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.calls())
	}
	r, sent := up.requests[0], up.bodies[0]
	if r.URL.String() != "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1/responses" {
		t.Errorf("upstream = %s", r.URL)
	}
	if string(sent) != codexResponsesBody {
		t.Errorf("body changed")
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDRESP/") || !strings.Contains(auth, "/us-west-2/bedrock/aws4_request") {
		t.Fatalf("Authorization = %q, want SigV4 for bedrock in us-west-2", auth)
	}
	when, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		t.Fatal(err)
	}
	check, _ := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(sent))
	for k, vs := range r.Header {
		if k != "Authorization" && k != "X-Amz-Date" {
			check.Header[k] = vs
		}
	}
	if err := sigv4.Sign(check, sent, sigv4.Credentials{AccessKeyID: "AKIDRESP", SecretAccessKey: "resp-secret"},
		sigv4.Options{Region: "us-west-2", Service: sigv4.BedrockService, Time: when}); err != nil {
		t.Fatal(err)
	}
	if check.Header.Get("Authorization") != auth {
		t.Errorf("the Responses route's signature does not verify against what was sent")
	}
}

// TestViaResponsesStreamIsFlushedThroughTheListener: a Responses event stream reaches the
// agent byte for byte, and each chunk is flushed as it arrives — the first event is read
// by the client BEFORE the upstream sends the second, over a real socket, so a handler
// that buffered would time out here.
func TestViaResponsesStreamIsFlushedThroughTheListener(t *testing.T) {
	up := withUpstream(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr}
	}}
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})

	const ev1 = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	const ev2 = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"

	type result struct {
		contentType, first, rest string
		err                      error
	}
	firstSeen := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/agent/codex/responses", strings.NewReader(codexResponsesBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		var first strings.Builder
		for first.Len() < len(ev1) {
			line, err := br.ReadString('\n')
			first.WriteString(line)
			if err != nil {
				done <- result{err: err}
				return
			}
		}
		close(firstSeen)
		rest, err := io.ReadAll(br)
		done <- result{contentType: resp.Header.Get("Content-Type"), first: first.String(), rest: string(rest), err: err}
	}()

	if _, err := io.WriteString(pw, ev1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstSeen:
	case r := <-done:
		t.Fatalf("the stream ended before its first event was read: %+v", r)
	case <-time.After(5 * time.Second):
		t.Fatal("the first event did not reach the agent before the upstream sent the next: the route is not flushing")
	}
	if _, err := io.WriteString(pw, ev2); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.first+r.rest != ev1+ev2 {
		t.Errorf("stream changed:\n got %q\nwant %q", r.first+r.rest, ev1+ev2)
	}
	if r.contentType != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", r.contentType)
	}
}

// TestViaSendsEachWireToItsOwnUpstream: a provider declaring chat-completions and
// Responses at different addresses gets each request at the one its path names; a path
// naming neither goes to the chat-completions endpoint.
func TestViaSendsEachWireToItsOwnUpstream(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"codex": "pb", "pi": "pb"})

	for _, c := range []struct{ method, path, want string }{
		{http.MethodPost, "/agent/codex/responses", "https://resp.both.example/v1/responses"},
		{http.MethodPost, "/agent/pi/chat/completions", "https://chat.both.example/v1/chat/completions"},
		{http.MethodGet, "/agent/codex/models", "https://chat.both.example/v1/models"},
		{http.MethodPost, "/agent/codex/responses/compact", "https://resp.both.example/v1/responses/compact"},
	} {
		before := up.calls()
		req, _ := http.NewRequest(c.method, "http://"+addr+c.path, strings.NewReader(`{}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || up.calls() != before+1 {
			t.Fatalf("%s %s: status %d, calls %d", c.method, c.path, resp.StatusCode, up.calls()-before)
		}
		got := up.requests[before]
		if got.URL.String() != c.want {
			t.Errorf("%s %s went to %s, want %s", c.method, c.path, got.URL, c.want)
		}
		if a := got.Header.Get("Authorization"); a != "Bearer both-key" {
			t.Errorf("%s: Authorization = %q, want the provider's key", c.path, a)
		}
	}
}

// TestViaRefusesAWireTheProviderDoesNotOffer: a request whose path names a wire the
// provider did not declare is refused with an OpenAI-shaped 404 naming the provider and
// the mismatch, and nothing is sent to the provider's other endpoint — a via route
// passes a request through and never translates it.
func TestViaRefusesAWireTheProviderDoesNotOffer(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"codex": "pz", "pi": "pr"})

	for _, c := range []struct{ path, want string }{
		{"/agent/codex/responses", "provider zai"},
		{"/agent/pi/chat/completions", "provider router"},
	} {
		resp, body := postTo(t, "http://"+addr+c.path, `{}`, nil)
		var doc struct {
			Type  string `json:"type"`
			Error struct {
				Message, Type string
			} `json:"error"`
		}
		if resp.StatusCode != http.StatusNotFound || json.Unmarshal([]byte(body), &doc) != nil {
			t.Fatalf("%s: %d %s, want a 404 JSON error", c.path, resp.StatusCode, body)
		}
		if doc.Type != "" || doc.Error.Type != "invalid_request_error" || !strings.Contains(doc.Error.Message, c.want) ||
			!strings.Contains(doc.Error.Message, "never translates") {
			t.Errorf("%s: body %s, want an OpenAI-shaped error naming %s and the pass-through rule", c.path, body, c.want)
		}
	}
	if up.calls() != 0 {
		t.Errorf("upstream calls = %d, want none", up.calls())
	}
}

// TestViaResponsesUpstreamErrorPassesThrough: the provider's own error status and body
// reach codex unchanged (codex reads OpenAI's error body itself, e.g. for a usage limit).
func TestViaResponsesUpstreamErrorPassesThrough(t *testing.T) {
	up := withUpstream(t)
	const errBody = `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
	up.responses = []func() *http.Response{func() *http.Response {
		r := jsonResponse(http.StatusTooManyRequests, errBody)
		r.Header.Set("Retry-After", "7")
		return r
	}}
	addr := startResponsesPlan(t, map[string]string{"codex": "pr"})
	resp, body := postTo(t, "http://"+addr+"/agent/codex/responses", codexResponsesBody, nil)
	if resp.StatusCode != http.StatusTooManyRequests || body != errBody || resp.Header.Get("Retry-After") != "7" {
		t.Errorf("got %d %q (Retry-After %q), want the upstream's 429 and body unchanged",
			resp.StatusCode, body, resp.Header.Get("Retry-After"))
	}
}

// TestViaRefusesAPathItWouldClassifyAsOneWireAndServeAsAnother pins WG-I23: a path with a
// dot or empty segment, or an encoded '?' or '#', is refused with an OpenAI-shaped 404 and
// sends nothing upstream. Each case here was a live bypass: the split classified the path
// as written while the upstream normalized it, so a request refused as one wire reached
// the provider as another, reached its other endpoint, or climbed out of its declared
// base path — and the encoded '?' became a query separator.
func TestViaRefusesAPathItWouldClassifyAsOneWireAndServeAsAnother(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"pi": "pr", "codex": "pb", "opencode": "pz"})

	for _, p := range []string{
		"/agent/pi/x/../chat/completions",          // chat-completions to a Responses-only provider
		"/agent/pi/%2e%2e/api/v1/chat/completions", // the same, with the dots encoded
		"/agent/opencode/chat/completions%3Fa=1",   // an encoded '?' becoming a query
		"/agent/opencode/chat/completions%23frag",  // an encoded '#'
		"/agent/codex/x/../responses",              // Responses sent to the chat endpoint
		"/agent/codex//responses",                  // an empty segment the upstream collapses
		"/agent/codex/../../../etc",                // out of the provider's base path
		"/agent/codex/./responses",                 // a '.' segment
		"/agent/pi/../codex/responses",             // one agent's prefix reaching another's
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+p, strings.NewReader(`{}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var doc struct {
			Error struct{ Message, Type string } `json:"error"`
		}
		if resp.StatusCode != http.StatusNotFound || json.Unmarshal(b, &doc) != nil ||
			doc.Error.Type != "invalid_request_error" || !strings.Contains(doc.Error.Message, "is not a path a via route forwards") {
			t.Errorf("%s: %d %s, want the OpenAI-shaped non-canonical-path 404", p, resp.StatusCode, b)
		}
	}
	if n := up.calls(); n != 0 {
		t.Errorf("upstream calls = %d, want none; first went to %s", n, up.requests[0].URL)
	}
}

// TestViaForwardsTheEscapedPathItClassified: what the route forwards is the path it
// classified, re-encoded once (WG-I23) — an encoded '%' reaches the provider still
// encoded rather than being read as a second escape, a trailing '/' and the agent's query
// survive, and nothing is cleaned away.
func TestViaForwardsTheEscapedPathItClassified(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"codex": "pb"})

	for _, c := range []struct{ path, want string }{
		{"/agent/codex/responses/resp%25abc", "https://resp.both.example/v1/responses/resp%25abc"},
		{"/agent/codex/models/", "https://chat.both.example/v1/models/"},
		{"/agent/codex/responses?stream=true", "https://resp.both.example/v1/responses?stream=true"},
		{"/agent/codex/", "https://chat.both.example/v1/"},
	} {
		before := up.calls()
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+c.path, strings.NewReader(`{}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || up.calls() != before+1 {
			t.Fatalf("%s: %d %s (calls %d)", c.path, resp.StatusCode, b, up.calls()-before)
		}
		if got := up.requests[before].URL.String(); got != c.want {
			t.Errorf("%s went to %s, want %s", c.path, got, c.want)
		}
	}
}
