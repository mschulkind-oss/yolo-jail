package oauthterminator

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestRedactedRespStripsBody: the malformed-proxy-response log line must NOT
// carry body_b64 (this daemon terminates Claude's TLS, so a proxied body can
// hold token material). Structural keys (status, error) survive for triage.
func TestRedactedRespRedactsBody(t *testing.T) {
	resp := jsonx.NewOrderedMap()
	resp.Set("status", jsonx.IntValue(200))
	resp.Set("body_b64", "c2VjcmV0LXRva2VuLW1hdGVyaWFs") // "secret-token-material"
	resp.Set("error", "broker_bad_response")

	got := redactedResp(resp)
	if strings.Contains(got, "c2VjcmV0LXRva2VuLW1hdGVyaWFs") {
		t.Fatalf("SECURITY: redactedResp leaked body_b64: %q", got)
	}
	if !strings.Contains(got, "<redacted") {
		t.Errorf("redactedResp missing redaction marker: %q", got)
	}
	if !strings.Contains(got, `"status": 200`) {
		t.Errorf("redactedResp dropped status: %q", got)
	}
	if !strings.Contains(got, `"error": "broker_bad_response"`) {
		t.Errorf("redactedResp dropped error: %q", got)
	}
	// The redaction records the base64 byte length (what's in the dict; decode
	// may have failed at the call site) so a soak still knows a body existed.
	if !strings.Contains(got, "28B>") {
		t.Errorf("redactedResp missing body length marker: %q", got)
	}
}

// TestErrDetail `resp.get("message") or resp.get("body") or ""`.
func TestErrDetail(t *testing.T) {
	m := jsonx.NewOrderedMap()
	if got := errDetail(m); got != "" {
		t.Errorf("empty errDetail = %q, want \"\"", got)
	}
	m.Set("body", "bad")
	if got := errDetail(m); got != "bad" {
		t.Errorf("body errDetail = %q, want bad", got)
	}
	m.Set("message", "boom")
	if got := errDetail(m); got != "boom" {
		t.Errorf("message errDetail = %q, want boom (message wins over body)", got)
	}
}

// TestLogFormatAndGating checks the level/name shape and DEBUG gating.
func TestLogFormatAndGating(t *testing.T) {
	var buf bytes.Buffer
	setupLogWriter(&buf, false)
	defer func() { logger = nil; logVerbose = false }()

	LogInfo("request: %s %s", "POST", "/v1/oauth/token")
	logDebug("hidden")
	out := buf.String()
	if !strings.Contains(out, "INFO oauth-broker-jail: request: POST /v1/oauth/token") {
		t.Errorf("info line = %q", out)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("DEBUG emitted without verbose: %q", out)
	}
}

// TestLogNilNoop: no configured logger -> no panic (unit-test default).
func TestLogNilNoop(t *testing.T) {
	logger = nil
	LogInfo("x")
	LogWarn("x")
	LogError("x")
	logDebug("x")
}
