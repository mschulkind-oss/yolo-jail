package run

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE REAL HOST HALF THROUGH THE MACOS-USER ARM (docs/design/host-notch-services.md HS-D14). The
// sibling tests (macosuserservices_test.go) stub startMacosUserService; this one leaves it alone,
// so the arm self-execs `yolo internal daemon wire-bridge`, which is this test binary through
// TestMain's internaldaemon dispatch. Only the sandbox is faked: MacosUserRun is where the
// sandboxed command would run, and it speaks claude's request shape to the address and token the
// arm handed it. The upstream is an httptest server the user's `providers` override points
// cerebras at.
//
// What it adds over the stubbed tests is the daemon's side of the arm's hand-off: that the host
// half the arm starts listens on the port the plan picked, refuses a caller without the token,
// translates for one with it, dials the provider with the provider's key and never the caller's,
// and is gone once Run returns. Making the startMacosUserServices call in run.go a no-op fails it
// (the upstream sees nothing). The Mac test,
// integration/macosuserbridge_test.go's TestMacosUserRunsTheWireBridgeHostHalfForABridgedProfile, is
// the same chain with a real sandbox in place of MacosUserRun.
func TestTheMacosUserArmServesClaudeOnCerebrasThroughARealHostHalf(t *testing.T) {
	var mu sync.Mutex
	var auths, paths []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths, paths = append(auths, r.Header.Get("Authorization")), append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant",`+
			`"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(up.Close)

	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "csk-test"}], `+
		`"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "`+up.URL+`/v1"}}}}}`, shellWith(nil))
	o.ProfileName = "cerebras"
	var base, token string
	var withToken, without int
	var body string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		b, _ := env.Get("ANTHROPIC_BASE_URL")
		k, _ := env.Get("ANTHROPIC_AUTH_TOKEN")
		base, _ = b.(string)
		token, _ = k.(string)
		post := func(auth string) (int, string) {
			req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages",
				strings.NewReader(`{"model":"qwen-3.8-27b","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			if auth != "" {
				req.Header.Set("Authorization", "Bearer "+auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return -1, err.Error()
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(raw)
		}
		withToken, body = post(token)
		without, _ = post("")
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if !strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasSuffix(base, ":8214") {
		t.Fatalf("the command's ANTHROPIC_BASE_URL = %q, want a picked loopback port\n%s", base, stderr.String())
	}
	if without != http.StatusUnauthorized {
		t.Errorf("a request without the caller token got %d, want 401", without)
	}
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if withToken != http.StatusOK || json.Unmarshal([]byte(body), &msg) != nil || msg.Type != "message" ||
		len(msg.Content) == 0 || msg.Content[0].Text != "ok" {
		t.Errorf("a request with the caller token got %d %s, want 200 in the anthropic shape\n%s",
			withToken, body, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/v1/chat/completions" || auths[0] != "Bearer csk-test" {
		t.Errorf("upstream saw %v with %v, want one chat-completions call bearing the provider key", paths, auths)
	}
	if token == "" || strings.Contains(strings.Join(auths, " "), token) {
		t.Errorf("the caller token must stop at the bridge; upstream saw %v", auths)
	}
	host := strings.TrimPrefix(base, "http://")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
		if err != nil {
			break
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Errorf("the bridge still answers at %s after the command returned", host)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}
