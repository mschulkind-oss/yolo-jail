package wirebridged

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// The via route's Converse pass-through (viaconverse.go; docs/design/wire-bridge-gateway.md
// WG-I36, WG-I48): Bedrock's own Converse API as pi's AWS SDK sends it to its via route on the
// shipped `bedrock-bridge`, signed and passed through to bedrock-runtime. Every test boots the
// production serve path (planFor over the shipped packs, then servePlan), so deleting the route's
// call sites fails them. No request leaves the process: upstreamTransport answers every one.

// converseBody is a Converse request as pi sends it: no model in the body, the path names it.
const converseBody = `{"messages":[{"role":"user","content":[{"text":"hi"}]}],"inferenceConfig":{"maxTokens":8}}`

// postConverse sends one Converse request to the via route as pi's SDK does: the caller token as a
// bearer, the SDK's own headers, and the path exactly as given (already escaped).
func postConverse(t *testing.T, client *http.Client, addr, escapedPath string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+escapedPath, strings.NewReader(converseBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testCallerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-User-Agent", "aws-sdk-js/3.1127.0")
	req.Header.Set("Amz-Sdk-Invocation-Id", "inv-1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading Converse response: %v", err)
	}
	return resp, string(b)
}

// awsError decodes an AWS-shaped error: its type header and JSON message.
func awsError(t *testing.T, resp *http.Response, body string) (typ, msg string) {
	t.Helper()
	var doc struct{ Message string }
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Errorf("the error body %q is not AWS's JSON shape: %v", body, err)
	}
	return resp.Header.Get("X-Amzn-Errortype"), doc.Message
}

// TestPisConverseStreamIsSignedAndPassedThroughToRuntime is the headline: pi on the shipped
// bedrock-bridge, its region ap-southeast-2, sends ConverseStream for a Claude model to its via
// route, and it reaches runtime's own Converse route in that region with the model id encoded as
// the SDK encoded it, signed for that region with pi's own pair, the body unchanged and the caller
// token not forwarded; the serve log names the Converse upstream. It fails if the route stops
// classifying Converse (the request would go to runtime's OpenAI base) or stops building the
// pass-through (the route would refuse Converse as a non-Bedrock one).
func TestPisConverseStreamIsSignedAndPassedThroughToRuntime(t *testing.T) {
	up, _, via, logs := servedShippedBedrockBridge(t, "eu-west-1", "ap-southeast-2")
	resp, body := postConverse(t, bridgeClient, via, "/agent/pi/model/us.anthropic.claude-opus-5-5%3A0/converse-stream")
	if resp.StatusCode != http.StatusOK || up.calls() != 1 {
		t.Fatalf("status %d, upstream calls %d: %s", resp.StatusCode, up.calls(), body)
	}
	sent := up.requests[0]
	if got, want := sent.URL.Scheme+"://"+sent.URL.Host+sent.URL.EscapedPath(),
		"https://bedrock-runtime.ap-southeast-2.amazonaws.com/model/us.anthropic.claude-opus-5-5%3A0/converse-stream"; got != want {
		t.Errorf("forwarded to %s, want %s", got, want)
	}
	if sent.URL.RawQuery != "" {
		t.Errorf("a query crossed: %q", sent.URL.RawQuery)
	}
	auth := sent.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDPI/") || !strings.Contains(auth, "/ap-southeast-2/bedrock/") ||
		strings.Contains(auth, testCallerToken) {
		t.Errorf("Authorization = %q, want SigV4 for ap-southeast-2/bedrock with pi's pair and no caller token", auth)
	}
	if string(up.bodies[0]) != converseBody {
		t.Errorf("body forwarded as %q, want it unchanged", up.bodies[0])
	}
	wantLines(t, logs(), "/agent/pi/ Converse → https://bedrock-runtime.ap-southeast-2.amazonaws.com (provider bedrock, SigV4 for bedrock in ap-southeast-2")
}

// TestAnInferenceProfileARNCrossesAsTheSDKEncodedIt: an inference-profile ARN's ':' and '/' reach
// runtime encoded as one path segment (%3A, %2F), which is the path its signature covers; and an
// agent that sent the ARN's '/' literally reaches the same upstream path, since the op is the
// path's last segment and the id is re-encoded rather than forwarded as written.
func TestAnInferenceProfileARNCrossesAsTheSDKEncodedIt(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	const want = "/model/arn%3Aaws%3Abedrock%3Aus-east-1%3A123456789012%3Ainference-profile%2Fus.anthropic.claude-opus-5-5/converse"
	for _, p := range []string{
		"/agent/pi/model/arn%3Aaws%3Abedrock%3Aus-east-1%3A123456789012%3Ainference-profile%2Fus.anthropic.claude-opus-5-5/converse",
		"/agent/pi/model/arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-opus-5-5/converse",
	} {
		before := up.calls()
		resp, body := postConverse(t, bridgeClient, via, p)
		if resp.StatusCode != http.StatusOK || up.calls() != before+1 {
			t.Fatalf("%s: status %d, upstream calls %d: %s", p, resp.StatusCode, up.calls()-before, body)
		}
		if got := up.requests[before].URL.EscapedPath(); got != want {
			t.Errorf("%s reached %s, want %s", p, got, want)
		}
		if raw := up.requests[before].URL.RawPath; raw != want {
			t.Errorf("%s: the upstream RawPath is %q, want %q, so the signed path is the sent one", p, raw, want)
		}
	}
}

// TestAConverseModelOffANarrowedListIsRefusedInAWSsShape: Converse names its model in the path
// alone, so the allowlist reads it there. A model a company `only` dropped is refused 400 as a
// ValidationException naming the model, the list and the switch, with nothing sent upstream; a
// listed ARN passes through. It fails if the pass-through stops asking the list, or the boot
// stops handing the route its agent's list.
func TestAConverseModelOffANarrowedListIsRefusedInAWSsShape(t *testing.T) {
	const arn = "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-opus-5-5"
	b := bootNarrowed(t, []string{"pi", "bedrock", "aws-auth", "wire-bridge"},
		`{"kind":"models","provider":"bedrock","add":[{"id":"us.anthropic.claude-opus-5-5","vendor":"anthropic"},
		  {"id":"`+arn+`","vendor":"anthropic"}]},
		 {"kind":"models","provider":"bedrock","only":["`+arn+`"]}`, nil,
		map[string]string{"pi": "bedrock-bridge"}, map[string]string{"pi": awsPair("AKIDPI", "us-east-1")}, true)
	if b.via == "" {
		t.Fatal("the boot served no via route for pi on bedrock-bridge")
	}
	resp, body := postConverse(t, bridgeClient, b.via, "/agent/pi/model/us.anthropic.claude-opus-5-5/converse-stream")
	if resp.StatusCode != http.StatusBadRequest || b.up.calls() != 0 {
		t.Fatalf("an off-list model: %d, upstream calls %d, want a 400 and nothing sent: %s", resp.StatusCode, b.up.calls(), body)
	}
	typ, msg := awsError(t, resp, body)
	if typ != "ValidationException" {
		t.Errorf("X-Amzn-Errortype = %q, want ValidationException", typ)
	}
	for _, want := range []string{`model "us.anthropic.claude-opus-5-5" is not on provider bedrock's list`,
		"Allowed: " + arn, `"enforce_models": false`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal lacks %q: %s", want, msg)
		}
	}
	if resp, body := postConverse(t, bridgeClient, b.via, "/agent/pi/model/"+awsEscape(arn)+"/converse"); resp.StatusCode != 200 ||
		b.up.calls() != 1 {
		t.Errorf("the listed ARN: %d, upstream calls %d: %s", resp.StatusCode, b.up.calls(), body)
	}
}

// converseEventStream is synthetic, valid AWS framing with Converse's event names and payloads
// (AWS SDK 3.1127 ConverseStreamOutput). It is not an AWS capture or an InvokeModel `chunk`.
func converseEventStream() string {
	var stream []byte
	for _, event := range [][2]string{
		{"messageStart", `{"role":"assistant"}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hi"}}`},
		{"messageStop", `{"stopReason":"end_turn"}`},
		{"metadata", `{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"metrics":{"latencyMs":3}}`},
	} {
		stream = append(stream, eventStreamMessage([][2]string{{":message-type", "event"},
			{":event-type", event[0]}, {":content-type", "application/json"}}, []byte(event[1]))...)
	}
	return string(stream)
}

func converseStreamResponse() *http.Response {
	resp := eventStreamResponse()
	resp.Body = io.NopCloser(strings.NewReader(converseEventStream()))
	return resp
}

// TestTheConverseEventStreamIsRelayedByteForByte: runtime's binary event stream reaches pi
// untouched under its own type, with AWS's request id and Bedrock's own response facts, and
// nothing else of the upstream's headers.
func TestTheConverseEventStreamIsRelayedByteForByte(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	up.responses = []func() *http.Response{converseStreamResponse}
	resp, body := postConverse(t, bridgeClient, via, "/agent/pi/model/us.anthropic.claude-opus-5-5%3A0/converse-stream")
	if resp.StatusCode != http.StatusOK || body != converseEventStream() {
		t.Fatalf("relayed %d %q, want the event stream untouched", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.amazon.eventstream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if resp.Header.Get("X-Amzn-Requestid") != "aws-req-9" || resp.Header.Get("X-Amzn-Bedrock-Input-Token-Count") != "12" ||
		resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("response headers %v: want AWS's request id and Bedrock's facts, and nothing else", resp.Header)
	}
	// Each chunk is flushed as it arrives, which a served connection cannot show: the handler
	// itself, over a recorder.
	up.responses = append(up.responses, converseStreamResponse)
	h := newConversePassthrough("pi", "bedrock", bedrockBase,
		&bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{Env: sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/model/us.anthropic.claude-opus-5-5%3A0/converse-stream",
		strings.NewReader(converseBody)))
	if rec.Body.String() != converseEventStream() || !rec.Flushed {
		t.Errorf("the handler relayed %q (flushed %v), want the stream untouched and flushed", rec.Body.String(), rec.Flushed)
	}
}

// TestAConverseStreamCutShortAbortsTheAgentsConnection: an upstream that fails after the status
// line went out, possibly inside an event-stream message, makes pi's read fail rather than end,
// and the daemon log names the cut.
func TestAConverseStreamCutShortAbortsTheAgentsConnection(t *testing.T) {
	up, _, via, logs := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	stream, pw := newStubStream()
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: stream}
	}}
	const part = "\x00\x00\x00\x7f\x00\x00"
	go func() {
		_, _ = io.WriteString(pw, part)
		_ = pw.CloseWithError(fmt.Errorf("read tcp: connection reset by peer"))
	}()
	req, _ := http.NewRequest(http.MethodPost, "http://"+via+"/agent/pi/model/us.anthropic.claude-opus-5-5%3A0/converse-stream",
		strings.NewReader(converseBody))
	req.Header.Set("Authorization", "Bearer "+testCallerToken)
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: &protocols}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("Converse stream used %s, want pi's h2c transport", resp.Proto)
	}
	got, rerr := io.ReadAll(resp.Body)
	if rerr == nil {
		t.Errorf("pi read %q and then a clean end; want a failed read, so a truncated reply is never taken as finished", got)
	}
	waitClosed(t, stream)
	wantLines(t, logs(), "via route for pi: upstream https://bedrock-runtime.us-east-1.amazonaws.com: the Converse stream "+
		"for model us.anthropic.claude-opus-5-5:0 ended early after 6 bytes", "connection reset by peer")
}

// TestConverseOnANonBedrockViaRouteIsRefusedNotTranslated: a route whose provider is not Bedrock
// (zai) refuses Converse 404 in AWS's shape, naming the provider and why, and sends nothing
// upstream: a via route never translates one API into another.
func TestConverseOnANonBedrockViaRouteIsRefusedNotTranslated(t *testing.T) {
	up := withUpstream(t)
	addr := startResponsesPlan(t, map[string]string{"pi": "pz"})
	resp, body := postConverse(t, bridgeClient, addr, "/agent/pi/model/glm-5.3/converse-stream")
	if resp.StatusCode != http.StatusNotFound || up.calls() != 0 {
		t.Fatalf("%d with %d upstream calls: %s, want a 404 and nothing sent", resp.StatusCode, up.calls(), body)
	}
	typ, msg := awsError(t, resp, body)
	if typ != "UnknownOperationException" || !strings.Contains(msg, "provider zai (the via route for pi) is not Bedrock's") ||
		!strings.Contains(msg, "never translates") {
		t.Errorf("refusal %s %q, want an UnknownOperationException naming zai and why", typ, msg)
	}
}

// TestTheViaListenerSpeaksHTTP2WithoutTLS: pi's AWS SDK opens HTTP/2 to an http:// address with
// prior knowledge, so a client that speaks only that completes a Converse request through the
// production listener, over HTTP/2; and an HTTP/1 client still reaches the chat-completions wire.
// It fails if the via listener stops enabling unencrypted HTTP/2 (the h2 client's preface is then
// read as a malformed HTTP/1 request).
func TestTheViaListenerSpeaksHTTP2WithoutTLS(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	var only http.Protocols
	only.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: &only}
	t.Cleanup(transport.CloseIdleConnections)
	h2only := &http.Client{Transport: transport}
	up.responses = []func() *http.Response{converseStreamResponse}
	resp, body := postConverse(t, h2only, via, "/agent/pi/model/us.anthropic.claude-opus-5-5%3A0/converse-stream")
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 || up.calls() != 1 || body != converseEventStream() {
		t.Fatalf("over h2c: %s %d, upstream calls %d: %s", resp.Proto, resp.StatusCode, up.calls(), body)
	}
	resp, body = postTo(t, "http://"+via+"/agent/pi/chat/completions", `{"model":"us.openai.gpt-6.1-sol","messages":[]}`, nil)
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 1 {
		t.Errorf("over HTTP/1: %s %d: %s", resp.Proto, resp.StatusCode, body)
	}
}

// TestABedrockConverseRouteWithNoCredentialIdlesInAWSsShape: pi on bedrock-bridge with a region
// and no credential source gets a 503 ServiceUnavailableException naming the Converse upstream the
// bridge composed and every credential source it reads, and nothing is sent.
func TestABedrockConverseRouteWithNoCredentialIdlesInAWSsShape(t *testing.T) {
	clearAWS(t)
	up := withUpstream(t)
	providers, resolved := shippedBridgeTables(t, "")
	p := planFor(providers, map[string]string{"pi": "bedrock-bridge"}, resolved)
	if len(p.via.Routes) != 1 {
		t.Fatalf("via routes %+v (skipped %v), want pi's", p.via.Routes, p.via.Skipped)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "pi", "export AWS_REGION=${AWS_REGION:-'eu-west-1'}")
	p.via.ListenAddr = freeLoopback(t)
	startPlan(t, p, home)
	resp, body := postConverse(t, bridgeClient, p.via.ListenAddr, "/agent/pi/model/us.anthropic.claude-opus-5-5%3A0/converse-stream")
	if resp.StatusCode != http.StatusServiceUnavailable || up.calls() != 0 {
		t.Fatalf("%d with %d upstream calls: %s, want a 503 and nothing sent", resp.StatusCode, up.calls(), body)
	}
	typ, msg := awsError(t, resp, body)
	if typ != "ServiceUnavailableException" {
		t.Errorf("X-Amzn-Errortype = %q, want ServiceUnavailableException", typ)
	}
	for _, want := range []string{"via route for pi goes to Bedrock (https://bedrock-runtime.eu-west-1.amazonaws.com)",
		"AWS_ACCESS_KEY_ID", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_BEARER_TOKEN_BEDROCK"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the idle message lacks %q: %s", want, msg)
		}
	}
}

// TestConverseToAProxyKeepsItsPrefixAndOnlyTheAllowedHeaders pins WG-I48's destination and
// header boundary through the production boot, not just the URL builder.
func TestConverseToAProxyKeepsItsPrefixAndOnlyTheAllowedHeaders(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridgeOver(t, `{"bedrock":{"region":"eu-west-1","endpoints":{
		"openai":{"base_url":"https://proxy.example/bedrock/openai/v1"}}}}`, "eu-west-1", "us-east-1")
	req, err := http.NewRequest(http.MethodPost, "http://"+via+"/agent/pi/model/test%3A0/converse?discard=this",
		strings.NewReader(converseBody))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"Content-Type": "application/json", "Accept": "application/json",
		"X-Amzn-Bedrock-Trace": "ENABLED", "Authorization": "Bearer " + testCallerToken,
		"X-Api-Key": "inbound-not-an-upstream-key", "X-Amz-Security-Token": "inbound-not-a-session",
		"X-Amz-User-Agent": "aws-sdk-js/3.1127.0", "Amz-Sdk-Invocation-Id": "inv-1", "Amz-Sdk-Request": "attempt=1",
	} {
		req.Header.Set(key, value)
	}
	resp, err := bridgeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || up.calls() != 1 {
		t.Fatalf("status %d, upstream calls %d", resp.StatusCode, up.calls())
	}
	sent := up.requests[0]
	if got, want := sent.URL.String(), "https://proxy.example/bedrock/model/test%3A0/converse"; got != want {
		t.Errorf("target = %q, want %q with no inbound query", got, want)
	}
	for _, key := range []string{"Content-Type", "Accept", "X-Amzn-Bedrock-Trace"} {
		if sent.Header.Get(key) != req.Header.Get(key) {
			t.Errorf("allowed header %s = %q, want %q", key, sent.Header.Get(key), req.Header.Get(key))
		}
	}
	for _, key := range []string{"X-Api-Key", "X-Amz-Security-Token", "X-Amz-User-Agent", "Amz-Sdk-Invocation-Id", "Amz-Sdk-Request"} {
		if sent.Header.Get(key) != "" {
			t.Errorf("inbound-only header %s crossed: %q", key, sent.Header.Get(key))
		}
	}
	if auth := sent.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDPI/") ||
		!strings.Contains(auth, "/eu-west-1/bedrock/") || strings.Contains(auth, testCallerToken) {
		t.Errorf("Authorization = %q, want pi's own pair signed for the provider's region", auth)
	}
}

// TestConverseWithABedrockAPIKeyRelaysAnUpstreamRefusal covers the other authorization arm
// and AWS's JSON error wire: the bridge adds only the upstream bearer and keeps the refusal.
func TestConverseWithABedrockAPIKeyRelaysAnUpstreamRefusal(t *testing.T) {
	clearAWS(t)
	up := withUpstream(t)
	const refusal = `{"message":"fixture quota exceeded"}`
	up.responses = []func() *http.Response{func() *http.Response {
		resp := jsonResponse(http.StatusTooManyRequests, refusal)
		resp.Header.Set("X-Amzn-Errortype", "ThrottlingException")
		resp.Header.Set("X-Amzn-Requestid", "fixture-request")
		resp.Header.Set("Retry-After", "2")
		return resp
	}}
	providers, resolved := shippedBridgeTables(t, `{"bedrock":{"region":"us-east-1"}}`)
	p := planFor(providers, map[string]string{"pi": "bedrock-bridge"}, resolved)
	if p.adapter != nil {
		p.adapter.ListenAddr = freeLoopback(t)
	}
	home := t.TempDir()
	writeAgentKey(t, home, "pi", "export AWS_BEARER_TOKEN_BEDROCK=${AWS_BEARER_TOKEN_BEDROCK:-'fixture-upstream-key'}")
	p.via.ListenAddr = freeLoopback(t)
	startPlan(t, p, home)
	resp, body := postConverse(t, bridgeClient, p.via.ListenAddr, "/agent/pi/model/test/converse-stream")
	if resp.StatusCode != http.StatusTooManyRequests || body != refusal || up.calls() != 1 {
		t.Fatalf("status %d, upstream calls %d, body %q: want an unchanged upstream refusal", resp.StatusCode, up.calls(), body)
	}
	for key, want := range map[string]string{"X-Amzn-Errortype": "ThrottlingException", "X-Amzn-Requestid": "fixture-request", "Retry-After": "2"} {
		if resp.Header.Get(key) != want {
			t.Errorf("response %s = %q, want %q", key, resp.Header.Get(key), want)
		}
	}
	sent := up.requests[0]
	if sent.Header.Get("Authorization") != "Bearer fixture-upstream-key" || sent.Header.Get("X-Amz-Date") != "" {
		t.Errorf("upstream headers = %v, want only the Bedrock API key and no signature", sent.Header)
	}
}

// TestConverseRelaysANonExpiry403LargerThanTheExpiryProbe pins refusal pass-through beyond
// expiredRejection's 64 KiB inspection window. A non-expiry refusal must not be truncated or
// retried, even though it passes through that helper before the body is relayed.
func TestConverseRelaysANonExpiry403LargerThanTheExpiryProbe(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	const envelope = `{"message":""}`
	refusal := `{"message":"` + strings.Repeat("x", (64<<10)+1-len(envelope)) + `"}`
	up.responses = []func() *http.Response{func() *http.Response {
		resp := jsonResponse(http.StatusForbidden, refusal)
		resp.Header.Set("X-Amzn-Errortype", "AccessDeniedException")
		return resp
	}}
	resp, body := postConverse(t, bridgeClient, via, "/agent/pi/model/test/converse-stream")
	if resp.StatusCode != http.StatusForbidden || body != refusal || up.calls() != 1 {
		t.Fatalf("status %d, body bytes %d (want %d), upstream calls %d: want the entire non-expiry 403 without retry",
			resp.StatusCode, len(body), len(refusal), up.calls())
	}
	if resp.Header.Get("X-Amzn-Errortype") != "AccessDeniedException" {
		t.Errorf("response headers lost the upstream error type: %v", resp.Header)
	}
}

// TestConverseDoesNotHideANonExpiry403BodyReadFailure exercises the production boot on
// pi's h2c transport. A failure consumed by the expiry probe must still abort the agent's
// read, retain the already-read bytes and be logged with the original upstream error.
func TestConverseDoesNotHideANonExpiry403BodyReadFailure(t *testing.T) {
	up, _, via, logs := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	stream, pw := newStubStream()
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: http.StatusForbidden,
			Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Errortype": {"AccessDeniedException"}}, Body: stream}
	}}
	const part = `{"message":"fixture permission denied`
	go func() {
		_, _ = io.WriteString(pw, part)
		_ = pw.CloseWithError(fmt.Errorf("fixture non-expiry response read failure"))
	}()
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: &protocols}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	req, err := http.NewRequest(http.MethodPost, "http://"+via+"/agent/pi/model/test/converse-stream", strings.NewReader(converseBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testCallerToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusForbidden || string(body) != part || readErr == nil || up.calls() != 1 {
		t.Errorf("%s %d, body %q, read error %v, upstream calls %d: want the partial 403 and a failed read, without retry",
			resp.Proto, resp.StatusCode, body, readErr, up.calls())
	}
	waitClosed(t, stream)
	wantLines(t, logs(), fmt.Sprintf("Converse stream for model test ended early after %d bytes", len(part)),
		"fixture non-expiry response read failure")
}

// TestConverseExpiryStillRefreshesAndRetriesOnlyOnce pins the existing retry contract
// through the production boot: the first expiry refreshes the fixture credential, and a
// second expiry is relayed unchanged rather than starting another retry.
func TestConverseExpiryStillRefreshesAndRetriesOnlyOnce(t *testing.T) {
	const refusal = `{"message":"Signature expired: fixture"}`
	for _, secondExpiry := range []bool{false, true} {
		t.Run(fmt.Sprintf("second-expiry=%v", secondExpiry), func(t *testing.T) {
			clearAWS(t)
			up := withUpstream(t)
			expired := func() *http.Response { return jsonResponse(http.StatusForbidden, refusal) }
			up.responses = []func() *http.Response{expired}
			if secondExpiry {
				up.responses = append(up.responses, expired)
			}
			adapter, fetches := credAdapter(t)
			providers, resolved := shippedBridgeTables(t, `{"bedrock":{"region":"us-east-1"}}`)
			p := planFor(providers, map[string]string{"pi": "bedrock-bridge"}, resolved)
			if p.adapter != nil {
				p.adapter.ListenAddr = freeLoopback(t)
			}
			home := t.TempDir()
			writeAgentKey(t, home, "pi", "export AWS_CONTAINER_CREDENTIALS_FULL_URI=${AWS_CONTAINER_CREDENTIALS_FULL_URI:-'"+adapter.URL+"/credentials'}")
			p.via.ListenAddr = freeLoopback(t)
			startPlan(t, p, home)
			resp, body := postConverse(t, bridgeClient, p.via.ListenAddr, "/agent/pi/model/test/converse-stream")
			wantStatus := http.StatusOK
			if secondExpiry {
				wantStatus = http.StatusForbidden
				if body != refusal {
					t.Errorf("second expiry body = %q, want %q unchanged", body, refusal)
				}
			}
			if resp.StatusCode != wantStatus || up.calls() != 2 || *fetches != 2 {
				t.Fatalf("status %d, upstream calls %d, credential fetches %d: want status %d after one refresh/retry",
					resp.StatusCode, up.calls(), *fetches, wantStatus)
			}
			if !strings.Contains(up.requests[1].Header.Get("Authorization"), "Credential=ASIA2/") {
				t.Errorf("retry did not use refreshed fixture credential: %v", up.requests[1].Header)
			}
		})
	}
}

// TestConverseOverHTTP2RefusesAnUnauthenticatedCallerBeforeTheRoute pins the common caller
// gate on pi's h2c transport. Its 401 remains OpenAI-shaped as WG-I48 states; an authenticated
// non-POST reaches the Converse route and is refused in AWS's shape. Neither reaches upstream.
func TestConverseOverHTTP2RefusesAnUnauthenticatedCallerBeforeTheRoute(t *testing.T) {
	up, _, via, _ := servedShippedBedrockBridge(t, "eu-west-1", "us-east-1")
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: &protocols}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	for _, c := range []struct {
		method, token string
		status        int
		message       string
	}{
		{http.MethodPost, "", http.StatusUnauthorized, "carried no caller token"},
		{http.MethodPost, "fixture-wrong-token", http.StatusUnauthorized, "not this launch's"},
		{http.MethodGet, testCallerToken, http.StatusMethodNotAllowed, "route takes POST"},
	} {
		req, err := http.NewRequest(c.method, "http://"+via+"/agent/pi/model/test/converse-stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 2 || resp.StatusCode != c.status || !strings.Contains(string(body), c.message) || up.calls() != 0 {
			t.Errorf("%s with token %q: %s %d, %q, upstream calls %d", c.method, c.token, resp.Proto, resp.StatusCode, body, up.calls())
		}
		if c.status == http.StatusUnauthorized && (resp.Header.Get("WWW-Authenticate") == "" || !strings.Contains(string(body), `"error":`)) {
			t.Errorf("caller refusal lacks OpenAI error or bearer challenge: %v %s", resp.Header, body)
		}
		if c.status == http.StatusMethodNotAllowed && resp.Header.Get("X-Amzn-Errortype") != "ValidationException" {
			t.Errorf("method refusal lacks AWS error type: %v", resp.Header)
		}
	}
}

// TestParseConversePathTakesTheOpFromTheLastSegment pins the parser the route classifies by: the
// op is the last segment, the id everything between, and an id with a byte no model id has, or no
// id, is not a Converse route.
func TestParseConversePathTakesTheOpFromTheLastSegment(t *testing.T) {
	for _, c := range []struct {
		path, id, op string
		ok           bool
	}{
		{"/model/us.anthropic.claude-opus-5-5:0/converse-stream", "us.anthropic.claude-opus-5-5:0", "converse-stream", true},
		{"/model/arn:aws:bedrock:us-east-1:1:inference-profile/x/converse", "arn:aws:bedrock:us-east-1:1:inference-profile/x", "converse", true},
		{"/model//converse", "", "", false},
		{"/model/x/invoke", "", "", false},
		{"/model/a b/converse", "", "", false},
		{"/models/x/converse", "", "", false},
	} {
		id, op, ok := parseConversePath(c.path)
		if id != c.id || op != c.op || ok != c.ok {
			t.Errorf("%s: (%q, %q, %v), want (%q, %q, %v)", c.path, id, op, ok, c.id, c.op, c.ok)
		}
	}
}
