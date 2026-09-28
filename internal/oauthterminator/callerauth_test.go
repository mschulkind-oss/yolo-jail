package oauthterminator

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// callerauth_test.go pins the terminator's half of Claude OAuth caller authentication
// (docs/plans/notch-convergence.md §2.3, NC-D3): a refresh grant hands the broker the refresh
// token its caller presented, which the broker authenticates the caller by, and a refusal is a
// 401 whose top-level error is never invalid_grant. Through the real makeHandler, so deleting
// the PresentedRefreshToken argument at the call site fails it.

func TestARefreshGrantHandsTheBrokerThePresentedRefreshToken(t *testing.T) {
	requests := make(chan map[string]any, 1)
	double := startRelayDouble(t, func(c net.Conn) {
		body, err := readFramedRequest(c)
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		requests <- req
		out, _ := json.Marshal(map[string]any{
			"error": CallerUnauthenticated, "message": "yolo claude-oauth-broker: refused",
		})
		writeFramedResponse(c, out)
	})
	srv := &http.Server{Handler: makeHandler(double.endpointPath)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	defer srv.Close()

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+ln.Addr().String()+"/v1/oauth/token", "application/json",
		strings.NewReader(`{"grant_type":"refresh_token","refresh_token":"sk-ant-ort01-presented","client_id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var req map[string]any
	select {
	case req = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminator never asked the broker")
	}
	if req["action"] != "refresh" || req[PresentedRefreshTokenKey] != "sk-ant-ort01-presented" {
		t.Errorf("the broker was asked %v, want action=refresh carrying the presented refresh token", req)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a caller the broker could not authenticate got %d, want 401", resp.StatusCode)
	}
	var refusal map[string]any
	if err := json.Unmarshal(body, &refusal); err != nil {
		t.Fatalf("refusal body is not JSON: %s", body)
	}
	if refusal["error"] == "invalid_grant" {
		t.Error("the refusal's top-level error is invalid_grant, which makes Claude blank the shared credentials")
	}
}

// The field is ALWAYS sent, empty when the caller presented none, because the broker reads
// its absence as a terminator older than caller authentication and serves that one as before.
func TestARefreshWithNoPresentedTokenStillSendsTheField(t *testing.T) {
	if got := PresentedRefreshToken([]byte(`{"grant_type":"refresh_token"}`)); got != "" {
		t.Errorf("PresentedRefreshToken = %q, want empty", got)
	}
	requests := make(chan map[string]any, 1)
	double := startRelayDouble(t, func(c net.Conn) {
		body, err := readFramedRequest(c)
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		requests <- req
		writeFramedResponse(c, []byte(`{"error":"caller_unauthenticated"}`))
	})
	_ = Refresh(double.endpointPath, "")
	req := <-requests
	if v, present := req[PresentedRefreshTokenKey]; !present || v != "" {
		t.Errorf("the refresh frame = %v; the presented-token field must be sent, empty", req)
	}
}
