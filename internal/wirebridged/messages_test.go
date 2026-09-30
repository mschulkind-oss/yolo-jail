package wirebridged

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

// The Messages pass-through's tests (messages.go; wire-bridge-gateway.md Part 2). The fake
// upstream serves what AWS documents for bedrock-runtime's Anthropic Messages route: the
// first-party request and Anthropic server-sent events back (WG-I30). No request leaves the
// process: upstreamTransport answers every one (captureUpstream, signing_test.go).

const (
	opusID     = "global.anthropic.claude-opus-5-5"
	solID      = "us.openai.gpt-6.1-sol"
	messagesAt = "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic/v1/messages"
	chatAt     = "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions"
)

// bedrockListPack is a provider pack in the shape a company pack (or packs/bedrock) ships: a
// Bedrock upstream reached at runtime's OpenAI-compatible route, and a list naming each
// model's maker as vendor. "anthropic-lookalike" is an id that SAYS anthropic and declares
// another maker, and "unlisted-vendor" declares none: the bridge reads the declaration, never
// the id (§3). "opus-plain" names opus's id with no vendor, which leaves opus's declaration
// standing; "sonnet-1m" spells its id with claude's [1m] suffix, as a list written for
// claude may; and the two "clash" aliases declare different makers for one id, which keeps
// it translated and logged (WG-I34).
const bedrockListPack = `{"name":"bedrock-list","contributes":[
 {"kind":"provider","name":"br",
  "api_key_env_name":["AWS_BEARER_TOKEN_BEDROCK","AWS_ACCESS_KEY_ID","AWS_SECRET_ACCESS_KEY"],
  "endpoints":{"openai":{"base_url":"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1","wire_api":"openai-chat-completions"}},
  "models":{"opus":"global.anthropic.claude-opus-5-5","sol":"us.openai.gpt-6.1-sol",
   "lookalike":"us.anthropic-lookalike.model-1","plain":"global.anthropic.claude-haiku-9",
   "opus-plain":"global.anthropic.claude-opus-5-5","sonnet-1m":"global.anthropic.claude-sonnet-5[1m]",
   "clash-a":"us.clash.model-1","clash-b":"us.clash.model-1"},
  "model_options":{"opus":{"vendor":"anthropic","context_window":"1000000"},"sol":{"vendor":"openai"},
   "lookalike":{"vendor":"lookalike"},"sonnet-1m":{"vendor":"anthropic"},
   "clash-a":{"vendor":"anthropic"},"clash-b":{"vendor":"qwen"}}},
 {"kind":"profile","name":"bp","provider":"br"}]}`

const (
	sonnetID = "global.anthropic.claude-sonnet-5"
	clashID  = "us.clash.model-1"
)

// composedBedrockTable composes bedrockListPack beside the shipped claude and wire-bridge
// packs, exactly as a launch does: claude supplies the spoken anthropic protocol and
// wire-bridge the adapter address, so the entry gains endpoints.anthropic at the bridge and
// model_options in packload's flat shape. It is the table the daemon reads as YOLO_PROVIDERS.
func composedBedrockTable(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	return composedBedrockTableUnder(t, nil)
}

// composedBedrockTableUnder is composedBedrockTable with a user `providers` layer composed
// over the packs, as the launch composes config.providers (packload.ComposeProviders).
func composedBedrockTableUnder(t *testing.T, user *jsonx.OrderedMap) *jsonx.OrderedMap {
	t.Helper()
	decl, problems := packdecl.Decode([]byte(bedrockListPack))
	if len(problems) != 0 {
		t.Fatalf("fixture pack invalid: %v", problems)
	}
	packs := []*packload.Pack{{Name: "bedrock-list", Root: t.TempDir(), Decl: decl}}
	for _, p := range packload.Embedded() {
		if p.Name == "claude" || p.Name == "wire-bridge" {
			packs = append(packs, p)
		}
	}
	table, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	return table
}

// servedBedrockRoute runs the PRODUCTION boot over the composed table: routeFor picks the
// route for agent, and serve builds its handler from the key channel and binds it. It fails
// if routeFor stops reading the list's vendors, if adapterHandler stops handing them to the
// signed handler, or if ServeHTTP stops routing by them: each leaves an Anthropic model
// translated, which the tests below see as a request to chat-completions.
func servedBedrockRoute(t *testing.T, agent string, keyLines ...string) (up *captureUpstream, addr string, logs func() string) {
	t.Helper()
	noReadinessPipe(t)
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	logs = captureDiag(t)
	up = withUpstream(t)
	home := t.TempDir()
	if len(keyLines) == 0 {
		keyLines = []string{"export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIDMSG'}",
			"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'msg-secret'}"}
	}
	writeKeyChannel(t, home, keyLines...)
	rt, idle := routeFor(composedBedrockTable(t), map[string]string{agent: "bp"},
		map[string]packload.ResolvedProfile{"bp": {Provider: "br"}})
	if idle != "" {
		t.Fatalf("the composed Bedrock provider did not route at the bridge: %s", idle)
	}
	if rt.ListenAddr != "127.0.0.1:8214" {
		t.Fatalf("route listens on %s; want the composed adapter address", rt.ListenAddr)
	}
	rt.ListenAddr = "127.0.0.1:0" // the composed address, moved off a port another test may hold
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serve(ctx, rt, tokenEnv(map[string]string{"JAIL_HOME": home})) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool {
		b, err := os.ReadFile(endpointFile)
		addr = strings.TrimSpace(string(b))
		return err == nil && addr != ""
	})
	return up, addr, logs
}

// anthropicSSE is a streamed Claude answer in Anthropic's documented event grammar, with a
// thinking block and cache usage: the two things translation loses.
const anthropicSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"global.anthropic.claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":4096,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing it"}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAhIM"}}` + "\n\n" +
	"event: content_block_stop\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: ping\n" + `data: {"type": "ping"}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"event: message_stop is only text here"}}` + "\n\n" +
	"event: content_block_stop\n" + `data: {"type":"content_block_stop","index":1}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":9}}` + "\n\n" +
	"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n"

// claudeRequest is what Claude Code sends: cache_control on the system prompt and a message,
// adaptive thinking, and a metadata block — every field translation drops.
const claudeRequest = `{"model":"global.anthropic.claude-opus-5-5","max_tokens":32000,"stream":true,` +
	`"system":[{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}}],` +
	`"messages":[{"role":"user","content":[{"type":"text","text":"hi <b>&</b>","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],` +
	`"thinking":{"type":"adaptive"},"metadata":{"user_id":"u-1"},"tools":[{"name":"Read","description":"read","input_schema":{"type":"object"}}]}`

func sseResponse(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"},
		"Request-Id": {"req_bedrock_1"}, "X-Amzn-Requestid": {"aws-req-1"}}, Body: body}
}

func postMessages(t *testing.T, addr, body string, header map[string]string) (*http.Response, string, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := bridgeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	return resp, string(b), rerr
}

// TestAnAnthropicModelGoesUntranslatedToBedrocksMessagesRoute is Part 2's headline, through
// the production boot: claude asks the bridge for the list's Anthropic model, and the request
// reaches bedrock-runtime's /anthropic/v1/messages BYTE-IDENTICAL, cache_control, thinking and
// metadata included, with claude's version and beta headers, a SigV4 signature for
// us-east-1/bedrock that verifies against exactly what was sent, and never the caller token.
// The Anthropic stream comes back byte for byte, thinking block and cache usage included.
func TestAnAnthropicModelGoesUntranslatedToBedrocksMessagesRoute(t *testing.T) {
	up, addr, logs := servedBedrockRoute(t, "claude")
	up.responses = []func() *http.Response{func() *http.Response {
		return sseResponse(io.NopCloser(strings.NewReader(anthropicSSE)))
	}}
	resp, got, err := postMessages(t, addr, claudeRequest, map[string]string{
		"Anthropic-Version": "2023-06-01",
		"Anthropic-Beta":    "interleaved-thinking-2025-05-14,context-1m-2025-08-07",
		"Accept":            "application/json",
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, read error %v: %s", resp.StatusCode, err, got)
	}
	if up.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.calls())
	}
	sent, body := up.requests[0], up.bodies[0]
	if sent.Method != http.MethodPost || sent.URL.String() != messagesAt {
		t.Fatalf("upstream %s %s, want POST %s", sent.Method, sent.URL, messagesAt)
	}
	if string(body) != claudeRequest {
		t.Errorf("the body changed on the way:\n sent %s\n want %s", body, claudeRequest)
	}
	if sent.Header.Get("Anthropic-Version") != "2023-06-01" ||
		sent.Header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14,context-1m-2025-08-07" ||
		sent.Header.Get("Content-Type") != "application/json" || sent.Header.Get("Accept") != "application/json" {
		t.Errorf("Messages headers not carried: %v", sent.Header)
	}
	auth := sent.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDMSG/") || !strings.Contains(auth, "/us-east-1/bedrock/aws4_request") {
		t.Fatalf("Authorization = %q, want SigV4 for us-east-1/bedrock from the key channel", auth)
	}
	if !strings.Contains(auth, "anthropic-beta") || !strings.Contains(auth, "anthropic-version") {
		t.Errorf("the signature must cover the Messages headers: %s", auth)
	}
	for k, vs := range sent.Header {
		for _, v := range vs {
			if strings.Contains(v, testCallerToken) {
				t.Errorf("the caller token crossed upstream in %s", k)
			}
		}
	}
	if sent.Header.Get("X-Api-Key") != "" {
		t.Errorf("a signed request carried x-api-key too: a signature and a key never travel together")
	}
	verifySignature(t, sent, body, "AKIDMSG", "msg-secret")

	if got != anthropicSSE {
		t.Errorf("the stream changed on the way:\n got %q\nwant %q", got, anthropicSSE)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want the upstream's text/event-stream", ct)
	}
	if resp.Header.Get("Request-Id") != "req_bedrock_1" || resp.Header.Get("X-Amzn-Requestid") != "aws-req-1" {
		t.Errorf("request ids not relayed: %v", resp.Header)
	}
	l := logs()
	for _, want := range []string{
		// opus is carried though "opus-plain" names its id with no vendor; sonnet's id is
		// the list's [1m] spelling trimmed; the clash id is not carried (WG-I34).
		"Anthropic models on its list pass untranslated to " + messagesAt + ": " + opusID + ", " + sonnetID + "\n",
		"POST /v1/messages 200",
		"(model " + opusID + " is Anthropic's on the provider's list: untranslated to " + messagesAt + ")",
		`provider "br"'s list names ` + clashID + " under aliases declaring different vendors, so the bridge " +
			"translates it as it does any model not declared Anthropic's (wire-bridge-gateway.md WG-I34)",
	} {
		if !strings.Contains(l, want) {
			t.Errorf("the daemon log lacks %q:\n%s", want, l)
		}
	}
	if strings.Contains(l, "You are Claude Code") || strings.Contains(l, "weighing it") {
		t.Errorf("a body reached the log:\n%s", l)
	}
}

func verifySignature(t *testing.T, sent *http.Request, body []byte, akid, secret string) {
	t.Helper()
	when, err := time.Parse("20060102T150405Z", sent.Header.Get("X-Amz-Date"))
	if err != nil {
		t.Fatalf("X-Amz-Date: %v", err)
	}
	check, _ := http.NewRequest(sent.Method, sent.URL.String(), bytes.NewReader(body))
	for k, vs := range sent.Header {
		if k != "Authorization" && k != "X-Amz-Date" && k != "X-Amz-Security-Token" {
			check.Header[k] = vs
		}
	}
	if err := sigv4.Sign(check, body, sigv4.Credentials{AccessKeyID: akid, SecretAccessKey: secret},
		sigv4.Options{Region: "us-east-1", Service: sigv4.BedrockService, Time: when}); err != nil {
		t.Fatal(err)
	}
	if check.Header.Get("Authorization") != sent.Header.Get("Authorization") {
		t.Errorf("the signature does not verify against what was sent")
	}
}

// TestEveryOtherModelOnTheRouteIsStillTranslated: the same served route sends a model the
// list declares another maker's, one whose id says "anthropic" but whose declared vendor does
// not, one the list names with no vendor, one it does not name, and one two aliases declare
// different makers for, to chat-completions, translated, as before Part 2. The id is never
// parsed for a maker.
func TestEveryOtherModelOnTheRouteIsStillTranslated(t *testing.T) {
	up, addr, _ := servedBedrockRoute(t, "claude")
	for i, model := range []string{solID, "us.anthropic-lookalike.model-1", "global.anthropic.claude-haiku-9",
		"unlisted.anthropic.claude-x", clashID} {
		body := strings.Replace(claudeRequest, opusID, model, 1)
		resp, got, err := postMessages(t, addr, strings.Replace(body, `"stream":true,`, "", 1), nil)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, read error %v: %s", model, resp.StatusCode, err, got)
		}
		if up.calls() != i+1 {
			t.Fatalf("%s: upstream calls = %d, want %d", model, up.calls(), i+1)
		}
		sent, sentBody := up.requests[i], up.bodies[i]
		if sent.URL.String() != chatAt {
			t.Errorf("%s went to %s, want the translating route %s", model, sent.URL, chatAt)
		}
		if bytes.Contains(sentBody, []byte("cache_control")) || !bytes.Contains(sentBody, []byte(`"model":"`+model+`"`)) {
			t.Errorf("%s was not translated: %s", model, sentBody)
		}
	}
}

// TestCopilotOnABedrockRouteGetsTheSamePassThrough: copilot's derive prefers the anthropic
// endpoint (cerebras-pack-and-copilot-delivery.md D-3), so a Bedrock provider selected for
// copilot is the same adapter route, and its Anthropic models pass through the same way.
func TestCopilotOnABedrockRouteGetsTheSamePassThrough(t *testing.T) {
	up, addr, _ := servedBedrockRoute(t, "copilot")
	up.responses = []func() *http.Response{func() *http.Response {
		return sseResponse(io.NopCloser(strings.NewReader(anthropicSSE)))
	}}
	resp, got, err := postMessages(t, addr, claudeRequest, nil)
	if err != nil || resp.StatusCode != http.StatusOK || got != anthropicSSE {
		t.Fatalf("status %d, read error %v, stream intact %v", resp.StatusCode, err, got == anthropicSSE)
	}
	if up.requests[0].URL.String() != messagesAt || string(up.bodies[0]) != claudeRequest {
		t.Errorf("copilot's request went to %s, body intact %v", up.requests[0].URL, string(up.bodies[0]) == claudeRequest)
	}
	if up.requests[0].Header.Get("Anthropic-Version") != defaultAnthropicVersion {
		t.Errorf("a client that sends no anthropic-version gets the one the route requires, %s: got %q",
			defaultAnthropicVersion, up.requests[0].Header.Get("Anthropic-Version"))
	}
}

// TestAListIDSpelledWithTheOneMillionSuffixPassesThrough: a list written for claude may
// spell a Claude id with its [1m] client suffix, the spelling claude needs to use the
// 1M-context variant. That id is the same wire model, so a request for it, with or without
// the suffix, goes untranslated with the suffix trimmed from the body.
func TestAListIDSpelledWithTheOneMillionSuffixPassesThrough(t *testing.T) {
	up, addr, _ := servedBedrockRoute(t, "claude")
	for i, model := range []string{sonnetID + "[1m]", sonnetID} {
		up.responses = append(up.responses, func() *http.Response { return jsonResponse(200, `{"type":"message"}`) })
		body := strings.Replace(strings.Replace(claudeRequest, opusID, model, 1), `"stream":true,`, "", 1)
		resp, got, err := postMessages(t, addr, body, nil)
		if err != nil || resp.StatusCode != http.StatusOK || up.calls() != i+1 {
			t.Fatalf("%s: status %d, read error %v, calls %d: %s", model, resp.StatusCode, err, up.calls(), got)
		}
		if up.requests[i].URL.String() != messagesAt {
			t.Errorf("%s went to %s, want the Messages route", model, up.requests[i].URL)
		}
		if !bytes.Contains(up.bodies[i], []byte(`"model":"`+sonnetID+`"`)) || bytes.Contains(up.bodies[i], []byte("[1m]")) {
			t.Errorf("%s: the body sent must name the wire id %s: %s", model, sonnetID, up.bodies[i])
		}
	}
}

// TestAUserLayerOverThePackListRoutesByTheDeclaredVendor composes a user's `providers` entry
// over the pack's list, as a launch does, and reads the route the daemon boots with:
//   - the user points the pack's opus alias at a DeepSeek id, as a plain string: the pack's
//     vendor was the pack model's maker, not DeepSeek's, so that id is translated (it would
//     otherwise go untranslated to the Anthropic route and fail at AWS);
//   - the user adds an alias of their own for the pack's sonnet id: it declares no maker, so
//     the pack's declaration stands and the id stays on the pass-through, with no conflict.
func TestAUserLayerOverThePackListRoutesByTheDeclaredVendor(t *testing.T) {
	const deepseek = "us.deepseek.r1-v1:0"
	user := mustProviders(t, `{"br":{"models":{"opus":"`+deepseek+`","mine":"`+sonnetID+`"}}}`)
	rt, idle := routeFor(composedBedrockTableUnder(t, user), map[string]string{"claude": "bp"},
		map[string]packload.ResolvedProfile{"bp": {Provider: "br"}})
	if idle != "" {
		t.Fatalf("idle: %s", idle)
	}
	if rt.AnthropicModels[deepseek] {
		t.Errorf("a user's DeepSeek id under the pack's opus alias inherited the pack's vendor: %v", rt.AnthropicModels)
	}
	if rt.AnthropicModels[opusID] {
		t.Errorf("opus's id is named only by opus-plain now, which declares no vendor: %v", rt.AnthropicModels)
	}
	if !rt.AnthropicModels[sonnetID] {
		t.Errorf("a user alias declaring no vendor took the pack's sonnet off the pass-through: %v", rt.AnthropicModels)
	}
	if fmt.Sprint(rt.VendorConflicts) != "["+clashID+"]" {
		t.Errorf("VendorConflicts = %v, want only the pack's own clash", rt.VendorConflicts)
	}
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	home := t.TempDir()
	writeKeyChannel(t, home, "export AWS_ACCESS_KEY_ID='AKID'", "export AWS_SECRET_ACCESS_KEY='s'")
	h, _, idle := adapterHandler(rt, tokenEnv(map[string]string{"JAIL_HOME": home}))
	if idle != "" {
		t.Fatalf("adapter handler idle: %s", idle)
	}
	passthrough := h.(*bridgeHandler).messages
	if passthrough == nil {
		t.Fatalf("the handler has no Messages pass-through, though the pack's sonnet is still Anthropic's")
	}
	if _, claimed := passthrough.claims(deepseek); claimed {
		t.Errorf("the handler claims the user's DeepSeek id for the Messages route")
	}
	if _, claimed := passthrough.claims(sonnetID); !claimed {
		t.Errorf("the handler does not claim the pack's sonnet")
	}
}

// TestAVendorTheUserDeclaresRoutesTheirModel: a user's object-form entry declares its own
// id's maker as `vendor` (config.knownModelKeys, bedrock-plumbing.md OQ-BR9), and the bridge
// reads it exactly as it reads a pack's, through the production ComposeProviders and routeFor:
//   - a Claude id the user adds, declared anthropic, goes on the pass-through;
//   - the pack's opus alias re-pointed at a DeepSeek id declared deepseek is translated, the
//     pack's anthropic not carried under it;
//   - a user alias declaring another maker for the pack's sonnet id makes that id a conflict,
//     so it is translated and named for the serve log (WG-I34).
func TestAVendorTheUserDeclaresRoutesTheirModel(t *testing.T) {
	const (
		deepseek = "us.deepseek.r1-v1:0"
		newer    = "global.anthropic.claude-sonnet-6"
	)
	user := mustProviders(t, `{"br":{"models":{
	 "opus":{"id":"`+deepseek+`","vendor":"deepseek"},
	 "mine":{"id":"`+newer+`","vendor":"anthropic"},
	 "sonnet-as-qwen":{"id":"`+sonnetID+`","vendor":"qwen"}}}}`)
	rt, idle := routeFor(composedBedrockTableUnder(t, user), map[string]string{"claude": "bp"},
		map[string]packload.ResolvedProfile{"bp": {Provider: "br"}})
	if idle != "" {
		t.Fatalf("idle: %s", idle)
	}
	if !rt.AnthropicModels[newer] {
		t.Errorf("a Claude id the user declared anthropic is not on the pass-through: %v", rt.AnthropicModels)
	}
	if rt.AnthropicModels[deepseek] || rt.AnthropicModels[opusID] {
		t.Errorf("the re-pointed opus alias must carry only the user's deepseek: %v", rt.AnthropicModels)
	}
	if rt.AnthropicModels[sonnetID] {
		t.Errorf("sonnet's id is declared anthropic by the pack and qwen by the user, so it is no maker's: %v", rt.AnthropicModels)
	}
	if fmt.Sprint(rt.VendorConflicts) != "["+sonnetID+" "+clashID+"]" {
		t.Errorf("VendorConflicts = %v, want the user's sonnet clash beside the pack's own", rt.VendorConflicts)
	}
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	home := t.TempDir()
	writeKeyChannel(t, home, "export AWS_ACCESS_KEY_ID='AKID'", "export AWS_SECRET_ACCESS_KEY='s'")
	h, _, idle := adapterHandler(rt, tokenEnv(map[string]string{"JAIL_HOME": home}))
	if idle != "" {
		t.Fatalf("adapter handler idle: %s", idle)
	}
	passthrough := h.(*bridgeHandler).messages
	if passthrough == nil {
		t.Fatalf("the handler has no Messages pass-through, though the user declared a Claude id")
	}
	if _, claimed := passthrough.claims(newer); !claimed {
		t.Errorf("the handler does not claim the user's Claude id")
	}
	if _, claimed := passthrough.claims(deepseek); claimed {
		t.Errorf("the handler claims the user's DeepSeek id for the Messages route")
	}
}

// messagesHandler is the signed adapter handler with the list's Anthropic ids, for the
// handler-level cases.
func messagesHandler(env sigv4.Env) *bridgeHandler {
	return newSignedChatHandler(bedrockBase, wirebridge.ChatOptions{},
		&bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{Env: env}}, map[string]bool{opusID: true})
}

func servePathTo(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// TestABedrockAPIKeyGoesAsXAPIKeyOnTheMessagesRoute: with AWS_BEARER_TOKEN_BEDROCK the only
// source, the key rides as x-api-key, the header AWS documents for this route, with no
// Authorization and no signature (WG-I31).
func TestABedrockAPIKeyGoesAsXAPIKeyOnTheMessagesRoute(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response { return jsonResponse(200, `{"type":"message"}`) }}
	rec := postMessagesTo(t, messagesHandler(sigv4.Env{Bearer: "bedrock-api-key"}), strings.Replace(claudeRequest, `"stream":true,`, "", 1))
	if rec.Code != http.StatusOK || up.calls() != 1 {
		t.Fatalf("status %d, calls %d: %s", rec.Code, up.calls(), rec.Body)
	}
	h := up.requests[0].Header
	if h.Get("X-Api-Key") != "bedrock-api-key" || h.Get("Authorization") != "" || h.Get("X-Amz-Date") != "" {
		t.Errorf("want the key as x-api-key alone: %v", h)
	}
}

func postMessagesTo(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return servePathTo(t, h, "/v1/messages", body)
}

// TestAnUnstreamedAnswerIsRelayedVerbatim: a non-streaming request's JSON answer comes back
// as the upstream wrote it, at its status, with its type, and it is not a cut stream. It runs
// over a real listener, because only there does the relay's abort reach the agent: a JSON
// answer watched for an SSE closing event would end "early" and be aborted, which an
// httptest recorder cannot show.
func TestAnUnstreamedAnswerIsRelayedVerbatim(t *testing.T) {
	up, addr, logs := servedBedrockRoute(t, "claude")
	const answer = `{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"cache_read_input_tokens":2048,"output_tokens":2}}`
	up.responses = []func() *http.Response{func() *http.Response { return jsonResponse(200, answer) }}
	resp, got, err := postMessages(t, addr, strings.Replace(claudeRequest, `"stream":true,`, "", 1), nil)
	if err != nil || resp.StatusCode != http.StatusOK || got != answer || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("got %d %s %q, read error %v; want the answer verbatim and a clean end",
			resp.StatusCode, resp.Header.Get("Content-Type"), got, err)
	}
	if up.calls() != 1 || up.requests[0].URL.String() != messagesAt {
		t.Fatalf("calls %d; want one request to the Messages route", up.calls())
	}
	waitFor(t, func() bool { return strings.Contains(logs(), "POST /v1/messages 200") })
	if l := logs(); strings.Contains(l, "ended early") || strings.Contains(l, "aborted") {
		t.Errorf("a JSON answer was treated as a cut stream:\n%s", l)
	}
}

// TestTheStreamWatchFollowsTheAnswersFraming: the end-of-stream watch reads the grammar the
// bytes are in, so an SSE answer cut before message_stop aborts the agent's connection even
// when the request did not ask to stream.
func TestTheStreamWatchFollowsTheAnswersFraming(t *testing.T) {
	addr, body, pw, logs := streamedBedrockRoute(t)
	const partial = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n"
	go func() {
		_, _ = io.WriteString(pw, partial)
		_ = pw.Close()
	}()
	_, got, rerr := postMessages(t, addr, strings.Replace(claudeRequest, `"stream":true,`, "", 1), nil)
	if rerr == nil || got != partial {
		t.Errorf("read %q with error %v; want the partial bytes and then a failed read", got, rerr)
	}
	waitClosed(t, body)
	waitFor(t, func() bool { return strings.Contains(logs(), "without a message_stop or error event") })
}

// TestAnAgentThatHangsUpMidStreamIsNoCut: the agent closing its own request mid-stream is no
// upstream fault. The upstream body then fails with the request's cancellation, as a real
// transport's does, and the relay reports nothing: no "ended early" line, no abort.
func TestAnAgentThatHangsUpMidStreamIsNoCut(t *testing.T) {
	addr, body, pw, logs := streamedBedrockRoute(t)
	up := upstreamTransport.(*captureUpstream)
	const first = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/messages", strings.NewReader(claudeRequest))
	req.Header.Set("Content-Type", "application/json")
	go func() { _, _ = io.WriteString(pw, first) }()
	resp, err := bridgeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != first {
		t.Fatalf("read %q, %v; want the first event", buf, err)
	}
	// The transport ends an in-flight body read once its request's context is done.
	up.mu.Lock()
	sent := up.requests[0]
	up.mu.Unlock()
	go func() {
		<-sent.Context().Done()
		_ = pw.CloseWithError(sent.Context().Err())
	}()
	cancel()
	waitClosed(t, body)
	waitFor(t, func() bool { return strings.Contains(logs(), "POST /v1/messages 200") })
	if l := logs(); strings.Contains(l, "ended early") || strings.Contains(l, "aborted") {
		t.Errorf("the agent's own hang-up was logged as a cut:\n%s", l)
	}
}

// TestMessagesRefusalsKeepTheirStatusInAnthropicsShape (WG-I32): the route speaks the agent's
// protocol, so a refusal keeps its status. An Anthropic-shaped body is relayed as sent; AWS's
// own envelope is put into the Anthropic shape with AWS's message and the status's type.
func TestMessagesRefusalsKeepTheirStatusInAnthropicsShape(t *testing.T) {
	for _, c := range []struct {
		name, body, wantType, wantMsg string
		status                        int
		verbatim                      bool
	}{
		{"anthropic 400", `{"type":"error","error":{"type":"invalid_request_error","message":"thinking: bad budget"},"request_id":"r1"}`,
			"invalid_request_error", "thinking: bad budget", 400, true},
		{"anthropic 529", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			"overloaded_error", "Overloaded", 529, true},
		{"aws 403", `{"message":"User is not authorized to perform: bedrock:InvokeModel"}`,
			"permission_error", "not authorized to perform: bedrock:InvokeModel", 403, false},
		{"aws 429", `{"Message":"Too many tokens, please wait before trying again."}`,
			"rate_limit_error", "Too many tokens", 429, false},
		{"aws 503 html", `<html>unavailable</html>`, "api_error", "upstream returned 503", 503, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			up := withUpstream(t)
			up.responses = []func() *http.Response{func() *http.Response {
				r := jsonResponse(c.status, c.body)
				r.Header.Set("Retry-After", "7")
				r.Header.Set("X-Should-Retry", "true")
				r.Header.Set("Request-Id", "req_refused_1")
				r.Header.Set("X-Amzn-Requestid", "aws-refused-1")
				r.Header.Set("X-Amzn-Errortype", "ValidationException")
				return r
			}}
			rec := postMessagesTo(t, messagesHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}), claudeRequest)
			if rec.Code != c.status || up.calls() != 1 {
				t.Fatalf("status %d after %d calls, want %d relayed once", rec.Code, up.calls(), c.status)
			}
			if c.verbatim && rec.Body.String() != c.body {
				t.Errorf("an Anthropic-shaped refusal changed: %s", rec.Body)
			}
			var doc struct {
				Type  string `json:"type"`
				Error struct{ Type, Message string }
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Type != "error" ||
				doc.Error.Type != c.wantType || !strings.Contains(doc.Error.Message, c.wantMsg) {
				t.Errorf("body %s, want an Anthropic %s carrying %q", rec.Body, c.wantType, c.wantMsg)
			}
			// The retry hints and the request ids a support case names come back (WG-I32);
			// nothing else of AWS's does.
			for k, want := range map[string]string{"Retry-After": "7", "X-Should-Retry": "true",
				"Request-Id": "req_refused_1", "X-Amzn-Requestid": "aws-refused-1", "X-Amzn-Errortype": ""} {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s = %q, want %q: %v", k, got, want, rec.Header())
				}
			}
		})
	}
}

// TestAnExpiredSignatureOnTheMessagesRouteIsRefreshedOnce: the route shares the signer's
// chain and its §2.1 rule: an expiry refreshes the credential and retries exactly once.
func TestAnExpiredSignatureOnTheMessagesRouteIsRefreshedOnce(t *testing.T) {
	up := withUpstream(t)
	expired := func() *http.Response {
		r := jsonResponse(403, `{"message":"Signature expired: 20260929T000000Z is now earlier than 20260929T000500Z"}`)
		r.Header.Set("X-Amzn-ErrorType", "InvalidSignatureException")
		return r
	}
	up.responses = []func() *http.Response{expired, func() *http.Response { return jsonResponse(200, `{"type":"message"}`) }}
	adapter, fetches := credAdapter(t)
	rec := postMessagesTo(t, messagesHandler(sigv4.Env{ContainerURI: adapter.URL + "/credentials"}),
		strings.Replace(claudeRequest, `"stream":true,`, "", 1))
	if rec.Code != http.StatusOK || up.calls() != 2 || *fetches != 2 {
		t.Fatalf("status %d, calls %d, fetches %d; want 200 after one refresh and one retry", rec.Code, up.calls(), *fetches)
	}
	if up.requests[1].URL.String() != messagesAt || !strings.Contains(up.requests[1].Header.Get("Authorization"), "Credential=ASIA2/") ||
		!bytes.Equal(up.bodies[0], up.bodies[1]) {
		t.Errorf("the retry must resend the same body to the Messages route with the refreshed set")
	}
}

// TestTheMessagesRouteKeepsTheSignersFailureStatuses (WG-I31, §2.1): the pass-through shares
// the route's credential chain, so it answers a credential it cannot resolve as the
// translating upstream does, in Anthropic's shape and before any upstream call: no source is
// a 401 naming the three it tried, and an unreachable aws-auth pointer a 503 naming aws-auth
// and `aws sso login`. Neither is the 502 of an unavailable upstream.
func TestTheMessagesRouteKeepsTheSignersFailureStatuses(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURI := dead.URL + "/credentials"
	dead.Close()
	for _, c := range []struct {
		name     string
		env      sigv4.Env
		status   int
		typ      string
		mentions []string
	}{
		{"no credential source", sigv4.Env{}, http.StatusUnauthorized, "authentication_error",
			[]string{"AWS_ACCESS_KEY_ID", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_BEARER_TOKEN_BEDROCK"}},
		{"aws-auth unreachable", sigv4.Env{ContainerURI: deadURI}, http.StatusServiceUnavailable, "api_error",
			[]string{"aws-auth", "aws sso login"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			up := withUpstream(t)
			rec := postMessagesTo(t, messagesHandler(c.env), claudeRequest)
			var doc struct {
				Type  string `json:"type"`
				Error struct{ Type, Message string }
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Type != "error" {
				t.Fatalf("body %s is not Anthropic's error shape", rec.Body)
			}
			if rec.Code != c.status || up.calls() != 0 {
				t.Fatalf("status %d after %d upstream calls, want %d and none: %s", rec.Code, up.calls(), c.status, rec.Body)
			}
			if doc.Error.Type != c.typ {
				t.Errorf("error type %q, want %q", doc.Error.Type, c.typ)
			}
			for _, want := range c.mentions {
				if !strings.Contains(doc.Error.Message, want) {
					t.Errorf("the %d must name %s: %s", c.status, want, doc.Error.Message)
				}
			}
		})
	}
}

// TestAnEventStreamAnswerIsRefusedByName (WG-I30): the stream's framing is SOURCED, not
// observed. If the route ever answers with AWS's binary event stream, the agent gets a 502
// naming it, never bytes it cannot parse.
func TestAnEventStreamAnswerIsRefusedByName(t *testing.T) {
	logs := captureDiag(t)
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {awsEventStreamType}},
			Body: io.NopCloser(bytes.NewReader([]byte{0, 0, 0, 0x55, 0, 0, 0, 0x4b}))}
	}}
	rec := postMessagesTo(t, messagesHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}), claudeRequest)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), awsEventStreamType) ||
		!strings.Contains(rec.Body.String(), `"type":"error"`) {
		t.Errorf("got %d %s, want an Anthropic 502 naming %s", rec.Code, rec.Body, awsEventStreamType)
	}
	if !strings.Contains(logs(), "AWS's binary event stream") {
		t.Errorf("the daemon log does not name the framing:\n%s", logs())
	}
}

// TestTheOneMillionSuffixIsTrimmedForLookupAndOnTheWire: claude's [1m] client spelling is
// looked up without the suffix and never reaches AWS; every other member arrives unchanged.
func TestTheOneMillionSuffixIsTrimmedForLookupAndOnTheWire(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response { return jsonResponse(200, `{"type":"message"}`) }}
	in := strings.Replace(strings.Replace(claudeRequest, opusID, opusID+"[1m]", 1), `"stream":true,`, "", 1)
	rec := postMessagesTo(t, messagesHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}), in)
	if rec.Code != http.StatusOK || up.calls() != 1 || up.requests[0].URL.String() != messagesAt {
		t.Fatalf("status %d, calls %d: a [1m] Anthropic model must still pass through", rec.Code, up.calls())
	}
	var sent, want map[string]any
	if err := json.Unmarshal(up.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(strings.Replace(claudeRequest, `"stream":true,`, "", 1)), &want)
	if fmt.Sprint(sent) != fmt.Sprint(want) {
		t.Errorf("members changed beyond the model:\n sent %v\n want %v", sent, want)
	}
	if bytes.Contains(up.bodies[0], []byte(`\u003c`)) {
		t.Errorf("re-encoding escaped HTML in a string: %s", up.bodies[0])
	}
}

// TestCountTokensStaysRefusedForAnAnthropicModel (WG-I35): runtime documents no count_tokens
// on its Messages route, and a CRIS-only Claude model has none on runtime at all, so the
// refusal that sends claude to its own estimator (WB-D14) stands for every model.
func TestCountTokensStaysRefusedForAnAnthropicModel(t *testing.T) {
	up := withUpstream(t)
	rec := servePathTo(t, messagesHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}),
		"/v1/messages/count_tokens", `{"model":"`+opusID+`","messages":[]}`)
	if rec.Code != http.StatusNotFound || up.calls() != 0 {
		t.Errorf("status %d, calls %d; want 404 and no upstream call", rec.Code, up.calls())
	}
}

// TestTheMessagesRouteBoundsOnlyTheWaitForHeaders: the pass-through relays a long stream
// (thinking can run long), so like the via route it bounds the wait for response headers and
// never the body (WG-I24's rule, WG-I33).
func TestTheMessagesRouteBoundsOnlyTheWaitForHeaders(t *testing.T) {
	withViaHeaderTimeout(t, 50*time.Millisecond)
	old := upstreamTransport
	upstreamTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(5 * time.Second):
			// Fail fast rather than hang: a request no header bound cancels would otherwise
			// wait out whatever timeout its client has.
			return nil, fmt.Errorf("no header bound cancelled the request")
		}
	})
	t.Cleanup(func() { upstreamTransport = old })
	rec := postMessagesTo(t, messagesHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}), claudeRequest)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "sent no response headers in time (50ms)") {
		t.Errorf("got %d %s, want an Anthropic 504 naming the bound", rec.Code, rec.Body)
	}
	if c := newMessagesPassthrough(bedrockBase, map[string]bool{opusID: true},
		&bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{}}).client; c.Timeout != 0 {
		t.Errorf("the Messages pass-through's client has Timeout %s, which also bounds the stream's read", c.Timeout)
	}
}

// streamedBedrockRoute serves a route whose one upstream answer is a stream the test drives.
func streamedBedrockRoute(t *testing.T) (addr string, body *stubStreamBody, pw *io.PipeWriter, logs func() string) {
	t.Helper()
	up, addr, logs := servedBedrockRoute(t, "claude")
	body, pw = newStubStream()
	up.responses = []func() *http.Response{func() *http.Response { return sseResponse(body) }}
	return addr, body, pw, logs
}

// TestAMessagesStreamCutShortAbortsTheAgentsConnection (WG-I33): an upstream that fails after
// the status line makes claude's read FAIL, so a truncated answer is never taken as finished,
// and the log names the cut.
func TestAMessagesStreamCutShortAbortsTheAgentsConnection(t *testing.T) {
	addr, body, pw, logs := streamedBedrockRoute(t)
	const first = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n"
	go func() {
		_, _ = io.WriteString(pw, first)
		_ = pw.CloseWithError(fmt.Errorf("read tcp: connection reset by peer"))
	}()
	_, got, rerr := postMessages(t, addr, claudeRequest, nil)
	if rerr == nil {
		t.Errorf("claude read %q and a clean end of stream; want a failed read", got)
	}
	if got != first {
		t.Errorf("bytes before the cut = %q, want %q", got, first)
	}
	waitClosed(t, body)
	waitFor(t, func() bool { return strings.Contains(logs(), "connection reset by peer") })
	if l := logs(); !strings.Contains(l, "the Messages stream for model "+opusID+" ended early after") ||
		!strings.Contains(l, "the stream was cut short and the agent's connection aborted") {
		t.Errorf("the daemon log does not name the cut:\n%s", l)
	}
}

// TestAMessagesStreamEndingBeforeMessageStopAborts: an upstream that ends the stream cleanly
// before message_stop is a truncated answer too.
func TestAMessagesStreamEndingBeforeMessageStopAborts(t *testing.T) {
	addr, body, pw, logs := streamedBedrockRoute(t)
	const partial = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"event: message_stop\"}}\n\n"
	go func() {
		_, _ = io.WriteString(pw, partial)
		_ = pw.Close()
	}()
	_, got, rerr := postMessages(t, addr, claudeRequest, nil)
	if rerr == nil || got != partial {
		t.Errorf("read %q with error %v; want the partial bytes and then a failed read", got, rerr)
	}
	waitClosed(t, body)
	waitFor(t, func() bool { return strings.Contains(logs(), "without a message_stop or error event") })
}

// TestAMessagesStreamEndingOnAnErrorEventIsRelayedAsIs: Anthropic ends a failed stream with an
// error event, which closes the grammar; the agent reads it and a clean end, no abort.
func TestAMessagesStreamEndingOnAnErrorEventIsRelayedAsIs(t *testing.T) {
	addr, body, pw, logs := streamedBedrockRoute(t)
	const failed = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	go func() {
		_, _ = io.WriteString(pw, failed)
		_ = pw.Close()
	}()
	_, got, rerr := postMessages(t, addr, claudeRequest, nil)
	if rerr != nil || got != failed {
		t.Errorf("read %q with error %v; want the error event and a clean end", got, rerr)
	}
	waitClosed(t, body)
	if strings.Contains(logs(), "ended early") {
		t.Errorf("an error event was treated as a cut:\n%s", logs())
	}
}

// TestSSECloseFindsTheClosingEventAtALineHeadOnly: the watcher sees message_stop split across
// reads and in a data-only stream, and never inside a delta's text.
func TestSSECloseFindsTheClosingEventAtALineHeadOnly(t *testing.T) {
	for _, c := range []struct {
		name   string
		chunks []string
		done   bool
	}{
		{"event line split across reads", []string{"event: mess", "age_stop\r", "\ndata: {}\n\n"}, true},
		{"data-only stream", []string{`data: {"type": "message_stop"}` + "\n\n"}, true},
		{"error event", []string{"event:error\n"}, true},
		{"marker inside a delta", []string{`data: {"type":"content_block_delta","delta":{"text":"event: message_stop {\"type\":\"message_stop\"}"}}` + "\n"}, false},
		{"marker with no newline yet", []string{"event: message_stop"}, false},
		{"a long delta line, then the stop", []string{"data: " + strings.Repeat("x", 100000) + "\n", "event: message_stop\n"}, true},
	} {
		var s sseClose
		for _, ch := range c.chunks {
			s.feed([]byte(ch))
		}
		if s.done != c.done {
			t.Errorf("%s: done = %v, want %v", c.name, s.done, c.done)
		}
		if len(s.head) > sseHeadLen {
			t.Errorf("%s: kept %d bytes of a line, want at most %d", c.name, len(s.head), sseHeadLen)
		}
	}
}

// TestAnthropicModelIDsReadsTheDeclaredVendor pins the classification on the composed table:
// only a declared vendor "anthropic" counts, an id two aliases declare different vendors for
// is none, an alias declaring no vendor neither adds nor removes one (m-4), a list id spelled
// with claude's [1m] suffix is keyed by its wire id (m-5), and a provider with no
// model_options has no Anthropic model.
func TestAnthropicModelIDsReadsTheDeclaredVendor(t *testing.T) {
	entry := mustProviders(t, `{"p":{
		"models":{"a":"m-1","b":"m-2","c":"anthropic.claude-x","d":"m-3","e":"m-3","f":"m-1",
			"g":"m-4","h":"m-4","i":"m-5[1m]"},
		"model_options":{"a":{"vendor":"anthropic"},"b":{"vendor":"openai"},"d":{"vendor":"anthropic"},"e":{"vendor":"qwen"},
			"f":{"vendor":"anthropic","context_window":"1000000"},"g":{"vendor":"anthropic"},"h":{"context_window":"8192"},
			"i":{"vendor":"anthropic"}}}}`)
	v, _ := entry.Get("p")
	ids, conflicts := anthropicModelIDs(v.(*jsonx.OrderedMap))
	if len(ids) != 3 || !ids["m-1"] || !ids["m-4"] || !ids["m-5"] {
		t.Errorf("ids = %v, want m-1, m-4 and m-5 (the id is never parsed, m-3's aliases disagree, "+
			"m-4's second alias declares no vendor, and m-5 is listed as m-5[1m])", ids)
	}
	if fmt.Sprint(conflicts) != "[m-3]" {
		t.Errorf("conflicts = %v, want [m-3]", conflicts)
	}
	bare := mustProviders(t, `{"p":{"models":{"a":"anthropic.claude-x"}}}`)
	v, _ = bare.Get("p")
	if ids, _ := anthropicModelIDs(v.(*jsonx.OrderedMap)); len(ids) != 0 {
		t.Errorf("a list declaring no vendor has Anthropic ids %v", ids)
	}
}

// TestOnlyABedrockRouteCarriesAnthropicModels: the pass-through exists where the route is
// Bedrock's (its Messages route is composed from runtime's host); another provider's
// Anthropic-vendor entries stay translated, since its anthropic endpoint IS the bridge.
func TestOnlyABedrockRouteCarriesAnthropicModels(t *testing.T) {
	for upstream, want := range map[string]bool{
		bedrockBase:                  true,
		"https://api.cerebras.ai/v1": false,
		"https://bedrock-runtime-fips.us-east-1.amazonaws.com/openai/v1": false,
	} {
		providers := fmt.Sprintf(`{"b":{"endpoints":{
			"anthropic":{"base_url":"http://127.0.0.1:8214","wire_api":"anthropic"},
			"openai":{"base_url":%q,"wire_api":"openai-chat-completions"}},
			"models":{"o":%q},"model_options":{"o":{"vendor":"anthropic"}}}}`, upstream, opusID)
		r, idle := resolveRoute(entrypoint.NewEnv(map[string]string{
			"YOLO_PROVIDERS":    providers,
			"YOLO_PROFILES":     `{"bp":{"provider":"b"}}`,
			"YOLO_USE_PROFILES": `{"claude":"bp"}`,
		}))
		if idle != "" {
			t.Fatalf("%s: idle %s", upstream, idle)
		}
		if got := r.AnthropicModels[opusID]; got != want {
			t.Errorf("%s: carries the Anthropic model = %v, want %v", upstream, got, want)
		}
	}
	if newMessagesPassthrough(bedrockBase, map[string]bool{opusID: true}, nil) != nil {
		t.Errorf("a route with no signer got a Messages pass-through")
	}
	// A PROVIDER WHOSE PLATFORM SAYS BEDROCK is one at any https address (WG-I37): its Messages
	// route is composed on the host its own address names, a FIPS endpoint here.
	const fips = "https://bedrock-runtime-fips.us-east-1.amazonaws.com/openai/v1"
	providers := fmt.Sprintf(`{"b":{"platform":"aws-bedrock","region":"us-east-1","endpoints":{
		"anthropic":{"base_url":"http://127.0.0.1:8214","wire_api":"anthropic"},
		"openai":{"base_url":%q,"wire_api":"openai-chat-completions"}},
		"models":{"o":%q},"model_options":{"o":{"vendor":"anthropic"}}}}`, fips, opusID)
	r, idle := resolveRoute(entrypoint.NewEnv(map[string]string{
		"YOLO_PROVIDERS": providers, "YOLO_PROFILES": `{"bp":{"provider":"b"}}`, "YOLO_USE_PROFILES": `{"claude":"bp"}`,
	}))
	if idle != "" || r.SignRegion != "us-east-1" || !r.AnthropicModels[opusID] {
		t.Fatalf("the Bedrock-platform route at a FIPS host: %+v (idle %q), want it signed and carrying opus", r, idle)
	}
	m := newMessagesPassthrough(r.UpstreamBaseURL, r.AnthropicModels, &bedrockSigner{chain: &sigv4.Chain{}})
	if m == nil || m.url != "https://bedrock-runtime-fips.us-east-1.amazonaws.com/anthropic/v1/messages" {
		t.Errorf("the pass-through on a FIPS host = %+v, want its Messages route on that host", m)
	}
}

// TestTheMessagesRouteKeepsAProxysPathPrefix: a provider whose platform says Bedrock is signed at
// any https address (WG-I37), a corporate proxy among the ruling's cases, and a proxy that mirrors
// runtime under a path prefix serves Messages under the same prefix it serves /openai/v1 under.
// So the Messages route is composed where the base's own /openai/v1 was, and only a base that
// does not end in runtime's OpenAI path falls back to the host's root.
func TestTheMessagesRouteKeepsAProxysPathPrefix(t *testing.T) {
	signer := &bedrockSigner{chain: &sigv4.Chain{}}
	for base, want := range map[string]string{
		bedrockBase: "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic/v1/messages",
		"https://gw.corp.example/aws/bedrock/openai/v1":  "https://gw.corp.example/aws/bedrock/anthropic/v1/messages",
		"https://gw.corp.example/aws/bedrock/openai/v1/": "https://gw.corp.example/aws/bedrock/anthropic/v1/messages",
		"https://gw.corp.example/v1":                     "https://gw.corp.example/anthropic/v1/messages",
	} {
		m := newMessagesPassthrough(base, map[string]bool{opusID: true}, signer)
		if m == nil || m.url != want {
			t.Errorf("the Messages route for %s = %+v, want %s", base, m, want)
		}
	}
}
