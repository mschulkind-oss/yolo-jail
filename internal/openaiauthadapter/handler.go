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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// Token is the agent-shaped view returned by the host broker.
type Token struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAtMS  int64  `json:"expires_at"`
}

// Refresh asks the host broker to resolve a Codex refresh attempt. The marker it is handed is
// the broker's own `yolo-broker:<generation>`, with the caller token already checked and
// stripped; the broker uses it only for stale-caller detection.
type Refresh func(context.Context, string) (Token, error)

// Handler returns the loopback HTTP endpoint used by CODEX_REFRESH_TOKEN_URL_OVERRIDE.
//
// callerToken is this launch's caller token (docs/plans/notch-convergence.md §2.3, NC-D3), and
// a request must present it bound into its refresh marker (openauthclient.SplitCallerMarker) or
// it is refused 401 before the broker is asked anything. WHY: the port is a loopback port, and
// every process sharing that loopback can reach it — the host itself for the host half, and a
// jail on `network.mode: host` or a nested podman forced onto `--net=host` for the jail half.
// The broker's stale-caller arm answers any generation marker with the current access and id
// tokens, so an unchecked adapter hands the user's ChatGPT tokens to anyone who can connect.
// Codex cannot send a header, but it sends back unchanged the refresh token yolo wrote into its
// auth.json, and that token is where the writer bound the caller token.
//
// The refusal is OAuth's own error shape and names yolo, and it is never a 5xx a client would
// retry. The marker the broker answers with is bound again before it is returned, so Codex's
// next refresh carries the token too.
func Handler(callerToken string, refresh Refresh, now func() time.Time) http.Handler {
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
		marker, presented, bound := openauthclient.SplitCallerMarker(callerRefresh)
		if !bound || !openauthclient.CallerTokenMatches(presented, callerToken) {
			// Never the presented value in the log or the body: a wrong one may be somebody's
			// real refresh token sent to the wrong port.
			reason := callerTokenMissing
			if bound {
				reason = callerTokenWrong
			}
			fmt.Fprintf(os.Stderr, "openai-auth-adapter: %s %s 401 — refused: caller token %s "+
				"(the broker was not asked)\n", r.Method, r.URL.Path, reason)
			w.Header().Set("WWW-Authenticate", `Bearer realm="yolo-openai-auth-adapter"`)
			writeError(w, http.StatusUnauthorized, "invalid_client", callerRefusalMessage(reason))
			return
		}
		token, err := refresh(r.Context(), marker)
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
			"refresh_token": openauthclient.BindCallerToken(token.RefreshToken, callerToken),
			"expires_in":    expiresIn,
			"token_type":    "Bearer",
		})
	})
}

// The two refusal reasons, which differ only in what a caller that got one should check.
const (
	callerTokenMissing = "missing"
	callerTokenWrong   = "wrong"
)

// callerRefusalMessage is the 401's error_description.
func callerRefusalMessage(reason string) string {
	what := "carried no caller token"
	if reason == callerTokenWrong {
		what = "carried a caller token that is not this launch's"
	}
	return "yolo openai-auth adapter: refused — this refresh " + what + ". The adapter serves " +
		"only the Codex this launch started, whose auth.json refresh marker yolo binds to the " +
		"launch's token; a loopback shared with other processes makes this port reachable from " +
		"outside the launch (docs/plans/notch-convergence.md §2.3). A Codex that gets this was " +
		"started without its launcher, or by an older yolo: restart it from a fresh `yolo` entry"
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
//
// It serves only behind this launch's caller token ($YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN,
// which the launcher mints because the loophole declares `jail_daemon.caller_token`). Handed
// none, or a malformed one, it binds nothing and idles until it is stopped, logging why: a
// daemon that exited instead would crash-loop under `restart: on-failure`, and one that served
// would answer every process on its loopback.
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
	callerToken, why := CallerToken(os.Getenv)
	if why != "" {
		fmt.Fprintln(os.Stderr, "openai-auth-adapter: idling, serving nothing:", why)
		idleUntilStopped()
		return 0
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
	fmt.Fprintf(os.Stderr, "openai-auth-adapter: serving on %s; every refresh must carry this "+
		"launch's caller token ($%s)\n", listener.Addr(), openauthclient.CallerTokenEnv)
	if err := Serve(listener, callerToken, refresh); err != nil {
		fmt.Fprintln(os.Stderr, "openai-auth-adapter:", err)
		return 1
	}
	return 0
}

// idleUntilStopped blocks until the supervisor stops this daemon. A variable so a test can
// observe the idle instead of blocking on it.
var idleUntilStopped = func() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	<-ctx.Done()
}

// CallerToken reads and checks the jail adapter's caller token, returning why it cannot serve
// when it cannot. The format is the launcher's (svcendpoint.NewToken: 64 lowercase hex).
func CallerToken(getenv func(string) string) (string, string) {
	tok := getenv(openauthclient.CallerTokenEnv)
	if tok == "" {
		return "", "this launch handed the adapter no caller token ($" + openauthclient.CallerTokenEnv +
			" is unset), and it serves no caller it cannot authenticate — a launcher older than " +
			"the jail's binaries is the usual cause"
	}
	if !svcendpoint.IsToken(tok) {
		return "", "$" + openauthclient.CallerTokenEnv + " is not a caller token this launcher " +
			"mints (64 lowercase hex characters)"
	}
	return tok, ""
}

// Serve exposes Handler on an already-bound listener, behind callerToken. Host-managed launches
// use this form with 127.0.0.1:0 so concurrent workspaces never contend for a fixed port, and
// mint their own token; closing listener stops the server. It refuses to serve behind anything
// that is not a well-formed token, so no caller can wire it up unauthenticated.
func Serve(listener net.Listener, callerToken string, refresh Refresh) error {
	if !svcendpoint.IsToken(callerToken) {
		return errors.New("refusing to serve the OpenAI credential adapter without a caller token")
	}
	server := &http.Server{Handler: Handler(callerToken, refresh, nil), ReadHeaderTimeout: 5 * time.Second}
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
