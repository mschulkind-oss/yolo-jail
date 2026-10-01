package openaiauthdaemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

func jwt(payload string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func TestDaemonPublishesPrivateDirectHostSocketAndStreamsErrors(t *testing.T) {
	dir := shortSocketDir(t)
	fronted := filepath.Join(dir, "fronted.sock")
	hostSocket := HostSocketPath(fronted)
	stop := make(chan struct{})
	var once sync.Once
	shutdown := func() { once.Do(func() { close(stop) }) }
	config := HandlerConfig{Broker: openaiauth.Broker{
		StatePath: filepath.Join(dir, "missing.json"),
		LockPath:  filepath.Join(dir, "refresh.lock"),
	}}
	done := make(chan error, 1)
	// BOTH handlers, as production builds them: this test dials the private socket, which is
	// the host one. hostactions_test.go is where the two doors are told apart.
	go func() {
		done <- serveSockets(BuildHandler(config), BuildHostHandler(config),
			fronted, hostSocket, stop, shutdown)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(hostSocket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("direct host socket was not published")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if info, err := os.Stat(hostSocket); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("host socket mode = %v, %v", info.Mode().Perm(), err)
	}
	if _, err := openauthclient.RequestUnix(hostSocket, map[string]any{"action": "ping"}, nil); err != nil {
		t.Fatal(err)
	}
	var diagnostic strings.Builder
	if _, err := openauthclient.RequestUnix(hostSocket, map[string]any{"action": "token"}, &diagnostic); err == nil {
		t.Fatal("token unexpectedly succeeded without canonical state")
	}
	if !strings.Contains(diagnostic.String(), "broker_error:") {
		t.Fatalf("diagnostic = %q, want actionable error code", diagnostic.String())
	}
	diagnostic.Reset()
	if _, err := openauthclient.RequestUnix(hostSocket, map[string]any{"action": "token", "view": "all-secrets"}, &diagnostic); err == nil {
		t.Fatal("unknown credential view unexpectedly succeeded")
	}
	if !strings.Contains(diagnostic.String(), "unknown OpenAI credential view") {
		t.Fatalf("unknown-view diagnostic = %q", diagnostic.String())
	}
	shutdown()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRefreshUsesOpenAIFormAndDecodesRotatingTokens(t *testing.T) {
	access := jwt(`{"exp":4102444800,"https://api.openai.com/auth":{"chatgpt_account_id":"acct-1"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"grant_type": "refresh_token", "refresh_token": "refresh-1", "client_id": ClientID,
		} {
			if got := r.Form.Get(key); got != want {
				t.Errorf("form[%s] = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"id_token":"id-2","refresh_token":"refresh-2","expires_in":3600}`, access)
	}))
	defer server.Close()

	got, err := (Upstream{TokenURL: server.URL}).Refresh(context.Background(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != access || got.IDToken != "id-2" || got.RefreshToken != "refresh-2" || got.AccountID != "acct-1" {
		t.Fatalf("tokens = %#v", got)
	}
	if remaining := time.Until(got.ExpiresAt); remaining < 59*time.Minute || remaining > 61*time.Minute {
		t.Fatalf("expiry remaining = %v, want about one hour", remaining)
	}
}

func TestRefreshClassifiesFailureWithoutEmbeddingResponseBody(t *testing.T) {
	const secret = "refresh-token-that-must-not-escape"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"code":"invalid_grant","message":%q}}`, secret)
	}))
	defer server.Close()

	_, err := (Upstream{TokenURL: server.URL}).Refresh(context.Background(), "refresh-1")
	var refreshErr *openaiauth.RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Kind != openaiauth.ErrorPermanent || refreshErr.Code != "invalid_grant" {
		t.Fatalf("error = %#v, want permanent invalid_grant", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed response body: %v", err)
	}
}

func TestBrowserLoginFallsBackValidatesStateAndExchangesPKCE(t *testing.T) {
	originalOpen := browserOpen
	var opened string
	browserOpen = func(target string) error { opened = target; return nil }
	t.Cleanup(func() { browserOpen = originalOpen })
	// The first port is one this test holds, the second one the kernel picks, so the test
	// reads no port of the machine's: it bound 1455 and 1457 there, failing wherever
	// something else held 1457 and skipping wherever something held 1455.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	taken := occupied.Addr().(*net.TCPAddr).Port
	prevPorts := loginPorts
	loginPorts = []int{taken, 0}
	t.Cleanup(func() { loginPorts = prevPorts })

	var posted url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		posted = r.Form
		fmt.Fprint(w, `{"access_token":"header.e30.signature","id_token":"id-1","refresh_token":"refresh-1","expires_in":3600}`)
	}))
	defer tokenServer.Close()
	flow, err := StartLogin("https://example.test/authorize", Upstream{TokenURL: tokenServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer flow.Close()
	if opened != flow.URL {
		t.Fatalf("browser opened %q, want %q", opened, flow.URL)
	}
	redirect, err := url.Parse(flow.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	// The kernel picks the fallback's port, so the callback is on neither the held port nor
	// one of the ports a real login tries: either means the list the test handed in went unread.
	bound := flow.listener.Addr().(*net.TCPAddr).Port
	if bound == taken || slices.Contains(prevPorts, bound) {
		t.Fatalf("the callback listens on %d; want the fallback the test handed in, not the held %d "+
			"or one of %v", bound, taken, prevPorts)
	}
	if redirect.Port() != strconv.Itoa(bound) {
		t.Fatalf("redirect = %s, want it to name the port the callback listens on, %d", flow.RedirectURI, bound)
	}
	authURL, _ := url.Parse(flow.URL)
	query := authURL.Query()
	if got := query.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want Codex's upstream-compatible value", got)
	}
	bad, err := http.Get(flow.RedirectURI + "?state=wrong&code=bad")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad-state status = %d", bad.StatusCode)
	}
	good, err := http.Get(flow.RedirectURI + "?state=" + url.QueryEscape(query.Get("state")) + "&code=code-1")
	if err != nil {
		t.Fatal(err)
	}
	good.Body.Close()
	if _, err := flow.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if posted.Get("code") != "code-1" || posted.Get("redirect_uri") != flow.RedirectURI {
		t.Fatalf("exchange form = %v", posted)
	}
	if pkceChallenge(posted.Get("code_verifier")) != query.Get("code_challenge") {
		t.Fatal("authorization challenge does not match exchanged verifier")
	}
}

// The callback tries 1455 and then 1457; the fallback test above runs on ports of its own, so
// this is what pins the ones a login really uses.
func TestTheLoginCallbackTries1455Then1457(t *testing.T) {
	if !slices.Equal(loginPorts, []int{1455, 1457}) {
		t.Errorf("loginPorts = %v, want [1455 1457]", loginPorts)
	}
}

func TestAccountIDAcceptsDirectAndNestedOpenAIClaims(t *testing.T) {
	for _, tc := range []struct {
		name, claims string
	}{
		{"direct", `{"https://api.openai.com/auth.chatgpt_account_id":"acct-direct"}`},
		{"nested", `{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-nested"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountID(jwt(tc.claims)); got != "acct-"+tc.name {
				t.Fatalf("accountID = %q", got)
			}
		})
	}
}

func TestJailActionsExcludeMachineWideDestructiveOperations(t *testing.T) {
	for _, action := range []string{"ping", "token", "refresh", "status", "login"} {
		if !jailActionAllowed(action) {
			t.Errorf("expected jail action %q to be allowed", action)
		}
	}
	for _, action := range []string{"import", "logout", "replace", ""} {
		if jailActionAllowed(action) {
			t.Errorf("machine-wide action %q is available to a jail", action)
		}
	}
}

func TestCodexViewContainsOpaqueGenerationMarkerNotCanonicalRefreshToken(t *testing.T) {
	result := openaiauth.Result{
		State: openaiauth.State{
			AccessToken: "access", IDToken: "id", RefreshToken: "canonical-secret",
			ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Generation: 42,
		},
		Decision: openaiauth.DecisionCached,
	}
	view := codexView(result)
	if view["refresh_token"] != "yolo-broker:42" {
		t.Fatalf("refresh view = %q, want opaque generation marker", view["refresh_token"])
	}
	for _, value := range view {
		if value == "canonical-secret" {
			t.Fatal("Codex view exposed canonical refresh token")
		}
	}
	if generation, err := parseGenerationMarker("yolo-broker:42"); err != nil || generation != 42 {
		t.Fatalf("parse marker = %d, %v", generation, err)
	}
	for _, malformed := range []string{"", "canonical-secret", "yolo-broker:0", "yolo-broker:nope"} {
		if _, err := parseGenerationMarker(malformed); err == nil {
			t.Errorf("parseGenerationMarker(%q) succeeded", malformed)
		}
	}
}

// shortSocketDir returns a per-test directory short enough to hold an AF_UNIX socket path.
//
// darwin's sun_path is 104 bytes including the NUL (Linux's is 108), and t.TempDir() is rooted
// at TMPDIR — which on macOS is /var/folders/<2>/<26>/T/, ~49 bytes before the test name is
// appended. This package's sockets overran it.
//
// ⚠ THE SYMPTOM IS NOT A BIND ERROR, which is why it cost a CI run to find: the listener is
// reached through a helper, so an over-long path surfaces as "socket was not published" after a
// timeout. MEASURED 2026-09-15 — check-macos red on `35bf7b47`; reproduced on Linux by pointing
// TMPDIR at an 80-byte path, which is stricter than darwin's own ~49.
//
// The same helper, with the same comment, exists in internal/oauthbroker and eight other
// packages take the same MkdirTemp("/tmp", …) approach. It is per-package because a test helper
// cannot be imported across them.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-oaid-")
	if err != nil {
		t.Fatal(err)
	}
	if len(d)+len("/fronted.sock.host") > 103 {
		t.Fatalf("short socket dir is not short: %s", d)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
