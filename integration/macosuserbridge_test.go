package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// wireBridgeHostHalfArgv is what the wire bridge's host half shows in the host's process listing:
// packs/wire-bridge's `host_daemon.cmd`, less the `yolo` its argv starts with.
const wireBridgeHostHalfArgv = "internal daemon wire-bridge"

// TestMacosUserRunsTheWireBridgeHostHalfForABridgedProfile is HS-D14
// (docs/design/host-notch-services.md §4.7) on the hardware. With `claude` profiled at `cerebras`,
// the macos-user guest declines the bridge's jail daemon, and the launch starts the bridge's HOST
// HALF itself: outside Seatbelt, as the host user, on a loopback port this launch picked, answering
// only this launch's caller token, and stopped when the command exits.
//
// WHAT ONLY THIS TEST CAN SEE, none of it run on a Mac before: that the address
// ANTHROPIC_BASE_URL names in claude's env file answers from inside the sandbox; that a request
// with no token is refused 401 and one bearing claude's ANTHROPIC_AUTH_TOKEN comes back 200 in the
// anthropic shape; that the bridge, running on the host, dials the provider at the address the
// user's `providers` override names, bearing the provider key and never the caller token; that it
// runs as the host user; and that it is gone once the session ends. The unit tier runs the same
// chain with the sandbox faked: TestTheMacosUserArmStartsTheServiceAndStopsItAfterTheCommand with
// the start stubbed, and TestTheMacosUserArmServesClaudeOnCerebrasThroughARealHostHalf through a
// real host half.
//
// It runs no agent and calls no API: the probe sources claude's env file, as claude's launcher
// does, and speaks the anthropic Messages shape with curl; the upstream is a stub this test serves
// on the Mac's loopback, which the host half shares.
func TestMacosUserRunsTheWireBridgeHostHalfForABridgedProfile(t *testing.T) {
	requireMacosUser(t)
	const sentinel = "macosuser-wirebridge-sentinel"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding the upstream stub: %v", err)
	}
	var mu sync.Mutex
	var record struct {
		seen          bool
		Path          string
		Authorization string
		Body          upstreamBody
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body upstreamBody
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		record.seen, record.Path, record.Body = true, req.URL.Path, body
		record.Authorization = req.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-macosuser-bridge", "model": body.Model,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]string{"role": "assistant", "content": "bridge says hello"}}},
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
		})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	stubAddr := fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	packHome(t, `{
		"packs": ["claude", "cerebras"],
		"profile": {"claude": "cerebras"},
		"env_sources": [{"CEREBRAS_API_KEY": "`+sentinel+`"}],
		"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "http://`+stubAddr+`/v1"}}}}
	}`)
	ws := macosUserWorkspace(t, `{}`)
	uid := sandboxUID(t)
	me := strconv.Itoa(os.Getuid())

	watch := watchForDoorway(wireBridgeHostHalfArgv)
	t.Cleanup(func() { watch.stop() })
	curl := func(auth string) string {
		return `curl -sS -o /dev/null -w '%{http_code}' -H 'content-type: application/json' ` + auth +
			` -d "$body" "$ANTHROPIC_BASE_URL/v1/messages"`
	}
	r := macosUserRunProbe(t, "wire-bridge", ws, strings.Join([]string{
		`body='{"model":"qwen-3.8-27b","max_tokens":32,"messages":[{"role":"user","content":"say bridge"}]}'`,
		`echo "=== BRIDGE ==="`,
		`if [ -r ~/.config/yolo-agent-env/claude.sh ]; then echo ENVFILE=present; else echo ENVFILE=missing; ls -la ~/.config/ ~/.config/yolo-agent-env/ 2>&1; fi`,
		`. ~/.config/yolo-agent-env/claude.sh`,
		`echo "URL=${ANTHROPIC_BASE_URL-UNSET}"`,
		`echo "NOTOKEN=$(` + curl("") + `)"`,
		`echo "TOKEN=$(` + curl(`-H "authorization: Bearer ${ANTHROPIC_AUTH_TOKEN:-}"`) + `)"`,
		`echo "RESP=$(curl -sS -H 'content-type: application/json' ` +
			`-H "authorization: Bearer ${ANTHROPIC_AUTH_TOKEN:-}" -d "$body" "$ANTHROPIC_BASE_URL/v1/messages" | tr -d '\n')"`,
		`echo "CALLER_TOKEN=${ANTHROPIC_AUTH_TOKEN:-}"`,
		`echo "=== END ==="`,
	}, "\n"))
	saw := watch.stop()
	out := section(r.stdout, "=== BRIDGE ===", "=== END ===")
	diag := "\n--- probe:\n" + out + "\n--- launch stderr:\n" + r.stderr
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}

	if got["ENVFILE"] != "present" {
		t.Fatalf("the sandbox cannot read claude's env file at ~/.config/yolo-agent-env/claude.sh, "+
			"where the macos-user arm writes it (writeMacosUserAgentEnvFiles) and claude's launcher "+
			"sources it, so nothing below can be read%s", diag)
	}
	for _, want := range []string{
		`Started the "wire-bridge" service (pack "wire-bridge"`,
		"wire-bridge: yolo-jaild wire-bridge — ",
		"(its host half runs for this launch)",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the launch did not say %q (startMacosUserServices, noteMacosUserJailDaemonDeclines)%s", want, diag)
		}
	}
	if !strings.HasPrefix(got["URL"], "http://127.0.0.1:") || got["URL"] == "http://127.0.0.1:8214" {
		t.Errorf("claude's ANTHROPIC_BASE_URL is not a port picked for this launch%s", diag)
	}
	if got["NOTOKEN"] != "401" {
		t.Errorf("a request from inside the sandbox without the caller token got %q, want 401 "+
			"(000 means nothing answered at the address)%s", got["NOTOKEN"], diag)
	}
	if got["TOKEN"] != "200" {
		t.Errorf("a request bearing claude's caller token got %q, want 200%s", got["TOKEN"], diag)
	}
	var resp struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(got["RESP"]), &resp); err != nil || resp.Type != "message" ||
		resp.Role != "assistant" || len(resp.Content) == 0 || resp.Content[0].Text != "bridge says hello" {
		t.Errorf("the bridge's answer is not the anthropic shape carrying the stub's text (%v)%s", err, diag)
	}

	mu.Lock()
	upstream := record
	mu.Unlock()
	if !upstream.seen {
		t.Fatalf("the upstream stub was never called: the host half did not reach the address the "+
			"user's provider override names%s", diag)
	}
	if upstream.Path != "/v1/chat/completions" || upstream.Authorization != "Bearer "+sentinel {
		t.Errorf("upstream got %s with Authorization %q, want /v1/chat/completions bearing the provider key",
			upstream.Path, upstream.Authorization)
	}
	if tok := got["CALLER_TOKEN"]; tok == "" || strings.Contains(upstream.Authorization, tok) {
		t.Errorf("the caller token must stop at the bridge; upstream Authorization = %q", upstream.Authorization)
	}
	if upstream.Body.Model != "qwen-3.8-27b" || len(upstream.Body.Messages) != 1 ||
		upstream.Body.Messages[0].Content != "say bridge" {
		t.Errorf("upstream body = %+v, want the one user turn translated with the model passed through", upstream.Body)
	}
	requireDoorwayRanOutsideAndStopped(t, wireBridgeHostHalfArgv, saw, me, uid, diag)
}
