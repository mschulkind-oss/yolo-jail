package wirebridged

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

// The invoke pass-through's tests (invoke.go): Bedrock's own InvokeModel routes, signed and
// passed through for claude in its own Bedrock mode at the bridge (model-lists-and-pickers.md
// OQ-MM6). No request leaves the process: upstreamTransport answers every one.

// eventStreamBytes stands in for AWS's binary event-stream framing: bytes the bridge must relay
// untouched, a NUL and a high byte among them, under the type Claude Code reads it by.
const eventStreamBytes = "\x00\x00\x00\x7f\x00\x00\x00\x4b\xde\xad:event-type\x07\x00\x05chunk{\"bytes\":\"eyJ0eXBlIjoibWVzc2FnZV9zdG9wIn0=\"}\xff"

func invokeHandler(vendors map[string]string) *bridgeHandler {
	h := newSignedChatHandler(bedrockBase, wirebridge.ChatOptions{},
		&bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{Env: sigv4.Env{AccessKeyID: "AKIDINV",
			SecretAccessKey: "inv-secret"}}}, nil)
	h.invoke.vendors = vendors
	return h
}

func eventStreamResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{
		"Content-Type":                     {"application/vnd.amazon.eventstream"},
		"X-Amzn-Requestid":                 {"aws-req-9"},
		"X-Amzn-Bedrock-Input-Token-Count": {"12"},
		"Set-Cookie":                       {"not-relayed"},
	}, Body: io.NopCloser(strings.NewReader(eventStreamBytes))}
}

// TestBedrocksInvokeRoutesAreSignedAndPassedThrough is the route as claude's Bedrock mode meets
// it: the body forwarded byte for byte to the same route on runtime's host, the model id's `:`
// encoded as the AWS SDKs encode it, signed with SigV4 and carrying none of the inbound
// credential, Bedrock's own request headers kept, and the binary event stream relayed untouched
// under its own type. It fails if ServeHTTP stops routing /model/{id}/… to the pass-through,
// which then answers 404.
func TestBedrocksInvokeRoutesAreSignedAndPassedThrough(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{eventStreamResponse}
	const body = `{"anthropic_version":"bedrock-2023-05-31","anthropic_beta":["x"],"max_tokens":9,"messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/model/us.anthropic.claude-test-v1%3A0/invoke-with-response-stream",
		strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer the-caller-token")
	req.Header.Set("X-Amzn-Bedrock-Service-Tier", "priority")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	invokeHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || up.calls() != 1 {
		t.Fatalf("status %d, %d upstream calls: %s", rec.Code, up.calls(), rec.Body)
	}
	sent := up.requests[0]
	if got := sent.URL.Scheme + "://" + sent.URL.Host + sent.URL.EscapedPath(); got !=
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/us.anthropic.claude-test-v1%3A0/invoke-with-response-stream" {
		t.Errorf("forwarded to %s", got)
	}
	if string(up.bodies[0]) != body {
		t.Errorf("body forwarded as %q, want it unchanged", up.bodies[0])
	}
	if a := sent.Header.Get("Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIDINV/") ||
		strings.Contains(a, "the-caller-token") {
		t.Errorf("Authorization = %q, want a SigV4 signature and no inbound token", a)
	}
	if sent.Header.Get("X-Amzn-Bedrock-Service-Tier") != "priority" {
		t.Errorf("Bedrock's own request header was dropped: %v", sent.Header)
	}
	if rec.Body.String() != eventStreamBytes || rec.Header().Get("Content-Type") != "application/vnd.amazon.eventstream" {
		t.Errorf("relayed %q as %q, want the event stream untouched", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Amzn-Bedrock-Input-Token-Count") != "12" || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("response headers %v: want Bedrock's facts and nothing else", rec.Header())
	}
}

// TestTheInvokeRouteRefusesAnotherMakersModel: a model the list declares another maker's never
// reaches Bedrock in Anthropic's request format; the refusal says why and what to do instead,
// in the shape an AWS SDK reads. An Anthropic one, and one the list does not name, pass.
func TestTheInvokeRouteRefusesAnotherMakersModel(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response { return jsonResponse(200, `{"type":"message"}`) }}
	h := invokeHandler(map[string]string{"openai.gpt-test-1:0": "openai", "us.anthropic.claude-test-v1": "anthropic"})
	rec := servePathTo(t, h, "/model/openai.gpt-test-1%3A0/invoke", `{}`)
	if rec.Code != http.StatusBadRequest || up.calls() != 0 ||
		rec.Header().Get("X-Amzn-Errortype") != "ValidationException" ||
		!strings.Contains(rec.Body.String(), "openai's model on the provider's list") {
		t.Fatalf("another maker's model: %d %v %s, %d calls", rec.Code, rec.Header(), rec.Body, up.calls())
	}
	for _, id := range []string{"us.anthropic.claude-test-v1", "unlisted.model-v1"} {
		if rec := servePathTo(t, h, "/model/"+id+"/invoke", `{}`); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s, want it passed through", id, rec.Code, rec.Body)
		}
	}
}

// TestTheControlPlaneIsRefusedInAWSsShape: Claude Code's best-effort startup reads get a 404 its
// AWS SDK reads as an error, so it falls back on its own model ids; nothing reaches AWS.
func TestTheControlPlaneIsRefusedInAWSsShape(t *testing.T) {
	up := withUpstream(t)
	rec := httptest.NewRecorder()
	invokeHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/inference-profiles?typeEquals=SYSTEM_DEFINED", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Amzn-Errortype") != "ResourceNotFoundException" ||
		up.calls() != 0 {
		t.Errorf("GET /inference-profiles: %d %v %s, %d calls", rec.Code, rec.Header(), rec.Body, up.calls())
	}
}

// TestTheInvokePathAcceptsOnlyAModelID: a path that is not /model/{id}/{op}, an op the route does
// not serve, or an id with a byte no model id has, is not the pass-through's.
func TestTheInvokePathAcceptsOnlyAModelID(t *testing.T) {
	for _, tc := range []struct {
		path, id, op string
		ok           bool
	}{
		{"/model/us.anthropic.claude-test-v1%3A0/invoke", "us.anthropic.claude-test-v1:0", "invoke", true},
		{"/model/m/count-tokens", "m", "count-tokens", true},
		{"/model/m/converse", "", "", false},
		{"/model/m/invoke/extra", "", "", false},
		{"/model/a%20b/invoke", "", "", false},
		{"/model//invoke", "", "", false},
		{"/v1/messages", "", "", false},
	} {
		id, op, ok := parseInvokePath(tc.path)
		if ok != tc.ok || id != tc.id || op != tc.op {
			t.Errorf("parseInvokePath(%q) = %q, %q, %v; want %q, %q, %v", tc.path, id, op, ok, tc.id, tc.op, tc.ok)
		}
	}
	if got := awsEscape("arn:x/y z"); got != "arn%3Ax%2Fy%20z" {
		t.Errorf("awsEscape = %q", got)
	}
}

// TestTheBootHandsTheInvokeRouteTheListsMakers runs the PRODUCTION boot (routeFor, adapterHandler,
// serve): the list's `lookalike` maker reaches the invoke route, which refuses that model, and an
// Anthropic model passes. It fails if the boot stops handing the route's makers to the
// pass-through (route.ModelVendors), which then forwards the lookalike.
func TestTheBootHandsTheInvokeRouteTheListsMakers(t *testing.T) {
	up, addr, _ := servedBedrockRoute(t, "claude")
	up.responses = []func() *http.Response{eventStreamResponse}
	post := func(id string) int {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/model/"+id+"/invoke", strings.NewReader(`{}`))
		resp, err := bridgeClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}
	if got := post("us.anthropic-lookalike.model-1"); got != http.StatusBadRequest || up.calls() != 0 {
		t.Errorf("the lookalike: %d after %d calls, want a refusal", got, up.calls())
	}
	if got := post(opusID); got != http.StatusOK || up.calls() != 1 {
		t.Errorf("an Anthropic model: %d after %d calls, want it passed through", got, up.calls())
	}
}

// TestAFetchedListsMakerRoutesAClaudeModelUntranslated: where no pack or config gives the provider
// a list, the list the launch fetched says which models are Anthropic's (its maker is AWS's own
// providerName), so a Claude model on it goes to runtime's Messages route untranslated and another
// maker's is refused on the invoke route. It fails if declaredVendors stops reading the fetched
// list.
func TestAFetchedListsMakerRoutesAClaudeModelUntranslated(t *testing.T) {
	table := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("platform", "aws-bedrock")
	eps := jsonx.NewOrderedMap()
	openai := jsonx.NewOrderedMap()
	openai.Set("base_url", bedrockBase)
	eps.Set("openai", openai)
	anthropic := jsonx.NewOrderedMap()
	anthropic.Set("base_url", "http://127.0.0.1:8214")
	eps.Set("anthropic", anthropic)
	entry.Set("endpoints", eps)
	table.Set("br", entry)
	packload.SetFetchedModels(table, "br", []packload.FetchedModel{
		{ID: "us.anthropic.claude-test-v1", Vendor: "anthropic", Name: "US Claude Test"},
		{ID: "openai.gpt-test-1:0", Vendor: "openai"}})
	rt, idle := routeFor(table, map[string]string{"copilot": "bp"},
		map[string]packload.ResolvedProfile{"bp": {Provider: "br"}})
	if idle != "" {
		t.Fatalf("no route: %s", idle)
	}
	if !rt.AnthropicModels["us.anthropic.claude-test-v1"] || rt.AnthropicModels["openai.gpt-test-1:0"] {
		t.Errorf("AnthropicModels = %v, want the fetched Claude id alone", rt.AnthropicModels)
	}
	if rt.ModelVendors["openai.gpt-test-1:0"] != "openai" {
		t.Errorf("ModelVendors = %v, want the fetched makers", rt.ModelVendors)
	}
}
