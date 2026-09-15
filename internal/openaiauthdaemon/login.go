package openaiauthdaemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

// LoginFlow is one PKCE browser login bound to host loopback. URL is safe to
// present to the user; Wait validates state, exchanges the code, and closes the
// listener. Login secrets are retained only in memory.
type LoginFlow struct {
	URL         string
	RedirectURI string
	server      *http.Server
	listener    net.Listener
	verifier    string
	state       string
	code        chan string
	err         chan error
	upstream    Upstream
}

func StartLogin(authorizeURL string, upstream Upstream) (*LoginFlow, error) {
	if authorizeURL == "" {
		authorizeURL = DefaultAuthorizeURL
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLToken(16)
	if err != nil {
		return nil, err
	}
	listener, port, err := listenLoginPort()
	if err != nil {
		return nil, err
	}
	redirectURI := "http://localhost:" + strconv.Itoa(port) + "/auth/callback"
	flow := &LoginFlow{
		RedirectURI: redirectURI, listener: listener, verifier: verifier, state: state,
		code: make(chan string, 1), err: make(chan error, 1), upstream: upstream,
	}
	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {ClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {Scope},
		"code_challenge":             {pkceChallenge(verifier)},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"codex_cli_rs"},
	}
	flow.URL = authorizeURL + "?" + params.Encode()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", flow.callback)
	flow.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := flow.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case flow.err <- err:
			default:
			}
		}
	}()
	_ = browserOpen(flow.URL)
	return flow, nil
}

var browserOpen = openBrowser

func openBrowser(target string) error {
	candidates := [][]string{{"xdg-open", target}, {"gio", "open", target}}
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"open", target}}
	}
	for _, argv := range candidates {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, argv[1:]...)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return errors.New("no browser opener found")
}

func listenLoginPort() (net.Listener, int, error) {
	var last error
	for _, port := range []int{1455, 1457} {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return listener, port, nil
		}
		last = err
	}
	return nil, 0, fmt.Errorf("bind OpenAI browser callback ports 1455 and 1457: %w", last)
}

func (f *LoginFlow) callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("state") != f.state {
		http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}
	select {
	case f.code <- code:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>Authentication complete</title><p>OpenAI authentication completed. You can close this window.</p>"))
	default:
		http.Error(w, "OAuth callback already consumed", http.StatusConflict)
	}
}

func (f *LoginFlow) Wait(ctx context.Context) (openaiauth.Tokens, error) {
	defer f.Close()
	select {
	case code := <-f.code:
		return f.upstream.ExchangeCode(context.WithoutCancel(ctx), code, f.verifier, f.RedirectURI)
	case err := <-f.err:
		return openaiauth.Tokens{}, err
	case <-ctx.Done():
		return openaiauth.Tokens{}, ctx.Err()
	}
}

func (f *LoginFlow) Close() {
	if f.server != nil {
		_ = f.server.Close()
	}
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
