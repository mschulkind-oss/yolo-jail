package oauthbroker

import (
	"os"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// RefreshLockPath is the flock file serializing refreshes. Set by the daemon
// from the broker state dir; the SAME path as Python's REFRESH_LOCK so a Python
// and Go broker mutually exclude during rollout (kernel flock).
var RefreshLockPath string

// withRefreshLock runs fn while holding an exclusive flock on RefreshLockPath,
// mirroring Python's `with open(REFRESH_LOCK, "w") as lockf: flock(LOCK_EX)`.
func withRefreshLock(fn func() RefreshResult) RefreshResult {
	if RefreshLockPath == "" {
		return fn() // no lock configured (unit tests) — behave as if uncontended
	}
	if dir := dirOf(RefreshLockPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(RefreshLockPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		// Match Python: if we can't open the lock the refresh still proceeds
		// under the mkdir above having failed is unusual; return an error dict.
		return errResult("error", "creds_unreadable", "message", err.Error())
	}
	defer f.Close()
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// DoRefresh is the flock-serialized refresh of the shared credentials file.
// Returns either {access_token, refresh_token, expires_in, token_type} or
// {error, ...}. Mirrors do_refresh:
//   - cache hit (>= 90s headroom) -> return cached as an oauth response
//   - else read current oauth; missing/unreadable -> error dicts
//   - refresh upstream; classify HTTP vs transport errors
//   - normalize + write + return
func DoRefresh(credsPath string) RefreshResult {
	return withRefreshLock(func() RefreshResult {
		if cached := CachedTokens(credsPath); cached != nil {
			return AsOAuthResponse(cached)
		}
		current, ok := oauthFromCreds(credsPath)
		if !ok {
			// creds file unreadable / bad JSON.
			return errResult("error", "creds_unreadable", "message", "creds file unreadable")
		}
		refreshToken, _ := stringField(current, "refreshToken")
		if refreshToken == "" {
			return errResult("error", "no_refresh_token")
		}
		resp, err := refreshUpstream(refreshToken)
		if err != nil {
			switch e := err.(type) {
			case *httpError:
				body := e.body
				if len(body) > 200 {
					body = body[:200]
				}
				return errResult("error", "upstream_http", "status", jsonx.IntValue(int64(e.code)), "body", body)
			default:
				return errResult("error", "upstream_unreachable", "message", err.Error())
			}
		}
		newOAuth := NormalizeOAuth(resp, current)
		if err := WriteTokens(credsPath, newOAuth); err != nil {
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		return AsOAuthResponse(newOAuth)
	})
}

// RefreshDue reports whether the creds file's access token is within
// leadSeconds of expiry (or past it). False on any read/parse error or missing
// file — matches _refresh_due (a missing/unprimed broker is a no-op).
func RefreshDue(credsPath string, leadSeconds int, now int64) bool {
	if now == 0 {
		now = nowMS()
	}
	oauth, ok := oauthFromCreds(credsPath)
	if !ok {
		return false
	}
	v, ok := oauth.Get("expiresAt")
	if !ok {
		return false
	}
	expiresAtMS, ok := asInt64(v)
	if !ok {
		return false
	}
	return expiresAtMS-now < int64(leadSeconds)*1000
}

// BackgroundRefreshTick runs one iteration. Returns true iff the refresh
// failed TRANSIENTLY (upstream_unreachable) while still due — the loop uses
// this to fast-retry. Anything else (success, not due, non-transient error)
// returns false. Mirrors _background_refresh_tick.
func BackgroundRefreshTick(credsPath string, leadSeconds int) bool {
	if !RefreshDue(credsPath, leadSeconds, 0) {
		return false
	}
	result := DoRefresh(credsPath)
	if _, isErr := result.Get("error"); isErr {
		errVal, _ := result.Get("error")
		return errVal == "upstream_unreachable" && RefreshDue(credsPath, leadSeconds, 0)
	}
	return false
}

// RunBackgroundRefresher loops until stop is closed, ticking at tickSeconds and
// fast-retrying at fastRetrySeconds (up to maxFastRetries consecutive) on a
// transient-while-due failure. Mirrors _background_refresher_loop, including
// surviving a panicking tick. Runs as a goroutine started by the daemon.
func RunBackgroundRefresher(credsPath string, stop <-chan struct{}, tickSeconds, leadSeconds int) {
	fastRetries := 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		transient := func() (t bool) {
			defer func() { _ = recover() }() // loop must survive any tick error
			return BackgroundRefreshTick(credsPath, leadSeconds)
		}()
		var wait time.Duration
		if transient && fastRetries < BackgroundRefreshMaxFastRetries {
			fastRetries++
			wait = time.Duration(BackgroundRefreshFastRetrySeconds) * time.Second
		} else {
			fastRetries = 0
			wait = time.Duration(tickSeconds) * time.Second
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// stringField returns a string request/oauth field, or "" if absent/non-string.
func stringField(m *jsonx.OrderedMap, key string) (string, bool) {
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return ""
}
