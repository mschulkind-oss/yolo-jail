package oauthbroker

import (
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
)

// RefreshLockPath is the flock file serializing refreshes. Set by the daemon
// from the broker state dir. Frozen contract (must not drift — the exact path
// is the kernel-flock rendezvous every broker instance must agree on).
var RefreshLockPath string

// withRefreshLock runs fn while holding an exclusive flock on RefreshLockPath.
func withRefreshLock(fn func() RefreshResult) RefreshResult {
	if RefreshLockPath == "" {
		return fn() // no lock configured (unit tests) — behave as if uncontended
	}
	// NO MkdirAll of the lock's directory, and there used to be one. The daemon's state
	// dir is created at startup (EnsureCAAndLeaf) and the daemon exits when it goes
	// (hostservice.WatchStateDir, wired in Main); recreating it here, on the first refresh
	// after a retirement moved it away, is exactly what that exit exists to prevent.
	f, err := os.OpenFile(RefreshLockPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		// Can't open the lock — its directory is gone, and the daemon is on its way out;
		// return an error dict rather than proceeding unlocked.
		return errResult("error", "creds_unreadable", "message", err.Error())
	}
	defer f.Close()
	// The flock is the load-bearing single-use-refresh-token serialization
	// contract. We must NOT silently proceed unlocked (that would let
	// concurrent jails burn the token). Treat a Flock failure as a hard error.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return errResult("error", "creds_unreadable", "message", "refresh lock failed: "+err.Error())
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// DoRefresh is the flock-serialized refresh of the shared credentials file.
// Returns either {access_token, refresh_token, expires_in, token_type} or
// {error, ...}.
//   - cache hit (>= 90s headroom) -> return cached as an oauth response
//   - else read current oauth; missing/unreadable -> error dicts
//   - refresh upstream; classify HTTP vs transport errors
//   - normalize + write + return
func DoRefresh(credsPath string) RefreshResult {
	// Pre-lock snapshot — logged regardless of what we end up doing, so
	// tomorrow's debugger can reconstruct the state the broker saw when it was
	// asked to refresh (the 2026-04-23 shared-identity drift was invisible for
	// want of exactly this line).
	logInfo("do_refresh: shared=%s", describeCreds(credsPath))
	return withRefreshLock(func() RefreshResult { return doRefreshLocked(credsPath, cachedForRefresh) })
}

// doBackgroundRefresh is the background refresher's refresh: DoRefresh with the cache floor
// raised to the refresher's own lead.
//
// WHY A SECOND FLOOR. DoRefresh answers from the cache above RefreshCacheFloorMS (six minutes),
// which is right for an interception jail's Claude asking at five. The background lead is
// thirty minutes now (CL-D5), so a tick that found the login due and then called DoRefresh
// would be answered from the cache and refresh nothing until six minutes were left. Raising
// the floor to the lead makes "due" and "refreshed" the same decision, and it is still taken
// inside the lock: a second broker that refreshed meanwhile is seen, and nothing is spent twice.
func doBackgroundRefresh(credsPath string, leadSeconds int) RefreshResult {
	logInfo("do_refresh: shared=%s (background, lead %ds)", describeCreds(credsPath), leadSeconds)
	floor := int64(leadSeconds) * 1000
	if floor < RefreshCacheFloorMS {
		floor = RefreshCacheFloorMS
	}
	return withRefreshLock(func() RefreshResult {
		return doRefreshLocked(credsPath, func(p string) *jsonx.OrderedMap { return cachedAbove(p, floor) })
	})
}

// DoRefreshAsCaller is DoRefresh for a jail terminator's refresh, which must first prove that
// its caller holds the machine's Claude login (docs/plans/notch-convergence.md §2.3, NC-D3).
//
// WHY. The terminator listens on 127.0.0.1:443, and a loopback port is reachable by every
// process sharing that loopback: a jail on `network.mode: host` shares the host's, a nested
// podman forced onto `--net=host` shares its parent jail's. Before this, any such process that
// POSTed a refresh grant got the machine-wide access AND refresh tokens, and could redeem the
// single-use refresh token to break every jail's login. Claude cannot be told to send a secret
// (it speaks TLS to a vendor hostname), but it already sends one: the refresh token it re-reads
// from ~/.claude/.credentials.json, which in a jail is a link to the shared file, immediately
// before the POST (docs/research/claude-oauth-refresh-mechanics.md §3.4). So the check is that
// the presented token is the shared file's current one, or one this broker replaced moments ago
// (the refresh race: another jail, or the background tick, rotated it between Claude's read and
// its POST). A caller who cannot read the file cannot present either.
//
// P1 HOLDS: the presented token is compared and never spent. The refresh is still made from
// the shared file under the flock, so a stale or burnt token a caller holds is not an input to
// anything upstream.
//
// The comparison runs under the same flock as the refresh, so the file it compares against is
// the one the refresh then reads. A mismatch is `caller_unauthenticated`, never `invalid_grant`,
// which would make Claude blank the shared file (P2).
func DoRefreshAsCaller(credsPath, presented string) RefreshResult {
	logInfo("do_refresh: shared=%s caller_rt=%s", describeCreds(credsPath), TokenFP(presented))
	return withRefreshLock(func() RefreshResult {
		current, err := storeFor(credsPath).loadCanonicalLocked()
		if err != nil {
			logError("creds file unreadable: %s", err)
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		currentRT, _ := stringField(current, "refreshToken")
		if !callerPresentsLogin(currentRT, presented) {
			logWarn("refresh refused: the caller presented rt=%s, which is neither the shared "+
				"file's current refresh token (rt=%s) nor one this broker just replaced",
				TokenFP(presented), TokenFP(currentRT))
			return errResult("error", CallerUnauthenticated, "message",
				"yolo claude-oauth-broker: refused — this refresh did not present the machine's "+
					"current Claude login, so it did not come from a Claude reading the shared "+
					"credentials. The terminator serves only this machine's jails' Claude; a "+
					"loopback shared with other processes makes it reachable from outside a jail "+
					"(docs/plans/notch-convergence.md §2.3)")
		}
		return doRefreshLocked(credsPath, cachedForRefresh)
	})
}

// CallerUnauthenticated is the broker's error for a refresh whose caller did not present the
// login (DoRefreshAsCaller). The terminator answers it 401; it is deliberately not
// `invalid_grant`, which Claude would answer by blanking the shared credentials file.
const CallerUnauthenticated = oauthterminator.CallerUnauthenticated

// callerPresentsLogin reports whether presented is the shared file's current refresh token or
// one of the few this broker replaced most recently, compared in constant time. An empty
// presented token never matches, and neither does anything when the file holds none.
func callerPresentsLogin(current, presented string) bool {
	if presented == "" {
		return false
	}
	if current != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(current)) == 1 {
		return true
	}
	return recentlyReplaced.has(presented)
}

// replacedTokens remembers the SHA-256 of the last few refresh tokens this broker replaced
// (never the tokens themselves), so a Claude that read the file just before another refresh
// rotated it is still recognized. Bounded, because a Claude reads the file immediately before
// it POSTs, so only a token replaced in that window can be presented honestly; in memory,
// because the window is seconds and a restarted broker has no such window open.
type replacedTokens struct {
	mu   sync.Mutex
	sums [][sha256.Size]byte
}

// replacedTokenMemory is how many replaced refresh tokens are remembered.
const replacedTokenMemory = 4

var recentlyReplaced = &replacedTokens{}

func (r *replacedTokens) record(token string) {
	if token == "" {
		return
	}
	sum := sha256.Sum256([]byte(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sums = append(r.sums, sum)
	if len(r.sums) > replacedTokenMemory {
		r.sums = r.sums[len(r.sums)-replacedTokenMemory:]
	}
}

func (r *replacedTokens) has(token string) bool {
	sum := sha256.Sum256([]byte(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for _, s := range r.sums {
		if subtle.ConstantTimeCompare(s[:], sum[:]) == 1 {
			found = true
		}
	}
	return found
}

// doRefreshLocked is DoRefresh's body, run under the refresh flock by both entry points.
//
// It reads and writes the STORE (store.go): the canonical login, synced from the legacy shared
// file first, and on success the canonical, the legacy file and every registered view. In
// single-file mode that is the one file it always was.
func doRefreshLocked(credsPath string, cached func(string) *jsonx.OrderedMap) RefreshResult {
	s := storeFor(credsPath)
	s.syncLocked()
	credsPath = s.canonical
	{
		// cachedForRefresh (or the background refresher's higher floor), NOT CachedTokens:
		// this is the refresh path, whose floor must exceed the requesting agent's own
		// due-threshold.
		if cached := cached(credsPath); cached != nil {
			logInfo("cache hit: at=%s rt=%s exp=%s",
				fpOf(cached, "accessToken"), fpOf(cached, "refreshToken"), expiresAtStr(cached))
			return AsOAuthResponse(cached)
		}
		current, err := oauthFromCreds(credsPath)
		if err != nil && s.split() && os.IsNotExist(err) {
			// A split store with no canonical is a signed-out machine, not a broken file.
			current, err = jsonx.NewOrderedMap(), nil
		}
		if err != nil {
			// creds file unreadable / bad JSON — thread the real error text
			// (e.g. "[Errno 2] ...", "Expecting value: ...") into the reply.
			logError("creds file unreadable: %s", err)
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		// A readable file with a MISSING claudeAiOauth key (e.g. "{}") yields
		// an empty object here and falls through to no_refresh_token (empty
		// claudeAiOauth, then a missing refreshToken).
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
				// A malformed 200 body is NOT an upstream_unreachable dict.
				// Surface a distinct error that the bg tick does NOT fast-retry
				// (the fast-retry check is on upstream_unreachable only);
				// reusing creds_unreadable would be wrong, so use a dedicated
				// code. Log it — a 200 with a garbage body is a forensic event a
				// soak must not lose.
				logError("upstream bad response for rt=%s: %s", TokenFP(refreshToken), e.msg)
				return errResult("error", "upstream_bad_response", "message", e.msg)
			default:
				logError("upstream network error: %s", err)
				return errResult("error", "upstream_unreachable", "message", err.Error())
			}
		}
		newOAuth := NormalizeOAuth(resp, current, refreshToken)
		if err := s.saveLocked(newOAuth); err != nil {
			// Log a failed shared-creds write — it silently strands every jail
			// on the stale token.
			logError("creds write failed: %s", err)
			return errResult("error", "creds_unreadable", "message", err.Error())
		}
		// The token just replaced stays presentable for the refresh race
		// (DoRefreshAsCaller): a Claude that read the file a moment ago holds it.
		recentlyReplaced.record(refreshToken)
		logInfo("refreshed: rt %s -> %s, at -> %s, exp=%s",
			TokenFP(refreshToken), fpOf(newOAuth, "refreshToken"),
			fpOf(newOAuth, "accessToken"), expiresAtStr(newOAuth))
		return AsOAuthResponse(newOAuth)
	}
}

// RefreshDue reports whether the creds file's access token is within
// leadSeconds of expiry (or past it). False on any read/parse error or missing
// file (a missing/unprimed broker is a no-op).
func RefreshDue(credsPath string, leadSeconds int, now int64) bool {
	if now == 0 {
		now = nowMS()
	}
	oauth, err := oauthFromCreds(storeFor(credsPath).readPath())
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
// returns false.
//
// Each tick first keeps the store in step (store.go, views.go): the canonical synced from the
// legacy shared file, then every registered credential view kept current, a /login in a view
// adopted and a /logout honored. That runs whether or not a refresh is due, because it is how
// a jail's /login reaches the machine within a tick.
func BackgroundRefreshTick(credsPath string, leadSeconds int) bool {
	if s := storeFor(credsPath); s.split() || ViewRegistryDir != "" {
		withRefreshLock(func() RefreshResult {
			s.syncLocked()
			s.maintainViewsLocked()
			return nil
		})
	}
	if !RefreshDue(credsPath, leadSeconds, 0) {
		// DEBUG because most ticks skip; logging skips at DEBUG keeps the log
		// from becoming a wall of "not due" lines under normal INFO operation.
		logDebug("bg_refresh: skip (not due) shared=%s", describeCreds(credsPath))
		return false
	}
	logInfo("bg_refresh: due (within %ds of expiry) shared=%s", leadSeconds, describeCreds(credsPath))
	result := doBackgroundRefresh(credsPath, leadSeconds)
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
// transient-while-due failure.
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
					// loop must survive any tick error.
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
