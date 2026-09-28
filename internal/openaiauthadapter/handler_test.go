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

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

func TestHandlerForwardsCallerTokenAndReturnsCodexShape(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	seen := ""
	h := Handler(testToken, func(_ context.Context, caller string) (Token, error) {
		seen = caller
		return Token{AccessToken: "access", IDToken: "id", RefreshToken: "yolo-broker:8", ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm(bound("yolo-broker:7")).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if seen != "yolo-broker:7" {
		t.Fatalf("caller refresh = %q, want the broker marker with the caller token stripped", seen)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["access_token"] != "access" || got["refresh_token"] != bound("yolo-broker:8") || got["id_token"] != "id" || got["expires_in"] != float64(3600) {
		t.Fatalf("response = %#v", got)
	}
}

func TestHandlerRefusesNonRefreshGrantsWithoutCallingBroker(t *testing.T) {
	called := false
	h := Handler(testToken, func(context.Context, string) (Token, error) { called = true; return Token{}, nil }, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=authorization_code"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%v body=%s", rr.Code, called, rr.Body.String())
	}
}

func TestHandlerMapsBrokerFailureWithoutLeakingRequestToken(t *testing.T) {
	h := Handler(testToken, func(context.Context, string) (Token, error) { return Token{}, errors.New("broker unavailable") }, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm(bound("yolo-broker:7")).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "broker unavailable") || strings.Contains(rr.Body.String(), testToken) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerRefusesCanonicalRefreshTokenFromBroker(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := Handler(testToken, func(context.Context, string) (Token, error) {
		return Token{
			AccessToken: "access", RefreshToken: "canonical-refresh-secret",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli(),
		}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(RefreshForm(bound("yolo-broker:7")).Encode()))
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
		done <- Serve(listener, testToken, func(context.Context, string) (Token, error) {
			return Token{
				AccessToken: "access", RefreshToken: "yolo-broker:3",
				ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(),
			}, nil
		})
	}()
	response, err := http.PostForm("http://"+listener.Addr().String()+"/oauth/token", RefreshForm(bound("yolo-broker:2")))
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
	h := Handler(testToken, func(_ context.Context, caller string) (Token, error) {
		got = caller
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })

	// Codex's measured body: JSON, with client_id alongside the two fields that matter.
	body := `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","grant_type":"refresh_token",` +
		`"refresh_token":"` + bound("yolo-broker:7") + `"}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("a Codex-shaped JSON request must be served; status = %d, body = %s",
			rr.Code, rr.Body.String())
	}
	if got != "yolo-broker:7" {
		t.Errorf("the caller's refresh token did not reach the broker: got %q", got)
	}
}

// A charset parameter must not defeat the content-type match — real clients send it.
func TestHandlerAcceptsJSONWithACharsetParameter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := Handler(testToken, func(_ context.Context, _ string) (Token, error) {
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token",
		strings.NewReader(`{"grant_type":"refresh_token","refresh_token":"`+bound("yolo-broker:7")+`"}`))
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
	h := Handler(testToken, func(_ context.Context, _ string) (Token, error) {
		return Token{AccessToken: "access", RefreshToken: "yolo-broker:8",
			ExpiresAtMS: now.Add(time.Hour).UnixMilli()}, nil
	}, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodPost, "/oauth/token",
		strings.NewReader(RefreshForm(bound("yolo-broker:7")).Encode()))
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
	h := Handler(testToken, func(_ context.Context, _ string) (Token, error) {
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

// testToken is a well-formed caller token (svcendpoint.NewToken's shape).
var testToken = strings.Repeat("ab", 32)

// bound is a broker marker as the auth.json writer leaves it for Codex: with testToken bound.
func bound(marker string) string { return openauthclient.BindCallerToken(marker, testToken) }

// THE CALLER TOKEN IS DEMANDED (docs/plans/notch-convergence.md §2.3, NC-D3). The measured
// exposure: a caller holding no credential posted `refresh_token=yolo-broker:999999` to the
// host adapter and got HTTP 200 with the access and id tokens, because the broker's stale-caller
// arm answers any generation mismatch with the current token. Every shape a stranger can send —
// the plain marker, a marker with some other token bound, a real-looking refresh token — is
// refused 401 in OAuth's own shape, naming yolo, and the broker is never asked.
func TestHandlerRefusesARefreshWithoutThisLaunchsCallerToken(t *testing.T) {
	other := strings.Repeat("cd", 32)
	for name, presented := range map[string]string{
		"the plain broker marker":       "yolo-broker:999999",
		"another launch's token":        openauthclient.BindCallerToken("yolo-broker:4", other),
		"a canonical-looking token":     "rt_abcdef",
		"a marker with a truncated tag": "yolo-broker:4." + testToken[:10],
	} {
		t.Run(name, func(t *testing.T) {
			asked := false
			h := Handler(testToken, func(context.Context, string) (Token, error) {
				asked = true
				return Token{AccessToken: "leaked-access", IDToken: "leaked-id",
					RefreshToken: "yolo-broker:5", ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli()}, nil
			}, nil)
			req := httptest.NewRequest(http.MethodPost, "/oauth/token",
				strings.NewReader(`{"grant_type":"refresh_token","refresh_token":"`+presented+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", rr.Code, rr.Body.String())
			}
			if asked {
				t.Error("the broker was asked on behalf of an unauthenticated caller")
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("the refusal is not OAuth's JSON error shape: %v: %s", err, rr.Body.String())
			}
			if body["error"] != "invalid_client" || !strings.Contains(body["error_description"], "yolo") {
				t.Errorf("refusal = %v, want invalid_client naming yolo", body)
			}
			for _, leaked := range []string{"leaked-access", "leaked-id", other, testToken} {
				if strings.Contains(rr.Body.String(), leaked) {
					t.Errorf("the refusal carries %q", leaked)
				}
			}
		})
	}
}

// Serve refuses to run unauthenticated, so no caller (the host half included) can wire the
// adapter up without a token.
func TestServeRefusesWithoutACallerToken(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Serve(listener, "", func(context.Context, string) (Token, error) { return Token{}, nil }); err == nil {
		t.Fatal("Serve accepted an empty caller token")
	}
}

func TestCallerTokenReadsTheLaunchsVariable(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if _, why := CallerToken(getenv); !strings.Contains(why, openauthclient.CallerTokenEnv) {
		t.Errorf("an unset token must name the variable: %q", why)
	}
	env[openauthclient.CallerTokenEnv] = "not-a-token"
	if _, why := CallerToken(getenv); why == "" {
		t.Error("a malformed token was accepted")
	}
	env[openauthclient.CallerTokenEnv] = testToken
	if tok, why := CallerToken(getenv); tok != testToken || why != "" {
		t.Errorf("CallerToken = %q, %q", tok, why)
	}
}
