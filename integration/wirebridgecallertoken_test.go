package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// wirebridgecallertoken_test.go is the container tier of the wire bridge's caller token
// (docs/reference/wire-bridge.md#caller-authentication, WB-D18) across a jail's lifetime: an
// ATTACH delivers the token the running jail's bridge already demands, so a client started from
// the attached session is served; and the next fresh launch of the same workspace mints a new
// one. No agent runs and no external API is called: the upstream is a stub on the host's
// loopback, reached through the user provider override exactly as
// TestWireBridgeTranslatesAnthropicToOpenai reaches it.

var callerTokenLine = regexp.MustCompile(`(FIRST|ATTACH|NEXT)-TOKEN-([0-9a-f]{64})-END`)

func tokensIn(out, which string) []string {
	var got []string
	for _, m := range callerTokenLine.FindAllStringSubmatch(out, -1) {
		if m[1] == which {
			got = append(got, m[2])
		}
	}
	return got
}

func TestAnAttachKeepsTheBridgesCallerTokenAndALaunchRotatesIt(t *testing.T) {
	requireJail(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stubPort := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-stub", "model": "qwen-3.8-27b",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]string{"role": "assistant", "content": "ok"}}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dir := writeProject(t, `{}`)
	packHome(t, fmt.Sprintf(`{
		"packs": ["claude", "cerebras"],
		"profile": {"claude": "cerebras"},
		"env_sources": [{"CEREBRAS_API_KEY": "caller-token-integration-key"}],
		"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "http://127.0.0.1:%d/v1"}}}}
	}`, stubPort))

	const release = "release-caller-token"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-TOKEN-${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}-END; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644) })
	deadline := time.Now().Add(jailTimeout())
	for len(tokensIn(first.combined(), "FIRST")) == 0 {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never printed its caller token within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	firstTok := tokensIn(first.combined(), "FIRST")[0]
	awaitLaunchLockReleased(t, dir, first)

	// The attach: a new entry into the RUNNING jail, whose bridge read firstTok at boot.
	script := `set -u
. ~/.config/yolo-agent-env/claude.sh
for i in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/` + strconv.Itoa(stubPort) + `) 2>/dev/null && break; sleep 0.1; done
echo ATTACH-TOKEN-${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}-END
[ "$ANTHROPIC_AUTH_TOKEN" = "$YOLO_SERVICE_WIRE_BRIDGE_TOKEN" ] && echo CLAUDE-SENDS-THE-TOKEN
code=$(curl -sS -o /dev/null -w '%{http_code}' "$ANTHROPIC_BASE_URL/v1/messages" \
  -H 'content-type: application/json' -H "authorization: Bearer $ANTHROPIC_AUTH_TOKEN" \
  -d '{"model":"qwen-3.8-27b","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}')
echo "ATTACHED-CODE=$code"`
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("attach failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach, so this tested nothing:\n%s", r.combined())
	}
	if got := tokensIn(r.stdout, "ATTACH"); len(got) != 1 || got[0] != firstTok {
		t.Errorf("the attached session's caller token = %q, want the running jail's %q", got, firstTok)
	}
	for _, want := range []string{"CLAUDE-SENDS-THE-TOKEN", "ATTACHED-CODE=200"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the attached session: %q missing — the bridge the first launch started must "+
				"serve a client the attach configured:\n%s", want, r.stdout)
		}
	}

	// Release the first session; once its jail is gone, the next launch is a fresh one and
	// mints its own token.
	_ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644)
	select {
	case <-first.done:
	case <-time.After(jailTimeout()):
		t.Fatalf("first launch did not exit after release:\n%s", first.combined())
	}
	next := runYolo(t, dir, `echo NEXT-TOKEN-${YOLO_SERVICE_WIRE_BRIDGE_TOKEN}-END`)
	if next.rc != 0 {
		t.Fatalf("next launch failed: rc %d\n%s", next.rc, next.combined())
	}
	if strings.Contains(next.combined(), "Attaching to existing jail") {
		t.Fatalf("the next launch attached instead of starting fresh:\n%s", next.combined())
	}
	if got := tokensIn(next.stdout, "NEXT"); len(got) != 1 || got[0] == firstTok {
		t.Errorf("the next launch's caller token = %q; it must be a fresh one, not %q", got, firstTok)
	}
}
