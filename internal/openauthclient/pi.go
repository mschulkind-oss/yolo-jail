package openauthclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const piProviderID = "openai-codex"

type piOAuthCredential struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId,omitempty"`
}

// WritePiAuth merges a broker token view into Pi's native auth.json. Pi uses a
// sibling auth.json.lock directory (proper-lockfile with realpath disabled), so
// taking the same lock, and judging it stale by pi's own rule (piAuthLockRule),
// keeps this prelaunch writer from racing Pi itself.
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
	release, err := acquirePiAuthLock(path, piAuthLockRule)
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

// piLockRule sets two limits for a pi auth.json.lock. stale is how long the lock may go without an
// mtime refresh before it counts as abandoned. wait is how long yolo waits for a live holder.
type piLockRule struct {
	stale time.Duration
	wait  time.Duration
}

// piAuthLockRule is pi's own rule. pi takes auth.json.lock through proper-lockfile 4.1.2. That
// library calls a lock stale once its mtime is more than `stale` old, and a live holder refreshes
// the mtime every stale/2. pi's async holder (token refresh and login) passes `stale: 30000`
// (acquireLockAsync in dist/core/auth-storage.js, pi 0.87.1). Its sync holder uses the library's
// 10 s default and holds only for one read and one write. So a live lock can be up to 30 s old, and
// yolo breaks nothing younger. The wait runs 2 s past the stale window, because proper-lockfile
// may round an mtime up to the next second. A lock that was already abandoned when yolo started is
// then reclaimed before yolo gives up.
var piAuthLockRule = piLockRule{stale: 30 * time.Second, wait: 32 * time.Second}

// PiAuthLockBusyError is WritePiAuth's failure when a running pi still held auth.json.lock at
// the end of the wait. It is its own type so the client can tell it from a missing login and
// exit ExitPiAuthLockBusy: pi holds that lock while it refreshes its own token.
type PiAuthLockBusyError struct {
	LockPath     string
	SinceRefresh time.Duration
	Rule         piLockRule
}

func (e *PiAuthLockBusyError) Error() string {
	return fmt.Sprintf("lock Pi auth.json: %s is held by a running pi (last refreshed %s ago); "+
		"gave up after %s, since pi counts a lock as abandoned only after %s without a refresh. "+
		"Let pi finish and try again",
		e.LockPath, e.SinceRefresh.Round(time.Second), e.Rule.wait, e.Rule.stale)
}

func acquirePiAuthLock(authPath string, rule piLockRule) (func(), error) {
	lockPath := authPath + ".lock"
	release := func() { _ = os.Remove(lockPath) }
	tryLock := func() (bool, error) {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return true, nil
		}
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("lock Pi auth.json: %w", err)
	}
	deadline := time.Now().Add(rule.wait)
	delay := 10 * time.Millisecond
	for {
		if ok, err := tryLock(); ok || err != nil {
			return release, err
		}
		info, err := os.Stat(lockPath)
		if os.IsNotExist(err) {
			continue // released between the mkdir and the stat
		}
		if err != nil {
			return nil, fmt.Errorf("lock Pi auth.json: %w", err)
		}
		sinceRefresh := time.Since(info.ModTime())
		if sinceRefresh > rule.stale {
			// Abandoned by pi's own rule. Remove it as proper-lockfile does (rmdir, so only an
			// empty directory goes), then retry. If another writer wins the retry, wait for it.
			if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("lock Pi auth.json: remove abandoned lock: %w", err)
			}
			if ok, err := tryLock(); ok || err != nil {
				return release, err
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, &PiAuthLockBusyError{LockPath: lockPath, SinceRefresh: sinceRefresh, Rule: rule}
		}
		time.Sleep(min(delay, remaining))
		delay = min(2*delay, 500*time.Millisecond)
	}
}
