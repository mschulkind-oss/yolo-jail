package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var brokerRefreshMarker = regexp.MustCompile(`^yolo-broker:[1-9][0-9]*$`)

type tokenView struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAtMS  int64  `json:"expires_at"`
	AccountID    string `json:"account_id,omitempty"`
	Generation   int64  `json:"generation"`
}

type codexAuth struct {
	AuthMode     string      `json:"auth_mode"`
	OpenAIAPIKey any         `json:"OPENAI_API_KEY"`
	Tokens       codexTokens `json:"tokens"`
	LastRefresh  string      `json:"last_refresh"`
}

type codexTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

// WriteCodexAuth converts a broker token view into Codex's native auth.json
// shape and atomically replaces path with a private file.
func WriteCodexAuth(path string, response json.RawMessage) error {
	if path == "" {
		return errors.New("codex auth path is required")
	}
	var view tokenView
	if err := json.Unmarshal(response, &view); err != nil {
		return fmt.Errorf("decode codex credential view: %w", err)
	}
	if view.AccessToken == "" || view.IDToken == "" || view.RefreshToken == "" || view.ExpiresAtMS <= 0 {
		return errors.New("OpenAI credential service returned an incomplete codex view")
	}
	if !brokerRefreshMarker.MatchString(view.RefreshToken) {
		return errors.New("OpenAI credential service returned a non-broker refresh credential")
	}
	auth := codexAuth{
		AuthMode:     "chatgpt",
		OpenAIAPIKey: nil,
		Tokens: codexTokens{
			IDToken: view.IDToken, AccessToken: view.AccessToken,
			RefreshToken: view.RefreshToken, AccountID: view.AccountID,
		},
		LastRefresh: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("encode codex auth view: %w", err)
	}
	data = append(data, '\n')
	return atomicWritePrivate(path, data)
}

func atomicWritePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create codex auth directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure codex auth directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".auth.json.*")
	if err != nil {
		return fmt.Errorf("create temporary codex auth view: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace codex auth view: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
