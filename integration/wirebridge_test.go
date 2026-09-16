package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// upstreamBody is the openai-shaped request the bridge is expected to make: the
// fields this test asserts on, and no more.
type upstreamBody struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// TestWireBridgeTranslatesAnthropicToOpenai is the end-to-end tier for the wire
// bridge (docs/reference/wire-bridge.md): one real launch — user config selecting
// claude and cerebras (the bridge NOT listed; the needs closure must join it,
// and the banner line says so) with claude profiled at cerebras — proves the
// whole chain in a single jail:
//
//	a claude-shaped curl to $ANTHROPIC_BASE_URL/v1/messages   → an openai-shaped
//	  request at the stub upstream (bearer sentinel attached) and an
//	  anthropic-shaped answer back;
//	count_tokens                                              → 404 (WB-D14);
//	YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT                          → registered, so the
//	  reachability witness covered the listener before this command ever ran.
//
// NO agent binary runs and NO external API is called: the upstream is a stub this
// test serves on the HOST's loopback, and the bridge dials it because the user
// `providers` override re-points cerebras's openai base_url at it
// (`endpoints.<protocol>.base_url` is the override spelling when a pack ships an
// endpoints table).
//
// ⚠ THE STUB IS ON THE HOST BECAUSE THAT IS WHAT THE URL MEANS, and it did not use
// to be. `bf6a6e52` ruled that a USER provider URL at loopback names an inference
// server on the machine that launches yolo — a local Ollama, say — not something
// inside the jail's own network namespace, and it implements that by adding an
// implicit `forward_host_ports` entry for the port. Packs are excluded from that
// rule on purpose (a pack such as wire-bridge legitimately names jail loopback),
// but this fixture overrides through the USER key, so the rule applies to it. The
// stub therefore has to live where the URL now points; a stub started on the jail's
// own loopback loses the port to the forwarder, and the bridge dials past it to a
// host port with nothing behind it (measured: a 502 and no upstream record).
// A consequence worth knowing: this test now also covers the forward.
//
// The port is claimed from the kernel rather than written down, so a busy 18099 on
// a runner cannot make this test flake. The response assertion reads the file the
// in-jail curl wrote into the live-mounted workspace; the upstream assertion reads
// what the host-side handler recorded in memory.
func TestWireBridgeTranslatesAnthropicToOpenai(t *testing.T) {
	requireJail(t)

	const sentinel = "wirebridge-integration-sentinel"

	// The host-side upstream: bound before the launch so the implicit forward has
	// something behind it, and recording the one request the bridge makes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding the host-side upstream stub: %v", err)
	}
	stubPort := ln.Addr().(*net.TCPAddr).Port

	var mu sync.Mutex
	var record struct {
		seen          bool
		Path          string
		Authorization string
		ContentType   string
		Body          upstreamBody
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body upstreamBody
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		record.seen, record.Path, record.Body = true, req.URL.Path, body
		record.Authorization = req.Header.Get("Authorization")
		record.ContentType = req.Header.Get("Content-Type")
		mu.Unlock()

		resp := map[string]any{
			"id":    "chatcmpl-wirebridge-stub",
			"model": body.Model,
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]string{"role": "assistant", "content": "bridge says hello"},
			}},
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	stubAddr := fmt.Sprintf("127.0.0.1:%d", stubPort)

	dir := writeProject(t, `{}`)
	// `packs` is user-scope only, and so are use_profiles/providers/env_sources.
	// The openai base_url override is per-field over the pack's shipped facts —
	// the anthropic endpoint and the wire_api stay the pack's.
	packHome(t, `{
		"packs": ["claude", "cerebras"],
		"use_profiles": {"claude": "cerebras"},
		"env_sources": [{"CEREBRAS_API_KEY": "`+sentinel+`"}],
		"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "http://`+stubAddr+`/v1"}}}}
	}`)

	script := `set -u
# The implicit forward for the provider's port is set up before this command runs;
# wait for it rather than assuming, so a slow forwarder cannot read as a bridge fault.
for i in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/` + strconv.Itoa(stubPort) + `) 2>/dev/null && break; sleep 0.1; done
msg=$(curl -sS -o /workspace/wirebridge-resp.json -w '%{http_code}' \
  "$ANTHROPIC_BASE_URL/v1/messages" \
  -H 'content-type: application/json' \
  -H 'x-api-key: ignored-inbound-auth' \
  -d '{"model":"qwen-3.8-27b","max_tokens":32,"messages":[{"role":"user","content":"say bridge"}]}')
count=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$ANTHROPIC_BASE_URL/v1/messages/count_tokens" \
  -H 'content-type: application/json' \
  -d '{"model":"qwen-3.8-27b","messages":[]}')
echo "MSGS=$msg COUNT=$count"
env | grep -E '^YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT='
true`
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("bridged launch failed: rc %d\n%s", r.rc, r.combined())
	}

	// WB-D12: the closure joined the pack the user never listed, and said so.
	//
	// ⚠ THE CAUSING PACK IS DELIBERATELY NOT PINNED, and it used to be. This asserted the
	// exact string "+ wire-bridge (needed by cerebras: claude selected)" and went red when
	// `e0d62605` (the OMP / Codex-Claude bridge) gave packs/claude/pack.json its own
	// unconditional `needs: wire-bridge`. Both edges are now real — claude's unconditional
	// one and cerebras's `when_bins: [claude, copilot]` — so which one the resolver reports
	// is a fact about pack topology, not about this behaviour, and it moved once legitimately
	// already.
	//
	// WB-D12's claim is that the closure JOINED a pack the user never listed and SAID WHY.
	// That is what is checked: the line names wire-bridge and carries a cause. Pinning the
	// cause's author made a correct launch fail a stale string, which is the drift-prone form
	// this repo fixes by removing rather than by updating.
	if got := r.stderr; !strings.Contains(got, "+ wire-bridge (needed by ") {
		t.Errorf("the launch stderr must carry the needs cause line for wire-bridge — it "+
			"joined a closure the user never listed, so it has to say why:\n%s", got)
	}

	// The witness registration crossed, so the endpoint file the listener
	// published was probed (and the boot would have refused without it).
	if !strings.Contains(r.stdout, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT=/run/yolo-services/wire-bridge.endpoint") {
		t.Errorf("the jail env must carry the witness registration:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "MSGS=200 COUNT=404") {
		t.Errorf("the messages round-trip must be 200 and count_tokens must refuse 404:\n%s", r.stdout)
	}

	// What the UPSTREAM received: an openai-shaped request, the provider's key
	// as its bearer, the model id passthrough.
	mu.Lock()
	upstream := record
	mu.Unlock()
	if !upstream.seen {
		t.Fatalf("the host-side upstream stub was never called, so the bridge did not reach " +
			"the address the user provider names")
	}
	if upstream.Path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want the bridge's one chat-completions dial", upstream.Path)
	}
	if upstream.Authorization != "Bearer "+sentinel {
		t.Errorf("upstream Authorization = %q, want the sentinel borne as a bearer "+
			"from the 0600 key file", upstream.Authorization)
	}
	if upstream.Body.Model != "qwen-3.8-27b" || upstream.Body.MaxTokens != 32 {
		t.Errorf("upstream body model/max_tokens = %q/%d, want the passthrough pair",
			upstream.Body.Model, upstream.Body.MaxTokens)
	}
	if len(upstream.Body.Messages) != 1 || upstream.Body.Messages[0].Role != "user" ||
		upstream.Body.Messages[0].Content != "say bridge" {
		t.Errorf("upstream messages = %+v, want the one user turn translated", upstream.Body.Messages)
	}

	// What CLAUDE got back: the anthropic shape, with the stub's text inside.
	respRaw, err := os.ReadFile(filepath.Join(dir, "wirebridge-resp.json"))
	if err != nil {
		t.Fatalf("reading the response file: %v", err)
	}
	var resp struct {
		Type       string `json:"type"`
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("the response is not JSON: %v\n%s", err, respRaw)
	}
	if resp.Type != "message" || resp.Role != "assistant" || resp.StopReason != "end_turn" {
		t.Errorf("response shape = type %q role %q stop_reason %q, want the anthropic "+
			"message shape:\n%s", resp.Type, resp.Role, resp.StopReason, respRaw)
	}
	if len(resp.Content) == 0 || resp.Content[0].Type != "text" || resp.Content[0].Text != "bridge says hello" {
		t.Errorf("response content = %+v, want the stub's text as a text block", resp.Content)
	}
}
