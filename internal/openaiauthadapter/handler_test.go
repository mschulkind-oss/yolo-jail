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
