package oauthbroker

import (
	"encoding/base64"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// BuildHandler returns the hostservice.Handler for the broker, dispatching on
// the request "action" field. Mirrors oauth_broker.build_handler:
//
//	refresh (default) -> DoRefresh
//	cached            -> CachedTokens or {error: no_cached_token}
//	proxy             -> DoProxy + maybe-propagate + response
//	ping              -> {pong: true, pid}
//	<unknown>         -> stderr "unknown action: <a>\n" + exit(2)
func BuildHandler(credsPath string) hostservice.Handler {
	return func(s *hostservice.Session) {
		action := "refresh"
		if v, ok := s.Get("action"); ok {
			if str, ok := v.(string); ok && str != "" {
				action = str
			}
		}
		switch action {
		case "refresh":
			_ = s.JSON(DoRefresh(credsPath))
		case "cached":
			cached := CachedTokens(credsPath)
			if cached == nil {
				_ = s.JSON(errResult("error", "no_cached_token"))
			} else {
				_ = s.JSON(AsOAuthResponse(cached))
			}
		case "proxy":
			decoded, errMsg := decodeProxyRequest(s.Request)
			if errMsg != "" {
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
// message string. Mirrors _decode_proxy_request.
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

// maybePropagateTokenResponse mirrors _maybe_propagate_token_response: on a
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
	bodyB64, ok := stringField(response, "body_b64")
	if !ok || bodyB64 == "" {
		return
	}
	body, err := base64.StdEncoding.DecodeString(bodyB64)
	if err != nil {
		return
	}
	decodedBody, err := jsonx.Decode(body)
	if err != nil {
		return
	}
	upstreamResp, ok := decodedBody.(*jsonx.OrderedMap)
	if !ok {
		return
	}
	if _, ok := upstreamResp.Get("access_token"); !ok {
		return
	}
	if _, ok := upstreamResp.Get("refresh_token"); !ok {
		return
	}
	withRefreshLock(func() RefreshResult {
		previous, ok := oauthFromCreds(credsPath)
		if !ok {
			previous = jsonx.NewOrderedMap()
		}
		newOAuth := NormalizeOAuth(upstreamResp, previous)
		_ = WriteTokens(credsPath, newOAuth)
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

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
