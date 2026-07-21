package oauthbroker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestTokenFP pins the fingerprint algorithm: an 8-char sha256-hex prefix,
// "(none)" for empty. SECURITY: a full token must never appear; the fingerprint
// must be non-reversible and stable.
func TestTokenFP(t *testing.T) {
	// sha256("RT_test_secret")[:8], computed independently.
	if got, want := TokenFP("RT_test_secret"), "5e661e46"; got != want {
		t.Errorf("TokenFP = %q, want %q (must be sha256-hex[:8])", got, want)
	}
	if got := TokenFP(""); got != "(none)" {
		t.Errorf("TokenFP(\"\") = %q, want (none)", got)
	}
	// Equal tokens share a fingerprint (the cross-process rotation-eyeballing
	// property); different tokens (almost surely) don't. Build the two "equal"
	// inputs separately so the comparison is a real stability check, not a
	// same-expression tautology (staticcheck SA4000).
	tok := "sa" + "me"
	if TokenFP(tok) != TokenFP("same") {
		t.Error("TokenFP not stable for equal tokens")
	}
	if TokenFP("a") == TokenFP("b") {
		t.Error("TokenFP collision on distinct short tokens")
	}
	// The fingerprint must not be a prefix/substring of the token.
	if strings.Contains("supersecrettoken", TokenFP("supersecrettoken")) {
		t.Error("TokenFP leaked a substring of the token")
	}
}

// TestDescribeCredsRedaction proves describeCreds emits fingerprints, mtime,
// and expiresAt — and NEVER the raw token bytes.
func TestDescribeCredsRedaction(t *testing.T) {
	dir := t.TempDir()

	// Absent file.
	if got := describeCreds(filepath.Join(dir, "nope.json")); !strings.HasSuffix(got, ": <absent>") {
		t.Errorf("absent creds: %q, want <absent> suffix", got)
	}

	// Malformed JSON -> read_error, no crash, no token leak.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := describeCreds(bad); !strings.Contains(got, "read_error=") {
		t.Errorf("malformed creds: %q, want read_error=", got)
	}

	// Well-formed creds: fingerprints present, raw tokens absent.
	good := filepath.Join(dir, "creds.json")
	root := jsonx.NewOrderedMap()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", "AT_super_secret_value")
	oa.Set("refreshToken", "RT_super_secret_value")
	oa.Set("expiresAt", jsonx.IntValue(1700000000000))
	root.Set("claudeAiOauth", oa)
	blob, _ := jsonx.DumpsIndent(root, 2)
	if err := os.WriteFile(good, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	got := describeCreds(good)
	if !strings.Contains(got, "at="+TokenFP("AT_super_secret_value")) {
		t.Errorf("describeCreds missing access-token fingerprint: %q", got)
	}
	if !strings.Contains(got, "rt="+TokenFP("RT_super_secret_value")) {
		t.Errorf("describeCreds missing refresh-token fingerprint: %q", got)
	}
	if !strings.Contains(got, "exp=1700000000000") {
		t.Errorf("describeCreds missing expiresAt: %q", got)
	}
	if strings.Contains(got, "AT_super_secret_value") || strings.Contains(got, "RT_super_secret_value") {
		t.Fatalf("SECURITY: describeCreds leaked a raw token: %q", got)
	}
}

// TestDescribeExpiresAtNone: a creds object without expiresAt renders "None",
// never a raw/garbage value.
func TestDescribeExpiresAtNone(t *testing.T) {
	if got := describeExpiresAt(jsonx.NewOrderedMap()); got != "None" {
		t.Errorf("describeExpiresAt(empty) = %q, want None", got)
	}
	oa := jsonx.NewOrderedMap()
	oa.Set("expiresAt", jsonx.IntValue(42))
	if got := describeExpiresAt(oa); got != "42" {
		t.Errorf("describeExpiresAt(42) = %q, want 42", got)
	}
}

// TestLogLevelsAndFormat checks the "<LEVEL> <name>: <message>" shape and that
// DEBUG is gated on verbose.
func TestLogLevelsAndFormat(t *testing.T) {
	var buf bytes.Buffer
	setupLogWriter(&buf, false)
	defer func() { logger = nil; logVerbose = false }()

	logInfo("hello %d", 7)
	logDebug("should be suppressed")
	out := buf.String()
	if !strings.Contains(out, "INFO oauth-broker-host: hello 7") {
		t.Errorf("info line = %q, want 'INFO oauth-broker-host: hello 7'", out)
	}
	if strings.Contains(out, "should be suppressed") {
		t.Errorf("DEBUG emitted without verbose: %q", out)
	}

	buf.Reset()
	setupLogWriter(&buf, true)
	logDebug("now visible")
	if !strings.Contains(buf.String(), "DEBUG oauth-broker-host: now visible") {
		t.Errorf("verbose DEBUG missing: %q", buf.String())
	}
}

// TestLogNilIsNoop: with no logger configured (the unit-test default), log
// calls must not panic.
func TestLogNilIsNoop(t *testing.T) {
	logger = nil
	logInfo("no writer configured")
	logWarn("still fine")
	logError("still fine")
	logDebug("still fine")
}

// TestSortedKeys renders request keys as a sorted Python-repr list.
func TestSortedKeys(t *testing.T) {
	m := jsonx.NewOrderedMap()
	m.Set("method", "GET")
	m.Set("action", "proxy")
	if got, want := sortedKeys(m), "['action', 'method']"; got != want {
		t.Errorf("sortedKeys = %q, want %q", got, want)
	}
	if got := sortedKeys(jsonx.NewOrderedMap()); got != "[]" {
		t.Errorf("sortedKeys(empty) = %q, want []", got)
	}
}

// TestContentEncodingRepr checks the canonical/lowercase header lookup plus
// repr rendering.
func TestContentEncodingRepr(t *testing.T) {
	// Absent -> None.
	resp := jsonx.NewOrderedMap()
	if got := contentEncodingRepr(resp); got != "None" {
		t.Errorf("no headers: %q, want None", got)
	}
	// Present canonical header -> repr('gzip').
	h := jsonx.NewOrderedMap()
	h.Set("Content-Encoding", "gzip")
	resp.Set("headers", h)
	if got := contentEncodingRepr(resp); got != "'gzip'" {
		t.Errorf("gzip: %q, want 'gzip'", got)
	}
	// Lowercase fallback.
	resp2 := jsonx.NewOrderedMap()
	h2 := jsonx.NewOrderedMap()
	h2.Set("content-encoding", "br")
	resp2.Set("headers", h2)
	if got := contentEncodingRepr(resp2); got != "'br'" {
		t.Errorf("br: %q, want 'br'", got)
	}
}

func TestPyTypeName(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "NoneType"},
		{true, "bool"},
		{"s", "str"},
		{1.5, "float"},
		{[]any{}, "list"},
		{jsonx.IntValue(3), "int"},
		{jsonx.NewOrderedMap(), "dict"},
	}
	for _, c := range cases {
		if got := pyTypeName(c.v); got != c.want {
			t.Errorf("pyTypeName(%T) = %q, want %q", c.v, got, c.want)
		}
	}
}
