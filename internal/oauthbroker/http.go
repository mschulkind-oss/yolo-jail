package oauthbroker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// httpClient is the shared client with compression DISABLED — the 2026-05-12
// logout-loop fix. The broker must parse token-endpoint responses; Go's
// http.Transport auto-decompresses gzip by default (DisableCompression=false +
// implicit Accept-Encoding), which would BOTH re-introduce the encoding and
// hide it, so we disable it AND strip accept-encoding on forwarded requests
// (see hopByHop). 30s request timeout.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DisableCompression: true,
	},
}

// userAgent identifies the broker; a generic client UA triggers Cloudflare
// error 1010 on platform.claude.com, so a distinct UA is required. The
// versioned form is injected by the caller (main stamps it).
var userAgent = "yolo-jail-oauth-broker"

// SetUserAgent lets the binary stamp the versioned UA (yolo-jail-oauth-broker/<ver>).
func SetUserAgent(ua string) { userAgent = ua }

// hopByHop is the header set stripped from forwarded requests/responses.
// content-length is recomputed; accept-encoding is stripped so upstream never
// returns a compressed body the mirror's decode would choke on.
var hopByHop = map[string]struct{}{
	"host": {}, "connection": {}, "keep-alive": {}, "proxy-authenticate": {},
	"proxy-authorization": {}, "te": {}, "trailer": {}, "transfer-encoding": {},
	"upgrade": {}, "content-length": {}, "accept-encoding": {},
}

// RefreshResult is either a success response or an error dict, both modeled as
// an OrderedMap so JSON encoding preserves field order in the session.json output.
type RefreshResult = *jsonx.OrderedMap

// errResult builds an {"error": ...} OrderedMap with extra ordered fields.
func errResult(pairs ...any) RefreshResult {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// refreshUpstream POSTs the refresh grant and returns the parsed JSON body as
// an OrderedMap. On an HTTP error status it returns an *httpError so the
// caller can shape {error: upstream_http, ...}.
func refreshUpstream(refreshToken string) (*jsonx.OrderedMap, error) {
	// Body built with encoding/json (server is not key-order-sensitive).
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     ClientID,
	})
	req, err := http.NewRequest(http.MethodPost, tokenURL(), strings.NewReader(string(body)))
	if err != nil {
		return nil, &urlError{msg: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", OAuthBetaHeader)
	req.Header.Set("User-Agent", userAgent)
	// Send "Accept-Encoding: identity" to actively FORBID a compressed response
	// (the 2026-05-12 logout-loop fix). DisableCompression only avoids
	// REQUESTING gzip; without an explicit identity, upstream/Cloudflare may
	// still choose an encoding we can't decode.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &urlError{msg: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &httpError{code: resp.StatusCode, body: string(respBody)}
	}
	decoded, err := jsonx.Decode(respBody)
	if err != nil {
		// A 200 with an unparseable body must NOT be treated as a transient
		// transport failure. Use parseError so DoRefresh does NOT map it to
		// upstream_unreachable (which would wrongly fast-retry 12×5s) and the
		// bg tick's catch-all stays on the NORMAL cadence.
		return nil, &parseError{msg: "unparseable upstream response: " + err.Error()}
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, &parseError{msg: "upstream response not a JSON object"}
	}
	// access_token is a REQUIRED key. Reject a 200 lacking it (loud, no write)
	// so DoRefresh never silently writes a stale token with a fresh expiresAt.
	if _, ok := m.Get("access_token"); !ok {
		return nil, &parseError{msg: "upstream 200 response missing access_token"}
	}
	return m, nil
}

// httpError models a non-2xx status with a body.
type httpError struct {
	code int
	body string
}

func (e *httpError) Error() string { return fmt.Sprintf("upstream HTTP %d", e.code) }

// parseError models a 200 with a malformed/insufficient body. It is NOT
// classified transient/fast-retried.
type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

// urlError models a transport failure.
type urlError struct{ msg string }

func (e *urlError) Error() string { return e.msg }

// ProxyResult is either {status, headers, body_b64} or {error, message}.
type ProxyResult = *jsonx.OrderedMap

// DoProxy forwards a request to the real upstream, returning the response
// shape verbatim (incl. 4xx/5xx) or an {error} dict on transport failure.
// stripping both directions, and the "inject UA only if caller sent none".
func DoProxy(method, path string, headers map[string]string, body []byte) ProxyResult {
	if !strings.HasPrefix(path, "/") {
		logWarn("do_proxy rejected: bad path %s", pyReprPath(path))
		return errResult("error", "bad_path", "message", "path must start with '/': "+pyReprPath(path))
	}
	url := "https://" + UpstreamHost + path
	fwd := http.Header{}
	hasUA := false
	for k, v := range headers {
		if _, hop := hopByHop[strings.ToLower(k)]; hop {
			continue
		}
		fwd.Set(k, v)
		if strings.EqualFold(k, "user-agent") {
			hasUA = true
		}
	}
	if !hasUA {
		fwd.Set("User-Agent", userAgent)
	}

	fwdUA := fwd.Get("User-Agent")
	if fwdUA == "" {
		fwdUA = "(none)"
	}
	logInfo("do_proxy -> %s %s body_len=%d ua=%s", method, path, len(body), pytext.Repr(fwdUA))

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		logError("proxy upstream error for %s %s: %s", method, path, err)
		return errResult("error", "upstream_unreachable", "message", err.Error())
	}
	req.Header = fwd
	// Send "Accept-Encoding: identity" (forbid a compressed response the mirror
	// can't decode) — see refreshUpstream.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := httpClient.Do(req)
	if err != nil {
		logError("proxy upstream error for %s %s: %s", method, path, err)
		return errResult("error", "upstream_unreachable", "message", err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	respHeaders := jsonx.NewOrderedMap()
	for k, vals := range resp.Header {
		if _, hop := hopByHop[strings.ToLower(k)]; hop {
			continue
		}
		// Duplicate headers (multiple Set-Cookie) collapse to the LAST value.
		// Go's resp.Header[k] is []string in receive order — take the last.
		// (Header-NAME canonicalization by net/http is an accepted residue,
		// ledgered D5: the jail-side terminator re-canonicalizes anyway.)
		respHeaders.Set(k, vals[len(vals)-1])
	}
	logInfo("do_proxy <- %s %s status=%d body_len=%d", method, path, resp.StatusCode, len(respBody))
	out := jsonx.NewOrderedMap()
	out.Set("status", jsonx.IntValue(int64(resp.StatusCode)))
	out.Set("headers", respHeaders)
	out.Set("body_b64", base64.StdEncoding.EncodeToString(respBody))
	return out
}

// pyReprPath renders a path as a Python-style repr for the bad_path message
// ("path must start with '/': <repr>").
func pyReprPath(s string) string {
	return pytext.Repr(s)
}
