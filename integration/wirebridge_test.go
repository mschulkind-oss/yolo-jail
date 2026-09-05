package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireBridgeTranslatesAnthropicToOpenai is the end-to-end tier for the wire
// bridge (docs/design/wire-bridge.md): one real launch — user config selecting
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
// NO agent binary runs and NO external API is called: the upstream is a python3
// stub the command itself starts on the jail's loopback, and the bridge dials
// it because the user `providers` override re-points cerebras's openai base_url
// at it (`endpoints.<protocol>.base_url` is the override spelling when a pack
// ships an endpoints table). Assertions read files the command wrote into the
// live-mounted workspace — the same host-side reading renderedSurface uses.
func TestWireBridgeTranslatesAnthropicToOpenai(t *testing.T) {
	requireJail(t)

	const (
		sentinel = "wirebridge-integration-sentinel"
		stubAddr = "127.0.0.1:18099"
	)

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

	const stubPy = `import json, http.server
class Stub(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        with open("/workspace/wirebridge-upstream.json", "w") as f:
            json.dump({"path": self.path,
                       "authorization": self.headers.get("Authorization", ""),
                       "content_type": self.headers.get("Content-Type", ""),
                       "body": json.loads(body)}, f)
        resp = {"id": "chatcmpl-wirebridge-stub", "model": json.loads(body).get("model", ""),
                "choices": [{"index": 0, "finish_reason": "stop",
                             "message": {"role": "assistant", "content": "bridge says hello"}}],
                "usage": {"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8}}
        data = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def log_message(self, *a):
        pass
http.server.HTTPServer(("127.0.0.1", 18099), Stub).serve_forever()`

	script := `set -u
cat > /tmp/wirebridge-stub.py <<'STUBPY'
` + stubPy + `
STUBPY
python3 /tmp/wirebridge-stub.py &
for i in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/18099) 2>/dev/null && break; sleep 0.1; done
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
kill %1 2>/dev/null
wait 2>/dev/null
true`

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("bridged launch failed: rc %d\n%s", r.rc, r.combined())
	}

	// WB-D12: the closure joined the pack the user never listed, and said so.
	if got := r.stderr; !strings.Contains(got, "+ wire-bridge (needed by cerebras: claude selected)") {
		t.Errorf("the launch stderr must carry the needs cause line:\n%s", got)
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
	upRaw, err := os.ReadFile(filepath.Join(dir, "wirebridge-upstream.json"))
	if err != nil {
		t.Fatalf("reading the stub's record of the upstream request: %v", err)
	}
	var upstream struct {
		Path          string `json:"path"`
		Authorization string `json:"authorization"`
		ContentType   string `json:"content_type"`
		Body          struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"body"`
	}
	if err := json.Unmarshal(upRaw, &upstream); err != nil {
		t.Fatalf("parsing the stub's record: %v\n%s", err, upRaw)
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
