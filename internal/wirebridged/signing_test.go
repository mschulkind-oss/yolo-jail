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
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

const bedrockBase = "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"

// captureUpstream stands in for the network: every upstream request the bridge makes
// is recorded and answered here, so a route to a real AWS hostname is exercised end to
// end without a request leaving the process.
type captureUpstream struct {
	mu        sync.Mutex
	requests  []*http.Request
	bodies    [][]byte
	responses []func() *http.Response
}

func (c *captureUpstream) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, r)
	c.bodies = append(c.bodies, body)
	i := len(c.requests) - 1
	if i < len(c.responses) {
		return c.responses[i](), nil
	}
	return jsonResponse(200, openaiResp), nil
}

func (c *captureUpstream) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body))}
}

func withUpstream(t *testing.T) *captureUpstream {
	t.Helper()
	up := &captureUpstream{}
	old := upstreamTransport
	upstreamTransport = up
	t.Cleanup(func() { upstreamTransport = old })
	return up
}

func postMessage(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	h.ServeHTTP(rec, req)
	var doc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	return rec, doc
}

func signedHandler(env sigv4.Env) http.Handler {
	return newSignedChatHandler(bedrockBase, wirebridge.ChatOptions{},
		&bedrockSigner{region: "us-east-1", chain: &sigv4.Chain{Env: env}})
}

// TestBedrockUpstreamRequestsAreSignedOverWhatIsSent is the arm's headline: a request
// to bedrock-runtime carries a SigV4 Authorization for the right scope, a session
// token rides as X-Amz-Security-Token, no bearer is sent, and the signature verifies
// against the exact method, URL, headers and body that went upstream.
func TestBedrockUpstreamRequestsAreSignedOverWhatIsSent(t *testing.T) {
	up := withUpstream(t)
	creds := sigv4.Env{AccessKeyID: "AKIDTEST", SecretAccessKey: "secret-test", SessionToken: "session-test"}
	rec, _ := postMessage(t, signedHandler(creds))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if up.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.calls())
	}
	sent, body := up.requests[0], up.bodies[0]
	if sent.URL.Host != "bedrock-runtime.us-east-1.amazonaws.com" || sent.URL.Path != "/openai/v1/chat/completions" {
		t.Fatalf("upstream URL = %s", sent.URL)
	}
	auth := sent.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer") || !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDTEST/") ||
		!strings.Contains(auth, "/us-east-1/bedrock/aws4_request") {
		t.Fatalf("Authorization = %q, want a SigV4 signature for us-east-1/bedrock", auth)
	}
	if sent.Header.Get("X-Amz-Security-Token") != "session-test" {
		t.Errorf("a session credential's token must ride as X-Amz-Security-Token")
	}

	// Re-sign an identical request at the same instant: the signature must match, so
	// it covers exactly what was sent (headers set after signing would break this).
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
	if err := sigv4.Sign(check, body, sigv4.Credentials{AccessKeyID: "AKIDTEST",
		SecretAccessKey: "secret-test", SessionToken: "session-test"},
		sigv4.Options{Region: "us-east-1", Service: sigv4.BedrockService, Time: when}); err != nil {
		t.Fatal(err)
	}
	if check.Header.Get("Authorization") != auth {
		t.Errorf("the signature does not verify against what was sent:\n sent %s\n want %s",
			auth, check.Header.Get("Authorization"))
	}
}

// TestANonBedrockUpstreamIsNeverSigned: every other route keeps its bearer, and no
// AWS header appears on it.
func TestANonBedrockUpstreamIsNeverSigned(t *testing.T) {
	up := withUpstream(t)
	rec, _ := postMessage(t, newChatHandler("https://api.cerebras.ai/v1", "key-123", wirebridge.ChatOptions{}))
	if rec.Code != http.StatusOK || up.calls() != 1 {
		t.Fatalf("status %d, calls %d", rec.Code, up.calls())
	}
	h := up.requests[0].Header
	if h.Get("Authorization") != "Bearer key-123" || h.Get("X-Amz-Date") != "" {
		t.Errorf("a non-Bedrock upstream must carry its bearer and no signature: %v", h)
	}
}

func TestABearerOnlyBedrockChainSendsTheBearerUnsigned(t *testing.T) {
	up := withUpstream(t)
	rec, _ := postMessage(t, signedHandler(sigv4.Env{Bearer: "bedrock-api-key"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	h := up.requests[0].Header
	if h.Get("Authorization") != "Bearer bedrock-api-key" || h.Get("X-Amz-Date") != "" {
		t.Errorf("AWS_BEARER_TOKEN_BEDROCK must go as a bearer and never beside a signature: %v", h)
	}
}

func TestASignedRouteWithNoCredentialIsA401NamingTheSources(t *testing.T) {
	up := withUpstream(t)
	rec, doc := postMessage(t, signedHandler(sigv4.Env{}))
	if rec.Code != http.StatusUnauthorized || up.calls() != 0 {
		t.Fatalf("status %d, upstream calls %d; want 401 and no upstream call", rec.Code, up.calls())
	}
	msg := fmt.Sprint(doc["error"])
	for _, source := range []string{"AWS_ACCESS_KEY_ID", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_BEARER_TOKEN_BEDROCK"} {
		if !strings.Contains(msg, source) {
			t.Errorf("the 401 must name %s: %s", source, msg)
		}
	}
}

func TestADeadAWSAuthAdapterIsA503NamingIt(t *testing.T) {
	withUpstream(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	uri := dead.URL + "/credentials"
	dead.Close()
	rec, doc := postMessage(t, signedHandler(sigv4.Env{ContainerURI: uri}))
	msg := fmt.Sprint(doc["error"])
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(msg, "aws-auth") ||
		!strings.Contains(msg, "aws sso login") {
		t.Fatalf("status %d, message %s; want a 503 naming aws-auth and aws sso login", rec.Code, msg)
	}
}

func TestAWSAuthsOwnRefusalReachesTheAgent(t *testing.T) {
	withUpstream(t)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Code":"SessionExpired","Message":"your SSO session has lapsed: run aws sso login --profile work"}`))
	}))
	defer adapter.Close()
	rec, doc := postMessage(t, signedHandler(sigv4.Env{ContainerURI: adapter.URL + "/credentials"}))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(fmt.Sprint(doc["error"]), "aws sso login --profile work") {
		t.Fatalf("status %d, body %s; want 401 carrying aws-auth's own sentence", rec.Code, rec.Body)
	}
}

// credAdapter serves a fresh credential set per fetch and counts the fetches.
func credAdapter(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		fmt.Fprintf(w, `{"AccessKeyId":"ASIA%d","SecretAccessKey":"secret-%d","Token":"token-%d","Expiration":%q}`,
			i, i, i, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestAnExpiredSignatureIsRefreshedAndRetriedOnce(t *testing.T) {
	up := withUpstream(t)
	expired := func() *http.Response {
		r := jsonResponse(403, `{"message":"Signature expired: 20260925T000000Z is now earlier than 20260925T000500Z"}`)
		r.Header.Set("X-Amzn-ErrorType", "InvalidSignatureException")
		return r
	}
	up.responses = []func() *http.Response{expired}
	adapter, fetches := credAdapter(t)
	rec, _ := postMessage(t, signedHandler(sigv4.Env{ContainerURI: adapter.URL + "/credentials"}))
	if rec.Code != http.StatusOK || up.calls() != 2 || *fetches != 2 {
		t.Fatalf("status %d, upstream calls %d, credential fetches %d; want 200 after one refresh and one retry",
			rec.Code, up.calls(), *fetches)
	}
	if !strings.Contains(up.requests[1].Header.Get("Authorization"), "Credential=ASIA2/") {
		t.Errorf("the retry must sign with the refreshed set: %s", up.requests[1].Header.Get("Authorization"))
	}

	up2 := withUpstream(t)
	up2.responses = []func() *http.Response{expired, expired, expired}
	rec2, _ := postMessage(t, signedHandler(sigv4.Env{ContainerURI: adapter.URL + "/credentials"}))
	if rec2.Code != http.StatusForbidden || up2.calls() != 2 {
		t.Fatalf("a second expiry must be relayed after exactly one retry: status %d, calls %d", rec2.Code, up2.calls())
	}
}

func TestANonExpiry403IsRelayedWithoutARetry(t *testing.T) {
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response {
		return jsonResponse(403, `{"message":"User is not authorized to perform: bedrock:InvokeModel"}`)
	}}
	rec, doc := postMessage(t, signedHandler(sigv4.Env{AccessKeyID: "AKID", SecretAccessKey: "s"}))
	if rec.Code != http.StatusForbidden || up.calls() != 1 ||
		!strings.Contains(fmt.Sprint(doc["error"]), "not authorized") {
		t.Fatalf("status %d, calls %d, body %s; want AWS's 403 relayed once", rec.Code, up.calls(), rec.Body)
	}
}

func TestSigningNeverLogsACredential(t *testing.T) {
	read := captureDiag(t)
	up := withUpstream(t)
	up.responses = []func() *http.Response{func() *http.Response {
		return jsonResponse(403, `{"message":"The security token included in the request is expired"}`)
	}}
	adapter, _ := credAdapter(t)
	postMessage(t, signedHandler(sigv4.Env{ContainerURI: adapter.URL + "/credentials", ContainerToken: "container-token-x"}))
	postMessage(t, signedHandler(sigv4.Env{AccessKeyID: "AKIDSTATIC", SecretAccessKey: "static-secret-x", SessionToken: "static-token-x"}))
	log := read()
	for _, secret := range []string{"secret-1", "secret-2", "token-1", "token-2", "container-token-x",
		"static-secret-x", "static-token-x", "AWS4-HMAC-SHA256"} {
		if strings.Contains(log, secret) {
			t.Errorf("the bridge log carries %q:\n%s", secret, log)
		}
	}
}

// TestRouteForSignsOnlyABedrockHost pins the boot decision: the route carries a
// signing region exactly when the provider's upstream host is bedrock-runtime.
func TestRouteForSignsOnlyABedrockHost(t *testing.T) {
	for upstream, want := range map[string]string{
		bedrockBase: "us-east-1",
		"https://bedrock-runtime.eu-central-1.amazonaws.com/openai/v1":    "eu-central-1",
		"https://api.cerebras.ai/v1":                                      "",
		"https://bedrock-runtime.us-east-1.amazonaws.com.evil.example/v1": "",
		"http://bedrock-runtime.us-east-1.amazonaws.com/openai/v1":        "",
		"https://bedrock-runtime-fips.us-east-1.amazonaws.com/openai/v1":  "",
	} {
		providers := fmt.Sprintf(`{"b":{"endpoints":{
			"anthropic":{"base_url":"http://127.0.0.1:8214","wire_api":"anthropic"},
			"openai":{"base_url":%q,"wire_api":"openai-chat-completions"}}}}`, upstream)
		mustProviders(t, providers) // a fixture typo fails here, not as a wrong idle
		r, idle := resolveRoute(entrypoint.NewEnv(map[string]string{
			"YOLO_PROVIDERS":    providers,
			"YOLO_PROFILES":     `{"bp":{"provider":"b"}}`,
			"YOLO_USE_PROFILES": `{"claude":"bp"}`,
		}))
		if idle != "" {
			t.Fatalf("%s: idle %s", upstream, idle)
		}
		if r.SignRegion != want {
			t.Errorf("%s: SignRegion = %q, want %q", upstream, r.SignRegion, want)
		}
	}
}

// TestServeSignsABedrockRoute drives the PRODUCTION serve path: a route with a signing
// region reads the credential chain from the key channel at boot and serves signed.
// It fails if serve stops building the signed handler for such a route.
func TestServeSignsABedrockRoute(t *testing.T) {
	noReadinessPipe(t)
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	up := withUpstream(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userEnvFilePath(home), []byte(
		"export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-'AKIDBOOT'}\n"+
			"export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-'boot-secret'}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, route{ProviderName: "b", ListenAddr: "127.0.0.1:0",
			UpstreamBaseURL: bedrockBase, SignRegion: "us-east-1"},
			entrypoint.NewEnv(map[string]string{"JAIL_HOME": home}))
	}()
	defer func() { cancel(); <-done }()
	var addr string
	waitFor(t, func() bool {
		b, err := os.ReadFile(endpointFile)
		addr = strings.TrimSpace(string(b))
		return err == nil && addr != ""
	})
	resp, err := http.Post("http://"+addr+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if up.calls() != 1 || !strings.HasPrefix(up.requests[0].Header.Get("Authorization"),
		"AWS4-HMAC-SHA256 Credential=AKIDBOOT/") {
		t.Fatalf("serve did not sign the Bedrock route with the boot-read key pair: calls %d, auth %q",
			up.calls(), func() string {
				if up.calls() == 0 {
					return ""
				}
				return up.requests[0].Header.Get("Authorization")
			}())
	}
}

// TestServeIdlesABedrockRouteWithNoCredentialSource: the bridge never serves
// unauthenticated, so a Bedrock route with none of the three sources publishes nothing.
func TestServeIdlesABedrockRouteWithNoCredentialSource(t *testing.T) {
	noReadinessPipe(t)
	for _, v := range sigv4.EnvVars {
		t.Setenv(v, "")
	}
	read := captureDiag(t)
	endpointFile := filepath.Join(t.TempDir(), "wire-bridge.endpoint")
	old := EndpointFile
	EndpointFile = endpointFile
	t.Cleanup(func() { EndpointFile = old })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, route{ProviderName: "b", ListenAddr: "127.0.0.1:0",
			UpstreamBaseURL: bedrockBase, SignRegion: "us-east-1"},
			entrypoint.NewEnv(map[string]string{"JAIL_HOME": t.TempDir()}))
	}()
	waitFor(t, func() bool { return strings.Contains(read(), "idling") })
	cancel()
	<-done
	if _, err := os.Stat(endpointFile); err == nil {
		t.Fatal("a Bedrock route with no credential source published an endpoint")
	}
	if !strings.Contains(read(), "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Errorf("the idle line must name the sources it looked for:\n%s", read())
	}
}
