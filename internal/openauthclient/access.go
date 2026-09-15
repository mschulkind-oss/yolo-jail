package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// AccessTokenView is the minimum broker credential view needed by a service
// that calls the OpenAI API. It deliberately cannot carry the canonical refresh
// token or an ID token, so consumers such as protocol bridges cannot become
// refresh authorities.
type AccessTokenView struct {
	Token      string
	ExpiresAt  time.Time
	AccountID  string
	Generation uint64
}

// RequestAccessToken obtains the restricted, short-lived access-token view
// through a jail's authenticated broker endpoint. Callers keep Token only in
// memory and obtain a new view when it expires or an upstream request is
// unauthorized.
func RequestAccessToken(endpointPath string, stderr io.Writer) (AccessTokenView, error) {
	response, err := Request(endpointPath, map[string]any{"action": "token", "view": "access"}, stderr)
	if err != nil {
		return AccessTokenView{}, err
	}
	return decodeAccessTokenView(response, time.Now())
}

func decodeAccessTokenView(response json.RawMessage, now time.Time) (AccessTokenView, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response, &raw); err != nil {
		return AccessTokenView{}, fmt.Errorf("decode OpenAI access-token view: %w", err)
	}
	for _, field := range []string{"refresh_token", "id_token"} {
		if _, present := raw[field]; present {
			return AccessTokenView{}, errors.New("OpenAI credential service returned a credential outside the access-token view")
		}
	}
	var wire struct {
		Token      string `json:"access_token"`
		ExpiresAt  int64  `json:"expires_at"`
		AccountID  string `json:"account_id"`
		Generation uint64 `json:"generation"`
	}
	if err := json.Unmarshal(response, &wire); err != nil {
		return AccessTokenView{}, fmt.Errorf("decode OpenAI access-token view: %w", err)
	}
	if wire.Token == "" || wire.ExpiresAt <= now.UnixMilli() || wire.Generation == 0 {
		return AccessTokenView{}, errors.New("OpenAI credential service returned an incomplete or expired access-token view")
	}
	return AccessTokenView{
		Token: wire.Token, ExpiresAt: time.UnixMilli(wire.ExpiresAt),
		AccountID: wire.AccountID, Generation: wire.Generation,
	}, nil
}
