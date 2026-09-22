package openaiauthadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerForwardsCallerTokenAndReturnsCodexShape(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	seen := ""
	h := Handler(func(_ context.Context, caller string) (Token, error) {
		seen = caller
		return Token{AccessToken: "access", IDToken: "id", RefreshToken: "yolo-broker:8", ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm("old").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if seen != "old" {
		t.Fatalf("caller refresh = %q, want old", seen)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["access_token"] != "access" || got["refresh_token"] != "yolo-broker:8" || got["id_token"] != "id" || got["expires_in"] != float64(3600) {
		t.Fatalf("response = %#v", got)
	}
}

func TestHandlerRefusesNonRefreshGrantsWithoutCallingBroker(t *testing.T) {
	called := false
	h := Handler(func(context.Context, string) (Token, error) { called = true; return Token{}, nil }, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=authorization_code"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%v body=%s", rr.Code, called, rr.Body.String())
	}
}

func TestHandlerMapsBrokerFailureWithoutLeakingRequestToken(t *testing.T) {
	h := Handler(func(context.Context, string) (Token, error) { return Token{}, errors.New("broker unavailable") }, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm("secret-refresh").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "broker unavailable") || strings.Contains(rr.Body.String(), "secret-refresh") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerRefusesCanonicalRefreshTokenFromBroker(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := Handler(func(context.Context, string) (Token, error) {
		return Token{
			AccessToken: "access", RefreshToken: "canonical-refresh-secret",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli(),
		}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm("yolo-broker:7").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want refusal; body = %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "canonical-refresh-secret") {
		t.Fatalf("canonical refresh token crossed the adapter response: %s", rr.Body.String())
	}
}

func TestServeUsesCallerOwnedDynamicListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(listener, func(context.Context, string) (Token, error) {
			return Token{
				AccessToken: "access", RefreshToken: "yolo-broker:3",
				ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(),
			}, nil
		})
	}()
	response, err := http.PostForm("http://"+listener.Addr().String()+"/oauth/token", RefreshForm("yolo-broker:2"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after listener close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop when caller closed listener")
	}
}

// TestHandlerAcceptsCodexJSONBody is the defect this file could not see.
//
// Codex POSTs JSON — `.header("Content-Type","application/json").json(&refresh_request)` — and has
// since 0.56.0, through the installed 0.145.0 and current 0.155.1. The handler read the request with
// `r.ParseForm()`, which for an application/json body reads NOTHING, so every Codex refresh was
// answered `unsupported_grant_type` (400) and the adapter could not serve the one client it exists
// for.
//
// ⚠ NO TEST CAUGHT IT because every fixture here was form-encoded: the handler and its tests agreed
// with each other and with nothing else. That is why this case is written in Codex's OWN shape rather
// than added as a variant of the form one.
func TestHandlerAcceptsCodexJSONBody(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var got string
	h := Handler(func(_ context.Context, caller string) (Token, error) {
		got = caller
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })

	// Codex's measured body: JSON, with client_id alongside the two fields that matter.
	body := `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","grant_type":"refresh_token",` +
		`"refresh_token":"old"}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("a Codex-shaped JSON request must be served; status = %d, body = %s",
			rr.Code, rr.Body.String())
	}
	if got != "old" {
		t.Errorf("the caller's refresh token did not reach the broker: got %q", got)
	}
}

// A charset parameter must not defeat the content-type match — real clients send it.
func TestHandlerAcceptsJSONWithACharsetParameter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := Handler(func(_ context.Context, _ string) (Token, error) {
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token",
		strings.NewReader(`{"grant_type":"refresh_token","refresh_token":"old"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("a charset parameter must not defeat the match; status = %d", rr.Code)
	}
}

// The FORM branch survives. The OAuth spec's token endpoint is form-encoded, so dropping it would
// trade one silent incompatibility for another.
func TestHandlerStillAcceptsFormEncoded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := Handler(func(_ context.Context, _ string) (Token, error) {
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token",
		strings.NewReader(RefreshForm("old").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("form-encoded must keep working; status = %d", rr.Code)
	}
}

// Truncated JSON is MALFORMED, not "grant_type missing" — answering the latter sends the caller
// looking at the wrong field.
func TestHandlerRejectsTruncatedJSONAsMalformed(t *testing.T) {
	h := Handler(func(_ context.Context, _ string) (Token, error) {
		t.Fatal("the broker must not be called for a body that did not decode")
		return Token{}, nil
	}, time.Now)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token",
		strings.NewReader(`{"grant_type":"refresh_`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_request") {
		t.Errorf("want invalid_request, got %s", rr.Body.String())
	}
}
