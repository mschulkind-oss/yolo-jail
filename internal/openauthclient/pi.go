package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	piProviderID = "openai-codex"
	piLockStale  = 10 * time.Second
)

type piOAuthCredential struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId,omitempty"`
}

// WritePiAuth merges a broker token view into Pi's native auth.json. Pi uses a
// sibling auth.json.lock directory (proper-lockfile with realpath disabled), so
// taking the same lock keeps this prelaunch writer from racing Pi itself.
func WritePiAuth(path string, response json.RawMessage) error {
	if path == "" {
		return errors.New("pi auth path is required")
	}
	var view tokenView
	if err := json.Unmarshal(response, &view); err != nil {
		return fmt.Errorf("decode Pi credential view: %w", err)
	}
	if view.AccessToken == "" || view.ExpiresAtMS <= 0 || view.Generation <= 0 {
		return errors.New("OpenAI credential service returned an incomplete Pi view")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Pi auth directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure Pi auth directory: %w", err)
	}
	release, err := acquirePiAuthLock(path)
	if err != nil {
		return err
	}
	defer release()

	credentials := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &credentials); err != nil {
			return fmt.Errorf("decode existing Pi auth.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Pi auth.json: %w", err)
	}
	credential, err := json.Marshal(piOAuthCredential{
		Type: "oauth", Access: view.AccessToken,
		Refresh: fmt.Sprintf("yolo-broker:%d", view.Generation),
		Expires: view.ExpiresAtMS, AccountID: view.AccountID,
	})
	if err != nil {
		return fmt.Errorf("encode Pi credential view: %w", err)
	}
	credentials[piProviderID] = credential
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Pi auth.json: %w", err)
	}
	return atomicWritePrivate(path, append(data, '\n'))
}

func acquirePiAuthLock(authPath string) (func(), error) {
	lockPath := authPath + ".lock"
	for attempt := 0; attempt < 10; attempt++ {
		if err := os.Mkdir(lockPath, 0o700); err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("lock Pi auth.json: %w", err)
		}
		if info, err := os.Stat(lockPath); err == nil && time.Since(info.ModTime()) > piLockStale {
			_ = os.Remove(lockPath)
			continue
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.New("lock Pi auth.json: credential store is busy")
}
