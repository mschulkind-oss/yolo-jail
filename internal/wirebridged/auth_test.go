package wirebridged

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// testCallerToken is the caller token the serve-path tests hand the daemon (a well-formed one:
// 64 lowercase hex, the shape svcendpoint.NewToken mints).
var testCallerToken = strings.Repeat("ab", 32)

// tokenEnv is entrypoint.NewEnv with this launch's caller token set, as the per-entry channel
// hands it to a real daemon.
func tokenEnv(vars map[string]string) *entrypoint.Env {
	e := entrypoint.NewEnv(vars)
	e.Vars[CallerTokenEnv] = testCallerToken
	return e
}

// callerTokenTransport is a bridged client: it sends the launch's caller token as a bearer on
// every request that carries no credential of its own, the way every derive-configured agent
// does. A request that sets Authorization or x-api-key itself is sent as it is, so a test can
// present a wrong token or none.
type callerTokenTransport struct{}

func (callerTokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") == "" && r.Header.Get("X-Api-Key") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+testCallerToken)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// bridgeClient is the client the serve-path tests reach a served listener with.
var bridgeClient = &http.Client{Transport: callerTokenTransport{}}

// servedBridge starts the production serve path (servePlan) with an adapter route and a via
// route, each over the capturing stub upstream, and returns both addresses. env is the daemon's
// environment beyond JAIL_HOME; the caller decides whether it carries a token.
func servedBridge(t *testing.T, env map[string]string) (up *captureUpstream, adapterAddr, viaAddr string) {
	t.Helper()
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("CEREBRAS_API_KEY", "")
	up = withUpstream(t)
	home := t.TempDir()
	writeKeyChannel(t, home, "export ZAI_API_KEY=${ZAI_API_KEY:-'zai-key'}",
		"export CEREBRAS_API_KEY=${CEREBRAS_API_KEY:-'cb-key'}")
	adapterAddr, viaAddr = freeLoopback(t), freeLoopback(t)
	vp := viaRoutesFor(mustProviders(t, viaProviders), map[string]string{"pi": "pz"}, viaResolved("http://"+viaAddr))
	adapter := route{ProviderName: "cerebras", ListenAddr: adapterAddr,
		UpstreamBaseURL: "https://api.cerebras.ai/v1", KeyEnvName: "CEREBRAS_API_KEY"}
	noReadinessPipe(t)
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	vars := map[string]string{"JAIL_HOME": home}
	for k, v := range env {
		vars[k] = v
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- servePlan(ctx, plan{adapter: &adapter, via: vp}, entrypoint.NewEnv(vars)) }()
	t.Cleanup(func() { cancel(); <-done })
	if env[CallerTokenEnv] == testCallerToken {
		waitFor(t, func() bool {
			b, err := os.ReadFile(endpointFile)
			return err == nil && strings.TrimSpace(string(b)) != ""
		})
	}
	return up, adapterAddr, viaAddr
}

// sendAs posts body to url with exactly the given credential headers and no other: the bare
// client, so nothing adds the token behind the test's back.
func sendAs(t *testing.T, url, body string, header map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const anthropicHi = `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`

// TestTheBridgeRefusesEveryCallerWithoutThisLaunchsToken is WB-D18 on both listeners through the
// production serve path: no credential and a wrong one are refused 401 with a body naming the
// variable, in the shape that listener's clients read, and reach no upstream; the right token,
// as a bearer (claude, and the OpenAI clients) or as x-api-key (the Anthropic SDK form), is
// served. Deleting the requireAnthropicCaller or requireOpenAICaller wrap in servePlan fails it.
func TestTheBridgeRefusesEveryCallerWithoutThisLaunchsToken(t *testing.T) {
	up, adapterAddr, viaAddr := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	wrong := strings.Repeat("cd", 32)
	for _, l := range []struct {
		name, url, body, errType string
	}{
		{"adapter", "http://" + adapterAddr + "/v1/messages", anthropicHi, "authentication_error"},
		{"via", "http://" + viaAddr + "/agent/pi/chat/completions", `{"model":"m"}`, "invalid_request_error"},
	} {
		for _, c := range []struct {
			what   string
			header map[string]string
			want   int
			says   string
		}{
			{"no credential", nil, http.StatusUnauthorized, "carried no caller token"},
			{"a wrong bearer", map[string]string{"Authorization": "Bearer " + wrong}, http.StatusUnauthorized, "not this launch's"},
			{"a wrong x-api-key", map[string]string{"X-Api-Key": wrong}, http.StatusUnauthorized, "not this launch's"},
			{"a non-bearer scheme", map[string]string{"Authorization": "Basic " + testCallerToken}, http.StatusUnauthorized, "carried no caller token"},
			{"the token as a bearer", map[string]string{"Authorization": "Bearer " + testCallerToken}, http.StatusOK, ""},
			{"the token as x-api-key", map[string]string{"X-Api-Key": testCallerToken}, http.StatusOK, ""},
		} {
			before := up.calls()
			status, body := sendAs(t, l.url, l.body, c.header)
			if status != c.want {
				t.Errorf("%s route, %s: status %d, want %d (%s)", l.name, c.what, status, c.want, body)
				continue
			}
			if c.want != http.StatusUnauthorized {
				if up.calls() != before+1 {
					t.Errorf("%s route, %s: served without reaching the upstream", l.name, c.what)
				}
				continue
			}
			if up.calls() != before {
				t.Errorf("%s route, %s: a refused request reached the upstream", l.name, c.what)
			}
			var doc struct {
				Error struct{ Type, Message string } `json:"error"`
			}
			if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Error.Type != l.errType ||
				!strings.Contains(doc.Error.Message, c.says) || !strings.Contains(doc.Error.Message, CallerTokenEnv) {
				t.Errorf("%s route, %s: 401 body %s, want a %s naming %q and $%s",
					l.name, c.what, body, l.errType, c.says, CallerTokenEnv)
			}
			if strings.Contains(body, wrong) || strings.Contains(body, testCallerToken) {
				t.Errorf("%s route, %s: the refusal echoes a token: %s", l.name, c.what, body)
			}
		}
	}
}

// TestTheCallerTokenIsNeverForwardedUpstream: what the agent authenticates with stops at the
// bridge. Every upstream request carries the provider's own credential, and no header of any
// upstream request carries the caller token, on either listener, whichever header it came in.
func TestTheCallerTokenIsNeverForwardedUpstream(t *testing.T) {
	up, adapterAddr, viaAddr := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	for _, h := range []map[string]string{
		{"Authorization": "Bearer " + testCallerToken},
		{"X-Api-Key": testCallerToken},
		{"Authorization": "Bearer " + testCallerToken, "X-Api-Key": testCallerToken},
	} {
		if s, b := sendAs(t, "http://"+adapterAddr+"/v1/messages", anthropicHi, h); s != 200 {
			t.Fatalf("adapter: %d %s", s, b)
		}
		if s, b := sendAs(t, "http://"+viaAddr+"/agent/pi/chat/completions", `{"model":"m"}`, h); s != 200 {
			t.Fatalf("via: %d %s", s, b)
		}
	}
	if up.calls() != 6 {
		t.Fatalf("upstream calls = %d, want 6", up.calls())
	}
	for i, r := range up.requests {
		for k, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, testCallerToken) {
					t.Errorf("upstream request %d (%s) carries the caller token in %s", i, r.URL.Host, k)
				}
			}
		}
		want := "Bearer cb-key"
		if r.URL.Host == "api.z.ai" {
			want = "Bearer zai-key"
		}
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("upstream request %d (%s) Authorization = %q, want the provider's %q", i, r.URL.Host, got, want)
		}
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("upstream request %d (%s) carries an x-api-key %q", i, r.URL.Host, got)
		}
	}
}

// TestABridgeHandedNoCallerTokenServesNothing: a daemon whose launch handed it no token (or a
// malformed one) binds nothing and publishes nothing — it never serves unauthenticated — and
// says why in its log.
func TestABridgeHandedNoCallerTokenServesNothing(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset":     {},
		"malformed": {CallerTokenEnv: "local"},
	} {
		t.Run(name, func(t *testing.T) {
			logged := captureDiag(t)
			_, adapterAddr, viaAddr := servedBridge(t, env)
			waitFor(t, func() bool { return strings.Contains(logged(), "idling:") })
			for _, addr := range []string{adapterAddr, viaAddr} {
				if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
					_ = conn.Close()
					t.Errorf("%s accepts connections with no caller token", addr)
				}
			}
			if _, err := os.Stat(EndpointFile); !os.IsNotExist(err) {
				t.Errorf("the endpoint was published with no caller token: %v", err)
			}
			if !strings.Contains(logged(), CallerTokenEnv) {
				t.Errorf("the idle line does not name $%s:\n%s", CallerTokenEnv, logged())
			}
		})
	}
}

// TestServingSaysEveryListenerRequiresTheCallerToken: the daemon's log (the jail's record of what
// it serves) states the requirement once it serves, naming the variable and never its value.
func TestServingSaysEveryListenerRequiresTheCallerToken(t *testing.T) {
	logged := captureDiag(t)
	servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	waitFor(t, func() bool { return strings.Contains(logged(), "requires this launch's caller token") })
	if strings.Contains(logged(), testCallerToken) {
		t.Fatalf("THE CALLER TOKEN REACHED THE LOG:\n%s", logged())
	}
}
