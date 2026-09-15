// Package openaiauth owns the provider-neutral transaction at the center of
// yolo's shared OpenAI subscription authentication. Agent-specific credential
// files are views of this canonical state; they are never refresh authorities.
package openaiauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const stateVersion = 1

// State is the broker's canonical OpenAI credential generation. Times are Unix
// milliseconds so the persisted representation does not depend on a locale or
// time-zone parser.
type State struct {
	Version       int    `json:"version"`
	AccessToken   string `json:"access_token"`
	IDToken       string `json:"id_token"`
	RefreshToken  string `json:"refresh_token"`
	ExpiresAtMS   int64  `json:"expires_at"`
	AccountID     string `json:"account_id,omitempty"`
	Generation    uint64 `json:"generation"`
	LoginRequired bool   `json:"login_required,omitempty"`
	LastRefreshMS int64  `json:"last_refresh,omitempty"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

func (s State) validate() error {
	if s.Version != stateVersion {
		return fmt.Errorf("unsupported OpenAI credential state version %d", s.Version)
	}
	if s.Generation == 0 {
		return errors.New("OpenAI credential generation must be positive")
	}
	if s.RefreshToken == "" {
		return errors.New("OpenAI credential state has no refresh token")
	}
	if s.AccessToken == "" {
		return errors.New("OpenAI credential state has no access token")
	}
	if s.IDToken == "" {
		return errors.New("OpenAI credential state has no ID token")
	}
	if s.ExpiresAtMS <= 0 {
		return errors.New("OpenAI credential state has no expiry")
	}
	return nil
}

func loadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode OpenAI credential state: %w", err)
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// ReadState returns a validated snapshot of canonical state. Callers that need
// a read followed by a decision must use Broker methods so the reload happens
// under the machine-wide lock.
func ReadState(path string) (State, error) { return loadState(path) }

// writeState atomically replaces the canonical state with a mode-0600 file in
// a mode-0700 directory. The directory rename means lock-free readers see the
// old complete generation or the new complete generation, never a torn file.
func writeState(path string, state State) error {
	if state.Version == 0 {
		state.Version = stateVersion
	}
	if err := state.validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create OpenAI credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure OpenAI credential directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OpenAI credential state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".openai-auth.*")
	if err != nil {
		return fmt.Errorf("create temporary OpenAI credential state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary OpenAI credential state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary OpenAI credential state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary OpenAI credential state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary OpenAI credential state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace OpenAI credential state: %w", err)
	}
	return nil
}

// TokenFingerprint is the non-reversible, stable token identifier used in
// diagnostics. Raw credentials must never be logged.
func TokenFingerprint(token string) string {
	if token == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}
