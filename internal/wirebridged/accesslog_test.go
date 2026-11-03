package wirebridged

// The request log (accesslog.go), driven through the production serve path (servePlan, via
// servedBridge) against a REAL upstream server: the bridge's own upstream client dials it over
// TLS, whatever host the route names, so a 500, a hang, a reset and a refused connect are the
// network's own failures rather than a stub's. Deleting withRequestLog from servePlan, or the
// observedUpstream transport from a client, fails these.

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/logstamp"
)

// realUpstream points every upstream request the bridge sends at addr, over TLS, trusting any
// certificate: the route's host is kept in the request (and the log), the bytes go to addr.
func realUpstream(t *testing.T, addr string) {
	t.Helper()
	old := upstreamTransport
	upstreamTransport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	t.Cleanup(func() { upstreamTransport = old })
}

// requestLine waits for the request line containing every fragment and returns it.
func requestLine(t *testing.T, logged func() string, fragments ...string) string {
	t.Helper()
	var found string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(logged(), "\n") {
			ok := strings.Contains(line, "wire-bridge: request #")
			for _, f := range fragments {
				ok = ok && strings.Contains(line, f)
			}
			if ok {
				found = line
			}
		}
		if found != "" {
			t.Logf("%s", found)
			if !logstamp.HasStamp([]byte(found)) {
				t.Errorf("the request line carries no timestamp: %q", found)
			}
			return found
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no request line containing %q. Full log:\n%s", fragments, logged())
	return ""
}

func postBridge(t *testing.T, ctx context.Context, url, body string) (*http.Response, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := bridgeClient.Do(req)
	if err == nil {
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	return resp, err
}

func TestTheRequestLogNamesAnUpstreamFiveHundred(t *testing.T) {
	logged := captureDiag(t)
	_, adapterAddr, _ := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer up.Close()
	realUpstream(t, up.Listener.Addr().String())

	resp, err := postBridge(t, context.Background(), "http://"+adapterAddr+"/v1/messages", anthropicHi)
	if err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("got %v %v, want the 502 the bridge relays a 5xx as", resp, err)
	}
	requestLine(t, logged, "POST /v1/messages 502", "listener=adapter", "caller=-", "auth=ok",
		"upstream=https://api.cerebras.ai/v1/chat/completions", "upstream_status=500",
		"upstream_headers_ms=", "bytes_out=", "elapsed_ms=", "streamed=no", "end=clean")
}

func TestTheRequestLogSaysAHangIsStillWaitingOnTheUpstream(t *testing.T) {
	stillWaitingOverride.Store(int64(50 * time.Millisecond))
	t.Cleanup(func() { stillWaitingOverride.Store(0) })
	logged := captureDiag(t)
	_, adapterAddr, _ := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	release := make(chan struct{})
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer up.Close()
	defer close(release)
	realUpstream(t, up.Listener.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := postBridge(t, ctx, "http://"+adapterAddr+"/v1/messages", anthropicHi); errc <- err }()
	waitFor(t, func() bool {
		return strings.Contains(logged(), "still waiting on upstream https://api.cerebras.ai/v1/chat/completions for response headers")
	})
	if l := logged(); !strings.Contains(l, "still in flight after") || !strings.Contains(l, "auth=ok") {
		t.Errorf("the still-waiting line does not place the request:\n%s", l)
	}
	cancel() // the agent gives up, as claude's own timeout would
	<-errc
	requestLine(t, logged, "POST /v1/messages", "upstream=https://api.cerebras.ai/v1/chat/completions",
		`upstream_error="canceled: the caller closed the request"`)
}

// A reset the request's write meets is EPIPE on macOS, not ECONNRESET, and is named as the
// connection's end like a reset, not as an unclassified error.
func TestABrokenPipeToTheUpstreamIsNamedAConnectionClosed(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://api.example.invalid/v1", Err: &net.OpError{Op: "write", Net: "tcp",
		Err: os.NewSyscallError("write", syscall.EPIPE)}}
	if got := describeUpstreamError(context.Background(), err); !strings.HasPrefix(got, "connection closed: ") {
		t.Fatalf("describeUpstreamError(broken pipe) = %q, want the connection-closed class", got)
	}
}

func TestTheRequestLogNamesAResetAndARefusedConnect(t *testing.T) {
	logged := captureDiag(t)
	_, adapterAddr, _ := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})

	// RESET: accept, then close with SO_LINGER 0, which sends RST.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.(*net.TCPConn).SetLinger(0)
			_ = c.Close()
		}
	}()
	realUpstream(t, ln.Addr().String())
	if resp, err := postBridge(t, context.Background(), "http://"+adapterAddr+"/v1/messages", anthropicHi); err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("reset: got %v %v, want a 502", resp, err)
	}
	requestLine(t, logged, "POST /v1/messages 502", `upstream_error="connection `)

	// REFUSED: a port nothing listens on.
	realUpstream(t, refusingLoopback(t))
	if resp, err := postBridge(t, context.Background(), "http://"+adapterAddr+"/v1/messages", anthropicHi); err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("refused: got %v %v, want a 502", resp, err)
	}
	requestLine(t, logged, "POST /v1/messages 502", `upstream_error="connect refused: `)
}

// A streamed via request: the caller is the agent the path names, the first byte is timed, a
// complete stream ends clean, and one the upstream cuts mid-stream says so and how.
func TestTheRequestLogFollowsAStreamToItsEnd(t *testing.T) {
	logged := captureDiag(t)
	_, _, viaAddr := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		if strings.Contains(string(body), "cut-me") {
			// Cut mid-stream: the connection dies with the reply's terminator unsent.
			conn, _, _ := http.NewResponseController(w).Hijack()
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	realUpstream(t, up.Listener.Addr().String())

	url := "http://" + viaAddr + "/agent/pi/chat/completions"
	if resp, err := postBridge(t, context.Background(), url, `{"model":"m","stream":true}`); err != nil || resp.StatusCode != 200 {
		t.Fatalf("clean stream: %v %v", resp, err)
	}
	requestLine(t, logged, "POST /agent/pi/chat/completions 200", "listener=via", "caller=pi", "auth=ok",
		"upstream=https://api.z.ai/api/paas/v4/chat/completions", "upstream_status=200",
		"first_byte_ms=", "streamed=yes", "end=clean")

	_, _ = postBridge(t, context.Background(), url, `{"model":"cut-me","stream":true}`)
	requestLine(t, logged, "POST /agent/pi/chat/completions 200", "caller=pi", `upstream_end="cut: `, "end=aborted")
}

// THE LOG NEVER CARRIES A CREDENTIAL. Every refusal and every served request on both listeners,
// with the real token and a wrong one presented both ways: neither string may reach the log,
// and each refusal says which kind it was.
func TestTheRequestLogNeverCarriesTheCallerToken(t *testing.T) {
	logged := captureDiag(t)
	_, adapterAddr, viaAddr := servedBridge(t, map[string]string{CallerTokenEnv: testCallerToken})
	wrong := strings.Repeat("ef", 32)
	for _, url := range []string{"http://" + adapterAddr + "/v1/messages", "http://" + viaAddr + "/agent/pi/chat/completions"} {
		for _, h := range []map[string]string{
			nil,
			{"Authorization": "Bearer " + wrong},
			{"X-Api-Key": wrong},
			{"Authorization": "Bearer " + testCallerToken},
			{"X-Api-Key": testCallerToken},
		} {
			sendAs(t, url, anthropicHi, h)
		}
	}
	requestLine(t, logged, "/v1/messages 401", "listener=adapter", "auth=missing-token", "upstream=-",
		"nothing was sent upstream")
	requestLine(t, logged, "/v1/messages 401", "auth=wrong-token")
	requestLine(t, logged, "/agent/pi/chat/completions 401", "listener=via", "caller=pi", "auth=wrong-token")
	requestLine(t, logged, "/agent/pi/chat/completions 200", "auth=ok")
	if l := logged(); strings.Contains(l, testCallerToken) || strings.Contains(l, wrong) {
		t.Fatalf("A PRESENTED CREDENTIAL REACHED THE LOG:\n%s", l)
	}
}
