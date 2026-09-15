package openaiauthdaemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

const (
	ClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	DefaultTokenURL     = "https://auth.openai.com/oauth/token"
	Scope               = "openid profile email offline_access"
)

// Upstream performs OpenAI's provider-specific OAuth exchanges. Transaction
// ownership and persistence remain in openaiauth.Broker.
type Upstream struct {
	TokenURL string
	Client   *http.Client
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (u Upstream) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (u Upstream) endpoint() string {
	if u.TokenURL != "" {
		return u.TokenURL
	}
	return DefaultTokenURL
}

func (u Upstream) Refresh(ctx context.Context, refreshToken string) (openaiauth.Tokens, error) {
	return u.exchange(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	})
}

func (u Upstream) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (openaiauth.Tokens, error) {
	return u.exchange(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
}

func (u Upstream) exchange(ctx context.Context, form url.Values) (openaiauth.Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return openaiauth.Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := u.client().Do(req)
	if err != nil {
		return openaiauth.Tokens{}, &openaiauth.RefreshError{Kind: openaiauth.ErrorTransient, Code: "network_error", Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return openaiauth.Tokens{}, &openaiauth.RefreshError{Kind: openaiauth.ErrorTransient, Code: "read_error", Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := oauthErrorCode(body)
		kind := openaiauth.ErrorPermanent
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			kind = openaiauth.ErrorTransient
		}
		return openaiauth.Tokens{}, &openaiauth.RefreshError{
			Kind: kind,
			Code: code,
			Err:  fmt.Errorf("OpenAI token endpoint returned HTTP %d", resp.StatusCode),
		}
	}
	var decoded tokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return openaiauth.Tokens{}, &openaiauth.RefreshError{Kind: openaiauth.ErrorTransient, Code: "invalid_response", Err: errors.New("OpenAI token endpoint returned invalid JSON")}
	}
	if decoded.AccessToken == "" || decoded.ExpiresIn <= 0 {
		return openaiauth.Tokens{}, &openaiauth.RefreshError{Kind: openaiauth.ErrorTransient, Code: "incomplete_response", Err: errors.New("OpenAI token endpoint response omitted required fields")}
	}
	return openaiauth.Tokens{
		AccessToken: decoded.AccessToken, IDToken: decoded.IDToken,
		RefreshToken: decoded.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(decoded.ExpiresIn) * time.Second),
		AccountID:    accountID(decoded.AccessToken),
	}, nil
}

func oauthErrorCode(body []byte) string {
	var decoded struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &decoded) == nil {
		switch value := decoded.Error.(type) {
		case string:
			if value != "" {
				return value
			}
		case map[string]any:
			if code, ok := value["code"].(string); ok && code != "" {
				return code
			}
		}
	}
	return "http_error"
}

func accountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if id, _ := claims["https://api.openai.com/auth.chatgpt_account_id"].(string); id != "" {
		return id
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}
