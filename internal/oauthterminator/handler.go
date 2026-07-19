package oauthterminator

import (
	"encoding/base64"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// IsRefreshGrant reports whether body is a JSON object with
// grant_type == "refresh_token". Anything else (authorization_code from
// /login, unparseable, empty) is proxied untouched. Mirrors _is_refresh_grant.
func IsRefreshGrant(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	decoded, err := jsonx.Decode(body)
	if err != nil {
		return false
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	v, ok := m.Get("grant_type")
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s == "refresh_token"
}

// ProxyResult is a proxied upstream response or a 502 error, ready to write.
type ProxyResult struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// ProxyUpstream ships a request to the host broker with action=proxy and maps
// the response (or failure) to an HTTP status/body. Mirrors _proxy_upstream:
// transport failure or broker {error} -> 502 with the layer-named detail;
// malformed proxy response -> 502 broker_bad_response; else the upstream
// status/headers/body verbatim.
func ProxyUpstream(socketPath, method, path string, headers map[string]string, body []byte) ProxyResult {
	req := jsonx.NewOrderedMap()
	req.Set("action", "proxy")
	req.Set("method", method)
	req.Set("path", path)
	h := jsonx.NewOrderedMap()
	for k, v := range headers {
		h.Set(k, v)
	}
	req.Set("headers", h)
	if len(body) > 0 {
		req.Set("body_b64", base64.StdEncoding.EncodeToString(body))
	} else {
		req.Set("body_b64", "")
	}

	resp, err := AskHostBroker(socketPath, req)
	if err != nil {
		// err names the failing layer (relay vs broker) — don't prefix it
		// (Python: log.error("proxy failed: %s", e)).
		LogError("proxy failed: %s", err)
		return jsonError(502, "broker_unavailable", err.Error())
	}
	if _, isErr := resp.Get("error"); isErr {
		b, _ := jsonx.DumpsCompact(resp)
		return ProxyResult{Status: 502, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(b)}
	}
	statusV, ok := resp.Get("status")
	if !ok {
		LogError("malformed proxy response from host broker: missing status (resp=%s)", redactedResp(resp))
		return jsonError(502, "broker_bad_response", "missing status")
	}
	status, ok := asInt(statusV)
	if !ok {
		LogError("malformed proxy response from host broker: non-int status (resp=%s)", redactedResp(resp))
		return jsonError(502, "broker_bad_response", "non-int status")
	}
	respHeaders := map[string]string{}
	if hv, ok := resp.Get("headers"); ok {
		if hm, ok := hv.(*jsonx.OrderedMap); ok {
			for _, k := range hm.Keys() {
				v, _ := hm.Get(k)
				respHeaders[k] = stringOf(v)
			}
		}
	}
	var respBody []byte
	if bv, ok := resp.Get("body_b64"); ok {
		if s, ok := bv.(string); ok && s != "" {
			b, derr := base64.StdEncoding.DecodeString(s)
			if derr != nil {
				LogError("malformed proxy response from host broker: %s (resp=%s)", derr, redactedResp(resp))
				return jsonError(502, "broker_bad_response", derr.Error())
			}
			respBody = b
		}
	}
	return ProxyResult{Status: status, Headers: respHeaders, Body: respBody}
}

// Refresh sends action=refresh and maps the response to an HTTP result:
// transport failure -> 502; broker {error} -> 400; else 200 with the tokens.
// Mirrors the is_refresh branch of _handle.
func Refresh(socketPath string) ProxyResult {
	resp, err := AskHostBroker(socketPath, singleton("action", "refresh"))
	if err != nil {
		// Message names the failing layer (relay vs broker).
		LogError("refresh failed: %s", err)
		return jsonError(502, "broker_unavailable", err.Error())
	}
	if _, isErr := resp.Get("error"); isErr {
		errVal, _ := resp.Get("error")
		LogWarn("refresh: broker returned error=%s (%s)", stringOf(errVal), errDetail(resp))
		b, _ := jsonx.DumpsCompact(resp)
		return ProxyResult{Status: 400, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(b)}
	}
	var expiresIn string
	if v, ok := resp.Get("expires_in"); ok {
		expiresIn = stringOf(v)
	}
	LogInfo("refresh: OK expires_in=%s", expiresIn)
	b, _ := jsonx.DumpsCompact(resp)
	return ProxyResult{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(b)}
}

func jsonError(status int, errKey, detail string) ProxyResult {
	m := jsonx.NewOrderedMap()
	m.Set("error", errKey)
	m.Set("detail", detail)
	b, _ := jsonx.DumpsCompact(m)
	return ProxyResult{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(b)}
}

func singleton(k string, v any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set(k, v)
	return m
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		n := 0
		neg := false
		s := t
		if strings.HasPrefix(s, "-") {
			neg = true
			s = s[1:]
		}
		if s == "" {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		if neg {
			n = -n
		}
		return n, true
	default:
		s, err := jsonx.DumpsCompact(v)
		if err != nil {
			return 0, false
		}
		return asInt(s)
	}
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	s, _ := jsonx.DumpsCompact(v)
	return s
}

// errDetail renders the broker error's message/body for the "refresh: broker
// returned error=... (...)" line — Python's `resp.get("message") or
// resp.get("body") or ""`.
func errDetail(resp *jsonx.OrderedMap) string {
	if v, ok := resp.Get("message"); ok {
		return stringOf(v)
	}
	if v, ok := resp.Get("body"); ok {
		return stringOf(v)
	}
	return ""
}

// redactedResp renders a proxy response for the malformed-response log line
// WITHOUT its body_b64. Python logs the full resp dict (resp=%r), but this
// daemon terminates Claude's TLS, so a proxied body_b64 can carry token
// material; we redact it and keep the structural keys the triage needs (status,
// header names, error). SECURITY: never remove this redaction.
func redactedResp(resp *jsonx.OrderedMap) string {
	safe := jsonx.NewOrderedMap()
	for _, k := range resp.Keys() {
		v, _ := resp.Get(k)
		if k == "body_b64" {
			s, _ := v.(string)
			safe.Set(k, "<redacted "+itoa(len(s))+"B>")
			continue
		}
		safe.Set(k, v)
	}
	s, _ := jsonx.DumpsCompact(safe)
	return s
}
