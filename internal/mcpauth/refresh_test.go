package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// oauthServer is a loopback MCP resource and authorization server. The handlers are the
// test's own; nothing here reaches a real service.
func oauthServer(t *testing.T, mux func(base string) *http.ServeMux) *httptest.Server {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux(base).ServeHTTP(w, r)
	}))
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestDiscoverFollowsProtectedResourceMetadataToTheAuthorizationServer(t *testing.T) {
	srv := oauthServer(t, func(base string) *http.ServeMux {
		m := http.NewServeMux()
		m.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base + "/as"}})
		})
		m.HandleFunc("/.well-known/oauth-authorization-server/as", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"issuer": base + "/as", "token_endpoint": base + "/as/token"})
		})
		return m
	})
	d, err := TokenClient{}.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if d.Issuer != srv.URL+"/as" || d.TokenEndpoint != srv.URL+"/as/token" {
		t.Fatalf("discovery = %+v", d)
	}
}

func TestDiscoverFallsBackToTheServerOriginAsIssuer(t *testing.T) {
	srv := oauthServer(t, func(base string) *http.ServeMux {
		m := http.NewServeMux()
		m.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"issuer": base, "token_endpoint": base + "/token"})
		})
		return m
	})
	d, err := TokenClient{}.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if d.Issuer != srv.URL || d.TokenEndpoint != srv.URL+"/token" {
		t.Fatalf("discovery = %+v", d)
	}
}

// RFC 8414 §3.3: metadata whose issuer is not the one asked for is not that issuer's.
func TestDiscoverRefusesMetadataNamingAnotherIssuer(t *testing.T) {
	srv := oauthServer(t, func(base string) *http.ServeMux {
		m := http.NewServeMux()
		m.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"issuer": "https://elsewhere.test", "token_endpoint": base + "/token"})
		})
		return m
	})
	if _, err := (TokenClient{}).Discover(context.Background(), srv.URL+"/mcp"); err == nil {
		t.Fatal("metadata for another issuer was accepted")
	}
}

func TestDiscoverRefusesPlainHTTPOffLoopback(t *testing.T) {
	if _, err := (TokenClient{}).Discover(context.Background(), "http://mcp.example.test/mcp"); err == nil {
		t.Fatal("a plain-http server off loopback was contacted")
	}
}

const secretRT = "rt-secret-value"

func tokenServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, Discovery) {
	srv := oauthServer(t, func(base string) *http.ServeMux {
		m := http.NewServeMux()
		m.HandleFunc("/token", handle)
		m.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
			t.Error("a redirect from the token endpoint was followed with the refresh token")
		})
		return m
	})
	return srv, Discovery{Issuer: srv.URL, TokenEndpoint: srv.URL + "/token"}
}

func TestRefreshSendsTheGrantAndReadsTheTokens(t *testing.T) {
	srv, d := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != secretRT ||
			r.PostForm.Get("client_id") != "client-1" || !strings.HasSuffix(r.PostForm.Get("resource"), "/mcp") {
			t.Errorf("request = %s %v", r.Method, r.PostForm)
		}
		writeJSON(w, 200, map[string]any{"access_token": "at-new", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt-new"})
	})
	tokens, err := TokenClient{}.Refresh(context.Background(), d, "client-1", secretRT, srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "at-new" || tokens.RefreshToken != "rt-new" || tokens.ExpiresIn != 3600 {
		t.Fatalf("tokens = %+v", tokens.redacted())
	}
}

func TestRefreshClassifiesFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       map[string]any
		definitive bool
		code       string
	}{
		{"invalid_grant", 400, map[string]any{"error": "invalid_grant"}, true, "invalid_grant"},
		{"invalid_client", 401, map[string]any{"error": "invalid_client"}, true, "invalid_client"},
		{"error on a 200", 200, map[string]any{"error": "invalid_grant"}, true, "invalid_grant"},
		{"dpop", 200, map[string]any{"access_token": "x", "token_type": "DPoP", "expires_in": 60}, true, "dpop"},
		{"server error", 503, map[string]any{"error": "temporarily_unavailable"}, false, "temporarily_unavailable"},
		{"rate limited", 429, map[string]any{}, false, ""},
		{"no access token", 200, map[string]any{"token_type": "Bearer"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, d := tokenServer(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, tc.body) })
			_, err := TokenClient{}.Refresh(context.Background(), d, "c", secretRT, srv.URL+"/mcp")
			var rerr *RefreshError
			if !errors.As(err, &rerr) {
				t.Fatalf("err = %v, want a RefreshError", err)
			}
			if rerr.Definitive != tc.definitive || rerr.Code != tc.code {
				t.Fatalf("RefreshError = %+v, want definitive %v code %q", rerr, tc.definitive, tc.code)
			}
			if strings.Contains(err.Error(), secretRT) {
				t.Fatal("the refresh token appears in the error")
			}
		})
	}
}

func TestRefreshNeverFollowsARedirect(t *testing.T) {
	srv, d := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	_, err := TokenClient{}.Refresh(context.Background(), d, "c", secretRT, srv.URL+"/mcp")
	var rerr *RefreshError
	if !errors.As(err, &rerr) || rerr.Definitive {
		t.Fatalf("err = %v, want a transient refusal", err)
	}
}

func TestRefreshNetworkFailureIsTransient(t *testing.T) {
	srv, d := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	_, err := TokenClient{}.Refresh(context.Background(), d, "c", secretRT, "http://127.0.0.1/mcp")
	var rerr *RefreshError
	if !errors.As(err, &rerr) || rerr.Definitive {
		t.Fatalf("err = %v, want a transient failure", err)
	}
}
