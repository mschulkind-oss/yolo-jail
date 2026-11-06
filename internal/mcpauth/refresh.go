package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenClient is the broker's OAuth client for one-sign-in servers (§8.4,
// docs/design/mcp-auth-sharing.md): discovery from the server URL the user configured, and the
// refresh_token grant. No error it returns carries a token value, and no request it makes
// follows a redirect, so a refresh token is only ever sent to the token endpoint the trusted
// discovery named.
type TokenClient struct {
	// HTTP is the client to use; nil means a default with a 30 s timeout.
	HTTP *http.Client
}

// Discovery is what a refresh needs from the server's metadata.
type Discovery struct {
	Issuer        string
	TokenEndpoint string
}

// Tokens is a token endpoint's successful answer.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

// redacted is Tokens without its values, for a test's failure message.
func (t Tokens) redacted() string {
	return fmt.Sprintf("{access:%v refresh:%v expires_in:%d}", t.AccessToken != "", t.RefreshToken != "", t.ExpiresIn)
}

// RefreshError is a failed refresh. Definitive means retrying the same refresh token cannot
// succeed (an OAuth error the server returned, or a token type this broker cannot use);
// otherwise the failure is transient and the broker retries on its backoff. Code is the OAuth
// error code, "dpop" for a sender-constrained token, or "" when the server named none.
type RefreshError struct {
	Definitive bool
	Code       string
	Status     int
	Reason     string
}

func (e *RefreshError) Error() string {
	kind := "transient"
	if e.Definitive {
		kind = "definitive"
	}
	msg := "mcp-auth: refresh failed (" + kind
	if e.Code != "" {
		msg += ", " + e.Code
	}
	if e.Status != 0 {
		msg += fmt.Sprintf(", HTTP %d", e.Status)
	}
	msg += ")"
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

// maxOAuthBody bounds every metadata and token response read.
const maxOAuthBody = 1 << 20

func (c TokenClient) client() *http.Client {
	base := c.HTTP
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *base
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copied
}

// allowedURL is the scheme rule config validation applies to a remote MCP server: https, or
// http only to a loopback host.
func allowedURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("mcp-auth: not an absolute URL")
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return u, nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return u, nil
		}
	}
	return nil, fmt.Errorf("mcp-auth: %s is not https (or http to localhost); it is not contacted", u.Redacted())
}

// wellKnown is RFC 8414 §3.1 / RFC 9728 §3.1's construction: the well-known segment goes
// between the origin and the path.
func wellKnown(u *url.URL, suffix string) string {
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	return u.Scheme + "://" + u.Host + "/.well-known/" + suffix + path
}

func (c TokenClient) getJSON(ctx context.Context, raw string, into any) (bool, error) {
	if _, err := allowedURL(raw); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return false, fmt.Errorf("mcp-auth: metadata request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthBody)).Decode(into); err != nil {
		return false, fmt.Errorf("mcp-auth: metadata at %s is not JSON", raw)
	}
	return true, nil
}

func sameIssuer(a, b string) bool { return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/") }

// Discover finds the authorization server for the MCP server at serverURL: its protected
// resource metadata (RFC 9728) names the issuer, and without one the server's origin is the
// issuer; then that issuer's metadata (RFC 8414, or OpenID discovery) names the token
// endpoint, and must name the issuer it was asked for.
func (c TokenClient) Discover(ctx context.Context, serverURL string) (Discovery, error) {
	server, err := allowedURL(serverURL)
	if err != nil {
		return Discovery{}, err
	}
	issuer := server.Scheme + "://" + server.Host
	candidates := []string{wellKnown(server, "oauth-protected-resource")}
	if p := strings.TrimSuffix(server.EscapedPath(), "/"); p != "" {
		candidates = append(candidates, server.Scheme+"://"+server.Host+"/.well-known/oauth-protected-resource")
	}
	for _, candidate := range candidates {
		var prm struct {
			AuthorizationServers []string `json:"authorization_servers"`
		}
		found, err := c.getJSON(ctx, candidate, &prm)
		if err != nil {
			return Discovery{}, err
		}
		if found && len(prm.AuthorizationServers) > 0 {
			issuer = prm.AuthorizationServers[0]
			break
		}
	}
	issuerURL, err := allowedURL(issuer)
	if err != nil {
		return Discovery{}, err
	}
	metadata := []string{wellKnown(issuerURL, "oauth-authorization-server"), wellKnown(issuerURL, "openid-configuration"),
		strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"}
	for _, candidate := range metadata {
		var as struct {
			Issuer        string `json:"issuer"`
			TokenEndpoint string `json:"token_endpoint"`
		}
		found, err := c.getJSON(ctx, candidate, &as)
		if err != nil {
			return Discovery{}, err
		}
		if !found {
			continue
		}
		if !sameIssuer(as.Issuer, issuer) {
			return Discovery{}, fmt.Errorf("mcp-auth: authorization server metadata names another issuer; its sign-in is not refreshed")
		}
		if _, err := allowedURL(as.TokenEndpoint); err != nil {
			return Discovery{}, err
		}
		return Discovery{Issuer: issuer, TokenEndpoint: as.TokenEndpoint}, nil
	}
	return Discovery{}, fmt.Errorf("mcp-auth: no authorization server metadata found for %s", server.Redacted())
}

// definitiveCodes are the OAuth errors (RFC 6749 §5.2) a retry of the same request cannot cure.
var definitiveCodes = map[string]bool{
	"invalid_grant": true, "invalid_client": true, "unauthorized_client": true,
	"unsupported_grant_type": true, "invalid_scope": true, "invalid_request": true,
	"invalid_dpop_proof": true, "use_dpop_nonce": true,
}

// Refresh redeems refreshToken at the discovered token endpoint, naming the MCP server as the
// resource (RFC 8707), and returns the new tokens. A token endpoint that returns no new refresh
// token leaves the old one current (RFC 6749 §6), which the caller keeps.
func (c TokenClient) Refresh(ctx context.Context, d Discovery, clientID, refreshToken, resource string) (Tokens, error) {
	if _, err := allowedURL(d.TokenEndpoint); err != nil {
		return Tokens{}, &RefreshError{Definitive: true, Reason: err.Error()}
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	if clientID != "" {
		form.Set("client_id", clientID)
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, &RefreshError{Definitive: true, Reason: "cannot build the token request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return Tokens{}, &RefreshError{Reason: "token endpoint unreachable"}
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken  string          `json:"access_token"`
		TokenType    string          `json:"token_type"`
		ExpiresIn    json.Number     `json:"expires_in"`
		RefreshToken string          `json:"refresh_token"`
		Error        string          `json:"error"`
		Description  json.RawMessage `json:"error_description"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthBody)).Decode(&body)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return Tokens{}, &RefreshError{Status: resp.StatusCode, Reason: "the token endpoint redirected; the refresh token was not resent"}
	}
	if body.Error != "" {
		code := body.Error
		if len(code) > 64 {
			code = code[:64]
		}
		definitive := definitiveCodes[code] || (resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout)
		return Tokens{}, &RefreshError{Definitive: definitive, Code: code, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, &RefreshError{Status: resp.StatusCode}
	}
	if decodeErr != nil || body.AccessToken == "" {
		return Tokens{}, &RefreshError{Status: resp.StatusCode, Reason: "the token response has no access token"}
	}
	if strings.EqualFold(body.TokenType, "DPoP") {
		return Tokens{}, &RefreshError{Definitive: true, Code: "dpop", Reason: "a sender-constrained token cannot be shared"}
	}
	var expiresIn int64
	if body.ExpiresIn != "" {
		if expiresIn, err = body.ExpiresIn.Int64(); err != nil || expiresIn < 0 {
			return Tokens{}, &RefreshError{Status: resp.StatusCode, Reason: "the token response has an invalid expires_in"}
		}
	}
	return Tokens{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, ExpiresIn: expiresIn}, nil
}
