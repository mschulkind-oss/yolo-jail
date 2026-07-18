// Package oauthbroker is the Go port of src/oauth_broker.py — the per-host
// Claude OAuth refresh daemon that serializes refreshes across jails so nobody
// burns the single-use refresh token.
//
// Everything here is a FROZEN byte/behavior contract (go-port plan Stage 6):
// the creds-file JSON shape (indent=2 via jsonx), the mkstemp+fchmod(0600)+
// os.replace atomic write, the 90s cache headroom, the 300s/60s/5s×12 refresher
// timing and transient-vs-permanent classification (only upstream_unreachable
// fast-retries), the action request/response shapes incl. error dicts, the
// User-Agent, and DisableCompression + accept-encoding strip both directions
// (the 2026-05-12 logout-loop fix — Go's transparent gzip would regress it).
//
// Cert generation keeps exec'ing openssl with the byte-identical --init-ca
// script; a crypto/x509 migration is a LATER flagged change, not part of this
// no-change port.
//
// Source of truth: src/oauth_broker.py. Port from that file.
package oauthbroker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Upstream OAuth endpoint constants (byte-frozen; extracted from the Claude
// Code binary). TokenURL is overridable via YOLO_BROKER_UPSTREAM_URL for
// black-box parity testing (the test-only override the plan mandates in BOTH
// impls) — Python gets the same env hook.
const (
	UpstreamHost    = "platform.claude.com"
	defaultTokenURL = "https://platform.claude.com/v1/oauth/token"
	ClientID        = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	OAuthBetaHeader = "oauth-2025-04-20"
)

// Background refresher cadence (seconds). Byte-frozen against the Python
// constants.
const (
	BackgroundRefreshLeadSeconds      = 300
	BackgroundRefreshTickSeconds      = 60
	BackgroundRefreshFastRetrySeconds = 5
	BackgroundRefreshMaxFastRetries   = 12
)

// tokenURL returns the upstream token endpoint, honoring the test-only
// override env var (must match the Python broker's override).
func tokenURL() string {
	if v := os.Getenv("YOLO_BROKER_UPSTREAM_URL"); v != "" {
		return v
	}
	return defaultTokenURL
}

// TokenFP is the stable 8-hex-char sha256-prefix fingerprint used in logs
// (non-reversible; safe to emit). Mirrors _token_fp.
func TokenFP(tok string) string {
	if tok == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])[:8]
}

// nowFunc returns the current unix time in milliseconds (Python
// int(time.time()*1000)). A package var so tests can pin it deterministically.
var nowFunc = func() int64 { return time.Now().UnixMilli() }

func nowMS() int64 { return nowFunc() }

// oauthFromCreds reads the creds file and returns the claudeAiOauth object as
// an OrderedMap, or (nil, false) on any read/parse error or missing section.
func oauthFromCreds(credsPath string) (*jsonx.OrderedMap, bool) {
	data, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, false
	}
	decoded, err := jsonx.Decode(data)
	if err != nil {
		return nil, false
	}
	root, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	v, ok := root.Get("claudeAiOauth")
	if !ok {
		return nil, false
	}
	oauth, ok := v.(*jsonx.OrderedMap)
	if !ok {
		// Python `data.get("claudeAiOauth") or {}` treats a non-object as {}.
		return jsonx.NewOrderedMap(), true
	}
	return oauth, true
}

// asInt64 extracts an integer-ish value from a decoded jsonx value. jsonx
// decodes integers as an internal type re-encoded via DumpsCompact; here we
// need the numeric value, so we re-encode + parse. Handles float64 too.
func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	default:
		// jsonx integer literal: re-encode to its literal string and parse.
		s, err := jsonx.DumpsCompact(v)
		if err != nil {
			return 0, false
		}
		s = strings.TrimSpace(s)
		var n int64
		if _, err := fmt.Sscan(s, &n); err != nil {
			return 0, false
		}
		return n, true
	}
}

// CachedTokens returns the on-disk oauth object iff the access token has >= 90s
// headroom, else nil. Mirrors _cached_tokens.
func CachedTokens(credsPath string) *jsonx.OrderedMap {
	oauth, ok := oauthFromCreds(credsPath)
	if !ok {
		return nil
	}
	var expiresAtMS int64
	if v, ok := oauth.Get("expiresAt"); ok {
		expiresAtMS, _ = asInt64(v)
	}
	if expiresAtMS-nowMS() < 90_000 {
		return nil
	}
	return oauth
}

// AsOAuthResponse shapes on-disk tokens back into an upstream-style response
// body: {access_token, refresh_token, expires_in, token_type}. Mirrors
// _as_oauth_response (expires_in floored at 0; integer-divided by 1000).
func AsOAuthResponse(oauth *jsonx.OrderedMap) *jsonx.OrderedMap {
	var expiresAtMS int64
	if v, ok := oauth.Get("expiresAt"); ok {
		expiresAtMS, _ = asInt64(v)
	}
	expiresIn := (expiresAtMS - nowMS()) / 1000
	if expiresIn < 0 {
		expiresIn = 0
	}
	out := jsonx.NewOrderedMap()
	out.Set("access_token", getOrNil(oauth, "accessToken"))
	out.Set("refresh_token", getOrNil(oauth, "refreshToken"))
	out.Set("expires_in", jsonx.IntValue(expiresIn))
	out.Set("token_type", "Bearer")
	return out
}

func getOrNil(m *jsonx.OrderedMap, key string) any {
	if v, ok := m.Get(key); ok {
		return v
	}
	return nil
}

// NormalizeOAuth converts an upstream {access_token, refresh_token,
// expires_in, scope} response to the Claude-Code on-disk shape, preserving
// fields from previous. Mirrors _normalize_oauth exactly, including:
//   - out = dict(previous) then override accessToken/expiresAt
//   - refreshToken only overridden if present in the response
//   - scopes only synthesized from response `scope` when absent in previous
//     (and never an empty list)
func NormalizeOAuth(upstream, previous *jsonx.OrderedMap) *jsonx.OrderedMap {
	expiresIn := int64(3600)
	if v, ok := upstream.Get("expires_in"); ok {
		if n, ok := asInt64(v); ok {
			expiresIn = n
		}
	}
	// out = dict(previous) — copy preserving key order.
	out := jsonx.NewOrderedMap()
	for _, k := range previous.Keys() {
		v, _ := previous.Get(k)
		out.Set(k, v)
	}
	if at, ok := upstream.Get("access_token"); ok {
		out.Set("accessToken", at)
	}
	if rt, ok := upstream.Get("refresh_token"); ok {
		out.Set("refreshToken", rt)
	}
	out.Set("expiresAt", jsonx.IntValue(nowMS()+expiresIn*1000))
	if _, has := out.Get("scopes"); !has {
		scopeStr := ""
		if v, ok := upstream.Get("scope"); ok {
			if s, ok := v.(string); ok {
				scopeStr = s
			}
		}
		fields := strings.Fields(scopeStr)
		if len(fields) > 0 {
			arr := make([]any, len(fields))
			for i, f := range fields {
				arr[i] = f
			}
			out.Set("scopes", arr)
		}
	}
	return out
}

// WriteTokens atomically writes the shared credentials file via
// mkstemp+fchmod(0600)+rename, matching _write_tokens. The blob is
// json.dumps({"claudeAiOauth": oauth}, indent=2) — jsonx snapshot form WITHOUT
// sort_keys (Python's _write_tokens does NOT pass sort_keys), so key order is
// the insertion order of oauth. NOTE: unlike bind-mounted single files, this
// creds file lives in a DIRECTORY-mounted dir, so tmp+rename IS correct here
// (in-jail readers take no flock; an in-place O_TRUNC would expose a torn file).
func WriteTokens(credsPath string, oauth *jsonx.OrderedMap) error {
	root := jsonx.NewOrderedMap()
	root.Set("claudeAiOauth", oauth)
	blob, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		return err
	}
	dir := filepath.Dir(credsPath)
	tmp, err := os.CreateTemp(dir, filepath.Base(credsPath)+".tmp.")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.WriteString(blob); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, credsPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
