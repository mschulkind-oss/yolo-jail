package oauthbroker

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestRefreshLoggingEndToEnd drives a full DoRefresh against a fake upstream
// and asserts the operational log lines fire with fingerprints (no raw tokens)
// — the incident-forensics contract. Regression guard: the broker must never
// log a raw access/refresh token.
func TestRefreshLoggingEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	setupLogWriter(&buf, true)
	defer func() { logger = nil; logVerbose = false }()

	// Fake upstream returns a rotated token trio.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"AT_new_secret","refresh_token":"RT_new_secret","expires_in":3600,"scope":"user:inference"}`))
	}))
	defer srv.Close()
	os.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
	defer os.Unsetenv("YOLO_BROKER_UPSTREAM_URL")

	dir := t.TempDir()
	creds := filepath.Join(dir, "creds.json")
	root := jsonx.NewOrderedMap()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", "AT_old_secret")
	oa.Set("refreshToken", "RT_old_secret")
	oa.Set("expiresAt", jsonx.IntValue(0)) // expired -> cache miss
	root.Set("claudeAiOauth", oa)
	blob, _ := jsonx.DumpsIndent(root, 2)
	os.WriteFile(creds, []byte(blob), 0o600)

	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	defer func() { RefreshLockPath = "" }()

	res := DoRefresh(creds)
	if _, isErr := res.Get("error"); isErr {
		t.Fatalf("refresh errored: %v", res)
	}
	out := buf.String()
	t.Logf("LOG:\n%s", out)

	for _, want := range []string{
		"do_refresh: shared=",
		"cache miss: refreshing upstream with rt=" + TokenFP("RT_old_secret"),
		"refreshed: rt " + TokenFP("RT_old_secret") + " -> " + TokenFP("RT_new_secret"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q", want)
		}
	}
	for _, secret := range []string{"AT_old_secret", "RT_old_secret", "AT_new_secret", "RT_new_secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("SECURITY: raw token %q leaked to log:\n%s", secret, out)
		}
	}
}
