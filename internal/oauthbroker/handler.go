package oauthbroker

import (
	"encoding/base64"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// BuildHandler returns the hostservice.Handler for the broker, dispatching on
// the request "action" field.
//
//	refresh (default) -> DoRefresh
//	cached -> CachedTokens or {error: no_cached_token}
//	proxy -> DoProxy + maybe-propagate + response
//	ping -> {pong: true, pid}
//	<unknown> -> stderr "unknown action: <a>\n" + exit(2)
func BuildHandler(credsPath string) hostservice.Handler {
	return func(s *hostservice.Session) {
		action := "refresh"
		if v, ok := s.Get("action"); ok {
			if str, ok := v.(string); ok && str != "" {
				action = str
			}
		}
		// Per-request line: which action, and the proxy method/path (the
		// 2026-05-12 logout-loop triage keys off this — one line per jail
		// request so `is_refresh`/proxy activity is greppable). Mirrors
		// build_handler's opening log.info.
		logInfo("action=%s method=%s path=%s", action, sessionField(s, "method"), sessionField(s, "path"))
		switch action {
		case "refresh":
			_ = s.JSON(DoRefresh(credsPath))
		case "cached":
			cached := CachedTokens(credsPath)
			if cached == nil {
				logInfo("action=cached: no_cached_token")
				_ = s.JSON(errResult("error", "no_cached_token"))
			} else {
				logInfo("action=cached: hit at=%s rt=%s exp=%s",
					fpOf(cached, "accessToken"), fpOf(cached, "refreshToken"), expiresAtStr(cached))
				_ = s.JSON(AsOAuthResponse(cached))
			}
		case "proxy":
			decoded, errMsg := decodeProxyRequest(s.Request)
			if errMsg != "" {
				logWarn("action=proxy bad_request: %s", errMsg)
				_ = s.JSON(errResult("error", "bad_request", "message", errMsg))
				return
			}
			resp := DoProxy(decoded.method, decoded.path, decoded.headers, decoded.body)
			maybePropagateTokenResponse(credsPath, decoded, resp)
			_ = s.JSON(resp)
		case "ping":
			out := jsonx.NewOrderedMap()
			out.Set("pong", true)
			out.Set("pid", jsonx.IntValue(int64(os.Getpid())))
			_ = s.JSON(out)
		default:
			// Python: f"unknown action: {action!r}\n"
			logWarn("unknown action: %s (req keys: %s)", pytext.Repr(action), sortedKeys(s.Request))
			s.Stderr("unknown action: " + pytext.Repr(action) + "\n")
			s.Exit(2)
		}
	}
}

// proxyRequest is the validated form of a proxy action.
type proxyRequest struct {
	method  string
	path    string
	headers map[string]string
	body    []byte
}

// decodeProxyRequest validates a proxy request, returning it or an error
// message string.
func decodeProxyRequest(req *jsonx.OrderedMap) (proxyRequest, string) {
	method, _ := stringField(req, "method")
	if method == "" {
		return proxyRequest{}, "proxy: missing/invalid 'method'"
	}
	path, _ := stringField(req, "path")
	if path == "" {
		return proxyRequest{}, "proxy: missing/invalid 'path'"
	}
	headers := map[string]string{}
	if v, ok := req.Get("headers"); ok {
		hm, ok := v.(*jsonx.OrderedMap)
		if !ok {
			return proxyRequest{}, "proxy: 'headers' must be an object"
		}
		for _, k := range hm.Keys() {
			hv, _ := hm.Get(k)
			headers[k] = stringOf(hv)
		}
	}
	bodyB64 := ""
	if v, ok := req.Get("body_b64"); ok {
		s, ok := v.(string)
		if !ok {
			return proxyRequest{}, "proxy: 'body_b64' must be a string"
		}
		bodyB64 = s
	}
	var body []byte
	if bodyB64 != "" {
		b, err := base64.StdEncoding.DecodeString(bodyB64)
		if err != nil {
			return proxyRequest{}, "proxy: invalid base64 body: " + err.Error()
		}
		body = b
	}
	return proxyRequest{method: method, path: path, headers: headers, body: body}, ""
}

// successful POST /v1/oauth/token proxy round-trip, mirror the new tokens into
// the shared creds file (breaking the invalid_grant cascade after /login).
// Side-effect-free on every non-success path.
func maybePropagateTokenResponse(credsPath string, decoded proxyRequest, response *jsonx.OrderedMap) {
	if decoded.method != "POST" {
		return
	}
	if !hasPrefix(decoded.path, "/v1/oauth/token") {
		return
	}
	if _, isErr := response.Get("error"); isErr {
		return
	}
	status := int64(-1)
	if v, ok := response.Get("status"); ok {
		status, _ = asInt64(v)
	}
	if status != 200 {
		return
	}
	// From here every early return is WARN-level (not silent / DEBUG): each
	// means the shared creds file falls behind and the next refresh will fail
	// with invalid_grant. These skips were invisible for ~10 days (2026-05-12
	// logout loop); never again.
	bodyB64, ok := stringField(response, "body_b64")
	if !ok || bodyB64 == "" {
		logWarn("proxy mirror: token-endpoint 200 with empty/invalid body_b64 — " +
			"shared creds not updated; next refresh will likely 401")
		return
	}
	// Python catches base64 + JSON decode in ONE try/except with a single
	// "not parseable JSON" warning; match that message for both failures.
	body, err := base64.StdEncoding.DecodeString(bodyB64)
	if err == nil {
		var decodedBody any
		decodedBody, err = jsonx.Decode(body)
		if err == nil {
			upstreamResp, ok := decodedBody.(*jsonx.OrderedMap)
			if !ok {
				logWarn("proxy mirror: token-endpoint 200 body decoded to non-dict %s — "+
					"shared creds NOT updated", pyTypeName(decodedBody))
				return
			}
			propagate(credsPath, upstreamResp)
			return
		}
	}
	logWarn("proxy mirror: token-endpoint 200 body not parseable JSON "+
		"(content-encoding=%s): %s — shared creds NOT updated",
		contentEncodingRepr(response), err)
}

// propagate is the write-under-flock tail of maybePropagateTokenResponse, split
// out so the parse-failure returns above read linearly.
func propagate(credsPath string, upstreamResp *jsonx.OrderedMap) {
	_, hasAT := upstreamResp.Get("access_token")
	_, hasRT := upstreamResp.Get("refresh_token")
	if !hasAT || !hasRT {
		logWarn("proxy mirror: token-endpoint 200 body missing access_token/refresh_token "+
			"(keys=%s) — shared creds NOT updated", sortedKeys(upstreamResp))
		return
	}
	withRefreshLock(func() RefreshResult {
		previous, err := oauthFromCreds(credsPath)
		if err != nil {
			previous = jsonx.NewOrderedMap()
		}
		newOAuth := NormalizeOAuth(upstreamResp, previous)
		if werr := WriteTokens(credsPath, newOAuth); werr != nil {
			logWarn("proxy mirror: could not write %s: %s", credsPath, werr)
			return nil
		}
		logInfo("proxy mirror: wrote shared creds (rt %s -> %s, at -> %s)",
			fpOf(previous, "refreshToken"), fpOf(newOAuth, "refreshToken"), fpOf(newOAuth, "accessToken"))
		return nil
	})
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	// Python str(v) for non-strings; headers are always strings in practice.
	s, _ := jsonx.DumpsCompact(v)
	return s
}

// contentEncoding // `response.get("headers", {}).get("Content-Encoding") or .get("content-encoding")`
// used in the proxy-mirror parse-failure warning. Returns "" when absent (the
// caller reprs it, so absent -> Python's `None` repr — see the call site).
func contentEncoding(response *jsonx.OrderedMap) string {
	hv, ok := response.Get("headers")
	if !ok {
		return ""
	}
	hm, ok := hv.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	if v, ok := hm.Get("Content-Encoding"); ok {
		if s := stringOf(v); s != "" {
			return s
		}
	}
	if v, ok := hm.Get("content-encoding"); ok {
		return stringOf(v)
	}
	return ""
}

// contentEncodingRepr renders contentEncoding the way Python's f-string embeds
// the `.get(...) or .get(...)` expression: a present value as repr('gzip'),
// absent as None (Python's `None` when both header lookups miss / are empty).
func contentEncodingRepr(response *jsonx.OrderedMap) string {
	ce := contentEncoding(response)
	if ce == "" {
		return "None"
	}
	return pytext.Repr(ce)
}

// pyTypeName renders a decoded JSON value's Python type name, matching
// type(x).__name__ in the "decoded to non-dict %s" warning.
func pyTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.OrderedMap:
		return "dict"
	default:
		// jsonx integer literal -> Python int.
		return "int"
	}
}

// sessionField returns req[key] as a string for the per-request log line,
// defaulting to "-" when absent (Python's req.get("method", "-")).
func sessionField(s *hostservice.Session, key string) string {
	v, ok := s.Get(key)
	if !ok {
		return "-"
	}
	return stringOf(v)
}

// sortedKeys renders a request's keys the way Python's f-string embeds
// sorted(req.keys()) — a Python-repr list, e.g. "['action', 'method']".
func sortedKeys(req *jsonx.OrderedMap) string {
	keys := append([]string(nil), req.Keys()...)
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = pytext.Repr(k)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
