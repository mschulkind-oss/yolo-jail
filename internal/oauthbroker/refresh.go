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
	// The flock is the load-bearing single-use-refresh-token serialization
	// contract. Python's fcntl.flock RAISES on failure; we must NOT silently
	// proceed unlocked (that would let concurrent jails burn the token). Treat
	// a Flock failure as a hard error.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return errResult("error", "creds_unreadable", "message", "refresh lock failed: "+err.Error())
	}
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
	// Pre-lock snapshot — logged regardless of what we end up doing, so
	// tomorrow's debugger can reconstruct the state the broker saw when it was
	// asked to refresh (the 2026-04-23 shared-identity drift was invisible for
	// want of exactly this line). Mirrors do_refresh's first log.info.
	logInfo("do_refresh: shared=%s", describeCreds(credsPath))
	return withRefreshLock(func() RefreshResult {
		if cached := CachedTokens(credsPath); cached != nil {
			logInfo("cache hit: at=%s rt=%s exp=%s",
				fpOf(cached, "accessToken"), fpOf(cached, "refreshToken"), expiresAtStr(cached))
			return AsOAuthResponse(cached)
		}
		current, err := oauthFromCreds(credsPath)
		if err != nil {
			// creds file unreadable / bad JSON — thread the real error text
			// (Python emits str(e): "[Errno 2] ...", "Expecting value: ...").
			logError("creds file unreadable: %s", err)
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		// A readable file with a MISSING claudeAiOauth key (e.g. "{}") yields
		// an empty object here and falls through to no_refresh_token — matching
		// Python's `.get("claudeAiOauth") or {}` then missing refreshToken.
		refreshToken, _ := stringField(current, "refreshToken")
		if refreshToken == "" {
			logError("no_refresh_token: shared creds missing refreshToken")
			return errResult("error", "no_refresh_token")
		}
		logInfo("cache miss: refreshing upstream with rt=%s (old_exp=%s)",
			TokenFP(refreshToken), expiresAtStr(current))
		resp, err := refreshUpstream(refreshToken)
		if err != nil {
			switch e := err.(type) {
			case *httpError:
				body := e.body
				if len(body) > 200 {
					body = body[:200]
				}
				// Names invalid_grant (2026-04-23) and any 4xx/5xx with the
				// refresh-token fingerprint so a soak can correlate the failing
				// token across processes.
				logError("upstream %d for rt=%s: %s", e.code, TokenFP(refreshToken), body)
				return errResult("error", "upstream_http", "status", jsonx.IntValue(int64(e.code)), "body", body)
			case *parseError:
				// Python raises JSONDecodeError/KeyError here — NOT an
				// upstream_unreachable dict. Surface a distinct error that the
				// bg tick does NOT fast-retry (RefreshDue check on
				// upstream_unreachable only). Byte-shape: reuse creds_unreadable
				// is wrong; use a dedicated code. Python's exception propagates
				// unlogged from do_refresh; we log it (a 200 with a garbage body
				// is a forensic event a soak must not lose).
				logError("upstream bad response for rt=%s: %s", TokenFP(refreshToken), e.msg)
				return errResult("error", "upstream_bad_response", "message", e.msg)
			default:
				logError("upstream network error: %s", err)
				return errResult("error", "upstream_unreachable", "message", err.Error())
			}
		}
		newOAuth := NormalizeOAuth(resp, current)
		if err := WriteTokens(credsPath, newOAuth); err != nil {
			// Python's _write_tokens raises OSError, propagating unlogged; the Go
			// port returns an error dict, so log it — a failed shared-creds write
			// silently strands every jail on the stale token.
			logError("creds write failed: %s", err)
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		logInfo("refreshed: rt %s -> %s, at -> %s, exp=%s",
			TokenFP(refreshToken), fpOf(newOAuth, "refreshToken"),
			fpOf(newOAuth, "accessToken"), expiresAtStr(newOAuth))
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
	oauth, err := oauthFromCreds(credsPath)
	if err != nil {
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
		// DEBUG because most ticks skip (Python logs skips at DEBUG so the log
		// isn't a wall of "not due" lines under normal INFO operation).
		logDebug("bg_refresh: skip (not due) shared=%s", describeCreds(credsPath))
		return false
	}
	logInfo("bg_refresh: due (within %ds of expiry) shared=%s", leadSeconds, describeCreds(credsPath))
	result := DoRefresh(credsPath)
	if _, isErr := result.Get("error"); isErr {
		errVal, _ := result.Get("error")
		msg := ""
		if m, ok := result.Get("message"); ok {
			msg = stringOf(m)
		} else if b, ok := result.Get("body"); ok {
			msg = stringOf(b)
		}
		logWarn("bg_refresh: refresh failed error=%s message=%s", stringOf(errVal), msg)
		return errVal == "upstream_unreachable" && RefreshDue(credsPath, leadSeconds, 0)
	}
	expiresIn := ""
	if v, ok := result.Get("expires_in"); ok {
		expiresIn = stringOf(v)
	}
	logInfo("bg_refresh: ok expires_in=%s shared=%s", expiresIn, describeCreds(credsPath))
	return false
}

// RunBackgroundRefresher loops until stop is closed, ticking at tickSeconds and
// fast-retrying at fastRetrySeconds (up to maxFastRetries consecutive) on a
// transient-while-due failure. Mirrors _background_refresher_loop, including
// surviving a panicking tick. Runs as a goroutine started by the daemon.
func RunBackgroundRefresher(credsPath string, stop <-chan struct{}, tickSeconds, leadSeconds int) {
	logInfo("bg_refresh: started (tick=%ds, lead=%ds, creds=%s)", tickSeconds, leadSeconds, credsPath)
	defer logInfo("bg_refresh: stopped")
	fastRetries := 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		transient := func() (t bool) {
			defer func() {
				if r := recover(); r != nil {
					// loop must survive any tick error (Python's
					// `except Exception ... log.error(..., exc_info=True)`).
					logError("bg_refresh: tick crashed: %v", r)
				}
			}()
			return BackgroundRefreshTick(credsPath, leadSeconds)
		}()
		var wait time.Duration
		if transient && fastRetries < BackgroundRefreshMaxFastRetries {
			fastRetries++
			logInfo("bg_refresh: transient failure while due — fast retry %d/%d in %ds",
				fastRetries, BackgroundRefreshMaxFastRetries, BackgroundRefreshFastRetrySeconds)
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
