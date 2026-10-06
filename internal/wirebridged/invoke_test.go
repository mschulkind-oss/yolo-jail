package wirebridged

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
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

// TestTheInvokeRouteTranslatesAnotherMakersModel is the everything profile on claude's Bedrock
// mode (docs/design/bedrock-plumbing.md OQ-BR11, as OQ-MM6 amended it): a model the list declares
// another maker's never reaches Bedrock in Anthropic's request format. Its InvokeModel body becomes
// the Messages request it carries (the path's model, no `anthropic_version` or `anthropic_beta`),
// which the route translates to runtime's chat completions as it does on /v1/messages, and the
// answer comes back as InvokeModel's: Anthropic's message JSON. An Anthropic model, and one the
// list does not name, pass through untranslated.
func TestTheInvokeRouteTranslatesAnotherMakersModel(t *testing.T) {
	up := withUpstream(t)
	h := invokeHandler(map[string]string{"openai.gpt-test-1:0": "openai", "us.anthropic.claude-test-v1": "anthropic"})
	const body = `{"anthropic_version":"bedrock-2023-05-31","anthropic_beta":["context-1m-2025-08-07"],` +
		`"max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`
	rec := servePathTo(t, h, "/model/openai.gpt-test-1%3A0/invoke", body)
	if rec.Code != http.StatusOK || up.calls() != 1 {
		t.Fatalf("another maker's model: %d %s, %d upstream calls", rec.Code, rec.Body, up.calls())
	}
	sent := up.requests[0]
	if got := sent.URL.String(); got != bedrockBase+"/chat/completions" {
		t.Errorf("translated to %s, want runtime's chat completions", got)
	}
	if a := sent.Header.Get("Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIDINV/") {
		t.Errorf("the translated request is not signed: %q", a)
	}
	var translated map[string]any
	if err := json.Unmarshal(up.bodies[0], &translated); err != nil {
		t.Fatal(err)
	}
	if translated["model"] != "openai.gpt-test-1:0" || translated["anthropic_version"] != nil ||
		translated["anthropic_beta"] != nil || translated["stream"] == true {
		t.Errorf("translated body %s, want the path's model and none of InvokeModel's own fields", up.bodies[0])
	}
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil || msg.Type != "message" ||
		len(msg.Content) != 1 || msg.Content[0].Text != "hi there" ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("answer %s (%s), want Anthropic's message JSON", rec.Body, rec.Header().Get("Content-Type"))
	}
	up.responses = []func() *http.Response{
		func() *http.Response { return jsonResponse(200, `{"type":"message"}`) },
		func() *http.Response { return jsonResponse(200, `{"type":"message"}`) }}
	up.requests, up.bodies = nil, nil
	for _, id := range []string{"us.anthropic.claude-test-v1", "unlisted.model-v1"} {
		rec := servePathTo(t, h, "/model/"+id+"/invoke", `{}`)
		if rec.Code != http.StatusOK || !strings.HasSuffix(up.requests[len(up.requests)-1].URL.Path, "/model/"+id+"/invoke") {
			t.Errorf("%s: %d %s, want it passed through", id, rec.Code, rec.Body)
		}
	}
}

// awsEvent is one decoded message of AWS's binary event stream: its string headers and payload.
type awsEvent struct {
	headers map[string]string
	payload []byte
}

// decodeEventStream reads AWS's `application/vnd.amazon.eventstream` framing (each message a
// 12-byte prelude of total length, headers length and the prelude's CRC32, then the headers,
// the payload, and the whole message's CRC32), checking both checksums, as an AWS SDK does.
func decodeEventStream(t *testing.T, b []byte) []awsEvent {
	t.Helper()
	var out []awsEvent
	for len(b) > 0 {
		if len(b) < 16 {
			t.Fatalf("a truncated event-stream message: % x", b)
		}
		total, hlen := binary.BigEndian.Uint32(b[0:4]), binary.BigEndian.Uint32(b[4:8])
		if crc32.ChecksumIEEE(b[:8]) != binary.BigEndian.Uint32(b[8:12]) {
			t.Fatalf("the prelude's CRC does not match")
		}
		if int(total) > len(b) || total < 16+hlen {
			t.Fatalf("message length %d of %d bytes left", total, len(b))
		}
		msg := b[:total]
		if crc32.ChecksumIEEE(msg[:total-4]) != binary.BigEndian.Uint32(msg[total-4:]) {
			t.Fatalf("the message's CRC does not match")
		}
		ev := awsEvent{headers: map[string]string{}, payload: msg[12+hlen : total-4]}
		for h := msg[12 : 12+hlen]; len(h) > 0; {
			n := int(h[0])
			name := string(h[1 : 1+n])
			if h[1+n] != 7 {
				t.Fatalf("header %s has type %d, want a string (7)", name, h[1+n])
			}
			vlen := int(binary.BigEndian.Uint16(h[2+n : 4+n]))
			ev.headers[name] = string(h[4+n : 4+n+vlen])
			h = h[4+n+vlen:]
		}
		out = append(out, ev)
		b = b[total:]
	}
	return out
}

// chunkEvents is the Anthropic stream events an event stream's `chunk` messages carry: each
// payload's base64 `bytes`, decoded, as Claude Code's Bedrock client reads them back into SSE by
// their `type` (read in the 2.1.290 binary). A message of any other kind is returned in other.
func chunkEvents(t *testing.T, evs []awsEvent) (types []string, text string, other []awsEvent) {
	t.Helper()
	for _, ev := range evs {
		if ev.headers[":message-type"] != "event" || ev.headers[":event-type"] != "chunk" {
			other = append(other, ev)
			continue
		}
		if ev.headers[":content-type"] != "application/json" {
			t.Errorf("chunk content type %q", ev.headers[":content-type"])
		}
		var part struct {
			Bytes []byte `json:"bytes"`
		}
		if err := json.Unmarshal(ev.payload, &part); err != nil {
			t.Fatalf("chunk payload %q: %v", ev.payload, err)
		}
		var data struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(part.Bytes, &data); err != nil || data.Type == "" {
			t.Fatalf("chunk bytes %q are not an Anthropic event: %v", part.Bytes, err)
		}
		types = append(types, data.Type)
		text += data.Delta.Text
	}
	return types, text, other
}

// TestATranslatedModelsStreamIsAWSsEventStream: on /invoke-with-response-stream another maker's
// model is translated as a streamed Messages request, and the Anthropic events the translation
// yields go back framed as InvokeModelWithResponseStream frames them: one `chunk` message per
// event, its payload `{"bytes": <the event's JSON, base64>}`, under AWS's own content type.
func TestATranslatedModelsStreamIsAWSsEventStream(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response {
		return sseResponse(io.NopCloser(strings.NewReader(usageAfterFinishStream)))
	}}
	h := invokeHandler(map[string]string{"openai.gpt-test-1:0": "openai"})
	rec := servePathTo(t, h, "/model/openai.gpt-test-1%3A0/invoke-with-response-stream",
		`{"anthropic_version":"bedrock-2023-05-31","max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/vnd.amazon.eventstream" {
		t.Fatalf("%d %q: %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	var translated map[string]any
	_ = json.Unmarshal(up.bodies[0], &translated)
	if translated["stream"] != true {
		t.Errorf("the translated request does not stream: %s", up.bodies[0])
	}
	types, text, other := chunkEvents(t, decodeEventStream(t, rec.Body.Bytes()))
	if len(types) < 3 || types[0] != "message_start" || types[len(types)-1] != "message_stop" || text != "Hi" ||
		len(other) != 0 {
		t.Errorf("events %v, text %q, other %d, want a whole Anthropic stream saying Hi", types, text, len(other))
	}
}

// TestATranslatedModelsFailuresAreAWSs: a failure before the stream starts is InvokeModel's error
// shape at the upstream's status, and one mid-stream is an event-stream exception message, which
// Claude Code's Bedrock client turns into an `error` event (read in the 2.1.290 binary): the only
// legal way to fail inside the stream, as the `error` event is in SSE.
func TestATranslatedModelsFailuresAreAWSs(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{
		func() *http.Response { return jsonResponse(400, `{"error":{"message":"no such model"}}`) },
		func() *http.Response {
			return sseResponse(io.NopCloser(strings.NewReader(strings.SplitN(usageAfterFinishStream, "\n\n", 2)[0] + "\n\n")))
		}}
	h := invokeHandler(map[string]string{"openai.gpt-test-1:0": "openai"})
	path := "/model/openai.gpt-test-1%3A0/invoke-with-response-stream"
	rec := servePathTo(t, h, path, `{"max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`)
	var awsErr struct {
		Message string `json:"message"`
	}
	if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" ||
		json.Unmarshal(rec.Body.Bytes(), &awsErr) != nil || awsErr.Message != "no such model" {
		t.Errorf("an upstream 400: %d %v %s, want AWS's error shape with the upstream's message", rec.Code, rec.Header(), rec.Body)
	}
	rec = servePathTo(t, h, path, `{"max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`)
	_, _, other := chunkEvents(t, decodeEventStream(t, rec.Body.Bytes()))
	if len(other) != 1 || other[0].headers[":message-type"] != "exception" ||
		other[0].headers[":exception-type"] != "modelStreamErrorException" ||
		json.Unmarshal(other[0].payload, &awsErr) != nil || !strings.Contains(awsErr.Message, "wire-bridge") {
		t.Errorf("a stream cut short: %d other messages %+v, want one exception naming the bridge", len(other), other)
	}
}

// TestCountTokensOfATranslatedModelIsRefused: as /v1/messages/count_tokens is (wire-bridge.md
// WB-D14), so claude uses its own estimator; nothing reaches the upstream.
func TestCountTokensOfATranslatedModelIsRefused(t *testing.T) {
	up := withUpstream(t)
	rec := servePathTo(t, invokeHandler(map[string]string{"openai.gpt-test-1:0": "openai"}),
		"/model/openai.gpt-test-1%3A0/count-tokens", `{"input":{}}`)
	if rec.Code != http.StatusNotFound || up.calls() != 0 || rec.Header().Get("X-Amzn-Errortype") == "" {
		t.Errorf("count-tokens of a translated model: %d %v %s, %d calls", rec.Code, rec.Header(), rec.Body, up.calls())
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
// serve): the list's `lookalike` maker reaches the invoke route, which translates that model to
// runtime's chat completions, and an Anthropic model passes through. It fails if the boot stops
// handing the route's makers to the pass-through (route.ModelVendors), which then forwards the
// lookalike in Anthropic's format.
func TestTheBootHandsTheInvokeRouteTheListsMakers(t *testing.T) {
	up, addr, _ := servedBedrockRoute(t, "claude")
	post := func(id string) int {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/model/"+id+"/invoke",
			strings.NewReader(`{"max_tokens":9,"messages":[{"role":"user","content":"hi"}]}`))
		resp, err := bridgeClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}
	up.responses = []func() *http.Response{func() *http.Response { return jsonResponse(200, openaiResp) }, eventStreamResponse}
	if got := post("us.anthropic-lookalike.model-1"); got != http.StatusOK || up.calls() != 1 ||
		!strings.HasSuffix(up.requests[0].URL.Path, "/chat/completions") {
		t.Errorf("the lookalike: %d after %d calls, want it translated to chat completions", got, up.calls())
	}
	if got := post(opusID); got != http.StatusOK || up.calls() != 2 ||
		!strings.HasSuffix(up.requests[1].URL.Path, "/invoke") {
		t.Errorf("an Anthropic model: %d after %d calls, want it passed through", got, up.calls())
	}
}

// TestAFetchedListsMakerRoutesAClaudeModelUntranslated: where no pack or config gives the provider
// a list, the list the launch fetched says which models are Anthropic's (its maker is AWS's own
// providerName), so a Claude model on it goes to runtime's Messages route untranslated and another
// maker's is translated on the invoke route. It fails if declaredVendors stops reading the fetched
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
