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
// logout-loop fix. Python's urllib does not auto-decompress and the broker
// must parse token-endpoint responses; Go's http.Transport auto-decompresses
// gzip by default (DisableCompression=false + implicit Accept-Encoding), which
// would BOTH re-introduce the encoding and hide it, so we disable it AND strip
// accept-encoding on forwarded requests (see hopByHop). 30s timeout matches
// urlopen(timeout=30).
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DisableCompression: true,
	},
}

// userAgent identifies the broker; Python-urllib's default UA triggers
// Cloudflare error 1010 on platform.claude.com. Mirrors _broker_user_agent's
// versioned form; the version is injected by the caller (main stamps it).
var userAgent = "yolo-jail-oauth-broker"

// SetUserAgent lets the binary stamp the versioned UA (yolo-jail-oauth-broker/<ver>).
func SetUserAgent(ua string) { userAgent = ua }

// hopByHop is the header set stripped from forwarded requests/responses.
// content-length is recomputed; accept-encoding is stripped so upstream never
// returns a compressed body the mirror's decode would choke on. Byte-frozen
// against _HOP_BY_HOP.
var hopByHop = map[string]struct{}{
	"host": {}, "connection": {}, "keep-alive": {}, "proxy-authenticate": {},
	"proxy-authorization": {}, "te": {}, "trailer": {}, "transfer-encoding": {},
	"upgrade": {}, "content-length": {}, "accept-encoding": {},
}

// RefreshResult is either a success response or an error dict, both modeled as
// an OrderedMap so JSON encoding matches Python's session.json output.
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
// an OrderedMap. Mirrors _refresh_upstream. On an HTTP error status it returns
// an *httpError so the caller can shape {error: upstream_http, ...}.
func refreshUpstream(refreshToken string) (*jsonx.OrderedMap, error) {
	// Body built with encoding/json (server is not key-order-sensitive), same
	// as Python's json.dumps here.
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
		return nil, &urlError{msg: "unparseable upstream response: " + err.Error()}
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, &urlError{msg: "upstream response not a JSON object"}
	}
	return m, nil
}

// httpError models urllib.error.HTTPError (a non-2xx status with a body).
type httpError struct {
	code int
	body string
}

func (e *httpError) Error() string { return fmt.Sprintf("upstream HTTP %d", e.code) }

// urlError models urllib.error.URLError / OSError (transport failure).
type urlError struct{ msg string }

func (e *urlError) Error() string { return e.msg }

// ProxyResult is either {status, headers, body_b64} or {error, message}.
type ProxyResult = *jsonx.OrderedMap

// DoProxy forwards a request to the real upstream, returning the response
// shape verbatim (incl. 4xx/5xx) or an {error} dict on transport failure.
// Mirrors do_proxy, including the leading-slash path guard, hop-by-hop
// stripping both directions, and the "inject UA only if caller sent none".
func DoProxy(method, path string, headers map[string]string, body []byte) ProxyResult {
	if !strings.HasPrefix(path, "/") {
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

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return errResult("error", "upstream_unreachable", "message", err.Error())
	}
	req.Header = fwd

	resp, err := httpClient.Do(req)
	if err != nil {
		return errResult("error", "upstream_unreachable", "message", err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	respHeaders := jsonx.NewOrderedMap()
	for k := range resp.Header {
		if _, hop := hopByHop[strings.ToLower(k)]; hop {
			continue
		}
		respHeaders.Set(k, resp.Header.Get(k))
	}
	out := jsonx.NewOrderedMap()
	out.Set("status", jsonx.IntValue(int64(resp.StatusCode)))
	out.Set("headers", respHeaders)
	out.Set("body_b64", base64.StdEncoding.EncodeToString(respBody))
	return out
}

// pyReprPath renders a path the way Python's f"{path!r}" would in the bad_path
// message (do_proxy: f"path must start with '/': {path!r}").
func pyReprPath(s string) string {
	return pytext.Repr(s)
}
