// Package openaiauthadapter exposes the OpenAI token endpoint shape that Codex
// expects on jail loopback. It forwards refresh decisions to yolo's authenticated
// host credential service; no canonical refresh token is stored in the jail.
package openaiauthadapter

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

// Token is the agent-shaped view returned by the host broker.
type Token struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAtMS  int64  `json:"expires_at"`
}

// Refresh asks the host broker to resolve a Codex refresh attempt. The caller
// token is used only for stale-caller detection by the broker.
type Refresh func(context.Context, string) (Token, error)

// Handler returns the loopback HTTP endpoint used by
// CODEX_REFRESH_TOKEN_URL_OVERRIDE.
func Handler(refresh Refresh, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
			writeError(w, http.StatusNotFound, "invalid_request", "unknown OpenAI credential endpoint")
			return
		}
		grantType, callerRefresh, ok := readTokenRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request", "malformed token request")
			return
		}
		if grantType != "refresh_token" {
			writeError(w, http.StatusBadRequest, "unsupported_grant_type", "only refresh_token is supported")
			return
		}
		if callerRefresh == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
			return
		}
		token, err := refresh(r.Context(), callerRefresh)
		if err != nil {
			writeError(w, http.StatusBadGateway, "temporarily_unavailable", err.Error())
			return
		}
		if token.AccessToken == "" || !validGenerationMarker(token.RefreshToken) || token.ExpiresAtMS <= 0 {
			writeError(w, http.StatusBadGateway, "server_error", "credential service returned an incomplete token view")
			return
		}
		expiresIn := max(0, int(time.UnixMilli(token.ExpiresAtMS).Sub(now()).Seconds()))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  token.AccessToken,
			"id_token":      token.IDToken,
			"refresh_token": token.RefreshToken,
			"expires_in":    expiresIn,
			"token_type":    "Bearer",
		})
	})
}

func validGenerationMarker(value string) bool {
	generation, err := strconv.ParseUint(strings.TrimPrefix(value, "yolo-broker:"), 10, 64)
	return err == nil && generation > 0 && strings.HasPrefix(value, "yolo-broker:")
}

func writeError(w http.ResponseWriter, status int, code, description string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// RefreshForm is useful to callers and tests that need Codex's exact request
// shape without duplicating the endpoint contract.
func RefreshForm(refreshToken string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Main runs the jail-local HTTP adapter used by Codex's refresh URL override.
// The adapter has no credential state: every request crosses the authenticated
// endpoint file published for this jail and the host broker makes the refresh
// decision.
func Main(args []string) int {
	fs := flag.NewFlagSet("openai-auth-adapter", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:1460", "loopback address to listen on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "openai-auth-adapter: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	endpoint := os.Getenv(openauthclient.EndpointEnv)
	refresh := func(_ context.Context, callerRefresh string) (Token, error) {
		response, err := openauthclient.Request(endpoint, map[string]any{
			"action": "refresh", "refresh_token": callerRefresh,
		}, os.Stderr)
		if err != nil {
			return Token{}, err
		}
		var token Token
		if err := json.Unmarshal(response, &token); err != nil {
			return Token{}, fmt.Errorf("decode OpenAI credential response: %w", err)
		}
		return token, nil
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "openai-auth-adapter:", err)
		return 1
	}
	if err := Serve(listener, refresh); err != nil {
		fmt.Fprintln(os.Stderr, "openai-auth-adapter:", err)
		return 1
	}
	return 0
}

// Serve exposes Handler on an already-bound listener. Host-managed launches
// use this form with 127.0.0.1:0 so concurrent workspaces never contend for a
// fixed port; closing listener stops the server.
func Serve(listener net.Listener, refresh Refresh) error {
	server := &http.Server{Handler: Handler(refresh, nil), ReadHeaderTimeout: 5 * time.Second}
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// readTokenRequest pulls the grant type and refresh token out of a token request, accepting BOTH a
// JSON body and a form-encoded one.
//
// # Why JSON, and why this was a live defect
//
// Codex POSTs JSON — `.header("Content-Type","application/json").json(&refresh_request)` — and has
// since 0.56.0 (2025-11-07), through the installed 0.145.0 and current 0.155.1. This handler read
// the request with `r.ParseForm()`, which for an `application/json` body reads NOTHING: the form is
// empty, `grant_type` comes back "", and every Codex refresh was answered
// `unsupported_grant_type` (400). Measured 2026-09-22 by running Codex's request shape through
// `ParseForm` directly.
//
// So the adapter could not serve the one client it exists for, and no test caught it because every
// fixture was form-encoded — the handler and its tests agreed with each other and with nothing else.
//
// The form branch is KEPT rather than replaced: the OAuth spec's token endpoint is form-encoded, so
// a future client that follows it must still work, and dropping the branch would trade one silent
// incompatibility for another.
//
// Field names agree across both encodings (`client_id`, `grant_type`, `refresh_token`), which is why
// a decode is the whole fix.
func readTokenRequest(r *http.Request) (grantType, refreshToken string, ok bool) {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i] // strip a charset parameter
	}
	if strings.EqualFold(strings.TrimSpace(ct), "application/json") {
		var body struct {
			GrantType    string `json:"grant_type"`
			RefreshToken string `json:"refresh_token"`
		}
		// A body this cannot decode is malformed, not empty: answering "grant_type missing" for
		// truncated JSON would send the caller looking at the wrong field.
		if err := json.NewDecoder(io.LimitReader(r.Body, tokenRequestBodyLimit)).Decode(&body); err != nil {
			return "", "", false
		}
		return body.GrantType, body.RefreshToken, true
	}
	if err := r.ParseForm(); err != nil {
		return "", "", false
	}
	return r.Form.Get("grant_type"), r.Form.Get("refresh_token"), true
}

// tokenRequestBodyLimit bounds the decode. A token request is a few hundred bytes; the limit exists
// so a malformed or hostile body cannot make this handler read without end, the same reason every
// other body reader in this tree is bounded.
const tokenRequestBodyLimit = 64 << 10
