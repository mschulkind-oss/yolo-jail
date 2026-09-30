package integration

// wirebridgecodex_test.go is the end-to-end tier for the wire bridge's CODEX route
// (docs/reference/wire-bridge.md): claude on its `codex` profile, whose Anthropic Messages
// requests the bridge translates to OpenAI Responses and sends to the ChatGPT subscription with
// an access view the OpenAI credential service hands it per request. Until this test only the
// cerebras route had one (wirebridge_test.go), so every hop below was pinned by unit tests of
// its own half and none of them together:
//
//	a claude-shaped curl to $ANTHROPIC_BASE_URL/v1/messages (in the jail)
//	  → wire-bridge's openai-responses route at its served address (in the jail): the declared
//	    127.0.0.1:8215 on a bridged jail, a picked port on a nested one
//	  → the OpenAI credential service's loopback-TLS front (host) for an access view
//	  → a Responses request at the stub upstream (host), bearing that view
//	  → an Anthropic-shaped answer back.
//
// NO agent binary runs and NO external API is called. The upstream is a stub on the HOST's
// loopback, reached because the user `providers` override re-points openai-codex's
// `openai-responses` endpoint at it (the route reads that endpoint, ES-D29), and a user
// provider URL at loopback becomes an implicit forward (wirebridge_test.go's header says why
// the stub lives on the host). The access view is a FORGED LOGIN (openaiauth_test.go's term: a
// Codex-shaped auth.json of random test strings whose access token is a JWT a day from expiry),
// imported into a private credential state, so the broker serves it from cache and never calls
// OpenAI. The singleton rules are openaiauth_test.go's, through its own helpers: the machine's
// real grant is never touched, and the test owns the singleton only while it runs.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// codexUpstreamRecord is what the host-side stub saw of the one request the bridge made.
type codexUpstreamRecord struct {
	seen                          bool
	path, authorization, account  string
	model, store, maxOutputTokens string
	inputText                     string
}

func TestWireBridgeTranslatesClaudeCodexToResponses(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" || goruntime.GOOS != "linux" {
		t.Skipf("this test proves credential-service singleton ownership from /proc and runs on "+
			"podman on Linux, as TestOpenAIAuthBrokerRoundTripsAnImportedToken does; this is %q on %s",
			rt, goruntime.GOOS)
	}
	// A nested jail shares the launching jail's loopback (podman-in-podman forces --net=host),
	// where a bridge may already hold the Codex adapter's declared 8215. This test used to skip
	// there. Since served addresses (docs/plans/notch-convergence.md NC-D41) a shared-namespace
	// launch serves the route on a port it picked and composes claude's ANTHROPIC_BASE_URL from
	// it, so the nested run reaches its own bridge, asserted below (servedURLProblem).

	// The host-side upstream, bound before the launch so the implicit forward has something
	// behind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding the host-side upstream stub: %v", err)
	}
	stubPort := ln.Addr().(*net.TCPAddr).Port
	var mu sync.Mutex
	var record codexUpstreamRecord
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		record = codexUpstreamRecord{seen: true, path: req.URL.Path,
			authorization: req.Header.Get("Authorization"), account: req.Header.Get("ChatGPT-Account-Id"),
			model: string(body["model"]), store: string(body["store"]),
			maxOutputTokens: string(body["max_output_tokens"]), inputText: string(body["input"])}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_wirebridge_stub","model":"gpt-6-sol","status":"completed",`+
			`"output":[{"type":"message","content":[{"type":"output_text","text":"codex bridge says hello"}]}],`+
			`"usage":{"input_tokens":4,"output_tokens":5}}`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dir := writeProject(t, `{}`)
	packHome(t, `{
		"packs": ["claude"],
		"profile": {"claude": "codex"},
		"providers": {"openai-codex": {"endpoints": {"openai-responses": {"base_url": "http://127.0.0.1:`+
		strconv.Itoa(stubPort)+`/codex"}}}}
	}`)
	macArchivePrivateState(t)
	statePath := filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName)
	if !strings.HasPrefix(statePath, paths.GlobalStorage()+string(filepath.Separator)) {
		t.Fatalf("broker state %s is not under the private state dir %s", statePath, paths.GlobalStorage())
	}
	skipIfMachineHoldsAGrant(t, statePath)
	clearGrantlessSingleton(t)
	if r := runYoloCLI(t, dir, "openai-auth", "status"); r.rc != 0 {
		t.Fatalf("`yolo openai-auth status` failed: rc %d\n%s", r.rc, r.combined())
	}
	pid := requireOwnSingleton(t, statePath)
	t.Cleanup(func() {
		if err := stopSingletonPID(pid); err != nil {
			t.Errorf("stopping this test's openai-auth-broker singleton: %v", err)
		}
	})
	forged := forgeCodexLogin(t)
	if r := runYoloCLI(t, dir, "openai-auth", "import", "--from", forged.path); r.rc != 0 {
		t.Fatalf("`yolo openai-auth import` failed: rc %d\n%s", r.rc, r.combined())
	}
	if got := requireOwnSingleton(t, statePath); got != pid {
		t.Fatalf("the singleton changed from pid %d to %d during the import", pid, got)
	}

	// $ANTHROPIC_BASE_URL is claude's alone, in its own env file; source it as claude's
	// launcher does. Wait for the implicit forward rather than assuming it.
	script := `set -u
. ~/.config/yolo-agent-env/claude.sh
echo "BASE_URL=$ANTHROPIC_BASE_URL"
[ "${ANTHROPIC_AUTH_TOKEN:-}" = "$YOLO_SERVICE_WIRE_BRIDGE_TOKEN" ] && echo "TOKEN=matches-channel"
for i in $(seq 1 50); do (exec 3<>/dev/tcp/127.0.0.1/` + strconv.Itoa(stubPort) + `) 2>/dev/null && break; sleep 0.1; done
code=$(curl -sS --max-time 60 -o /workspace/wirebridge-codex-resp.json -w '%{http_code}' \
  "$ANTHROPIC_BASE_URL/v1/messages" \
  -H 'content-type: application/json' \
  -H "Authorization: Bearer $ANTHROPIC_AUTH_TOKEN" \
  -d '{"model":"gpt-6-sol","max_tokens":32,"messages":[{"role":"user","content":"say codex bridge"}]}')
echo "MSGS=$code"
true`
	r := runYolo(t, dir, script, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if r.rc != 0 {
		t.Fatalf("claude-on-codex launch failed: rc %d\n%s", r.rc, r.combined())
	}
	// WB-D18: claude sends the launch's caller token, as the bearer claude itself sends, and
	// the bridge refuses a request without it; the probe sends exactly what claude would.
	if got := kvLine(r.stdout, "TOKEN"); got != "matches-channel" {
		t.Errorf("claude's ANTHROPIC_AUTH_TOKEN is not the launch's caller token (TOKEN=%q)", got)
	}
	if p := servedURLProblem(kvLine(r.stdout, "BASE_URL"), "127.0.0.1:8215", "", inContainer()); p != "" {
		t.Errorf("claude's ANTHROPIC_BASE_URL is %s, want the Codex adapter's served address", p)
	}
	respRaw, _ := os.ReadFile(filepath.Join(dir, "wirebridge-codex-resp.json"))
	if got := kvLine(r.stdout, "MSGS"); got != "200" {
		t.Fatalf("the bridged messages request answered HTTP %s, want 200\nresponse: %s\n%s",
			got, respRaw, r.combined())
	}

	mu.Lock()
	up := record
	mu.Unlock()
	if !up.seen {
		t.Fatalf("the host-side upstream stub was never called: the Codex route did not dial the " +
			"openai-responses endpoint the user provider names")
	}
	if up.path != "/codex/responses" {
		t.Errorf("upstream path = %q, want the declared base plus /responses", up.path)
	}
	if up.authorization != "Bearer "+forged.access {
		t.Errorf("upstream Authorization carries %s, want the imported access view %s — the route "+
			"did not attach the credential service's token",
			openaiauth.TokenFingerprint(strings.TrimPrefix(up.authorization, "Bearer ")),
			openaiauth.TokenFingerprint(forged.access))
	}
	if strings.Contains(up.authorization, forged.refresh) {
		t.Errorf("the canonical refresh token reached the upstream")
	}
	if up.account != forged.account {
		t.Errorf("upstream ChatGPT-Account-Id = %q, want the imported account %q", up.account, forged.account)
	}
	if up.model != `"gpt-6-sol"` || up.store != "false" || up.maxOutputTokens != "" {
		t.Errorf("upstream body model=%s store=%s max_output_tokens=%q, want the passthrough model, "+
			"store false and no token cap (the subscription endpoint rejects one)",
			up.model, up.store, up.maxOutputTokens)
	}
	if !strings.Contains(up.inputText, "say codex bridge") {
		t.Errorf("upstream input = %s, want the user turn translated", up.inputText)
	}

	var resp struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("the response is not JSON: %v\n%s", err, respRaw)
	}
	if resp.Type != "message" || resp.Role != "assistant" || len(resp.Content) == 0 ||
		resp.Content[0].Text != "codex bridge says hello" {
		t.Errorf("response = %s, want the stub's text in the anthropic message shape", respRaw)
	}
	if strings.Contains(r.combined(), forged.access) || strings.Contains(r.combined(), forged.refresh) {
		t.Errorf("a forged token appeared verbatim in the launch output")
	}
	stepSummary(t, fmt.Sprintf("### Wire bridge Codex route: HTTP %s, upstream path %q",
		kvLine(r.stdout, "MSGS"), up.path))
}
