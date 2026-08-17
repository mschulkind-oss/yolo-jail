package oauthbroker

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The entitlement metadata contract, and why it needs a test rather than an argument.
//
// Claude Code >= 2.1.200 treats a credentials file carrying ONLY the token trio
// (accessToken/refreshToken/expiresAt) as NOT LOGGED IN: the subscription itself lives in
// subscriptionType / rateLimitTier, so an OAuth credential stripped of those keys is a valid
// token that the tool refuses to use (docs/design/agent-auth-modes.md §3 — it is also why
// apiKeyHelper cannot carry a subscription: an API key has no entitlement metadata slot).
//
// Preservation used to have TWO independent guards. The jail entrypoint's shared_credentials
// harvest copied oauthMetadataKeys unconditionally out of the local file, and the host broker's
// NormalizeOAuth preserved every previous key across a refresh. The harvest is now DELETED — the
// shared file always wins and nothing in the boot path rewrites those keys any more
// (docs/design/pack-code-separation.md §5/§8, internal/entrypoint/claude.go). That collapses the
// whole property onto ONE guard: the broker must not drop them when it rewrites the shared file.
//
// So this is the test that stands in for the deleted code. It is a REGRESSION TEST in the
// strict sense: the upstream refresh response legitimately carries no entitlement fields (they
// are not part of the OAuth token response), so any rewrite of NormalizeOAuth that builds its
// output FROM THE RESPONSE instead of copying previous-then-overriding will pass every
// token-shaped assertion in this package and silently log every jail out.

// TestNormalizeOAuthPreservesEntitlementMetadata pins the unit-level rule: keys the upstream
// response knows nothing about survive from previous into the normalized output.
func TestNormalizeOAuthPreservesEntitlementMetadata(t *testing.T) {
	previous := jsonx.NewOrderedMap()
	previous.Set("accessToken", "AT_old")
	previous.Set("refreshToken", "RT_old")
	previous.Set("expiresAt", jsonx.IntValue(1))
	previous.Set("scopes", []any{"user:inference", "user:profile"})
	previous.Set("subscriptionType", "team")
	previous.Set("rateLimitTier", "default_teams")

	// A real upstream refresh body: the token trio and a scope string, nothing else.
	upstream := jsonx.NewOrderedMap()
	upstream.Set("access_token", "AT_new")
	upstream.Set("refresh_token", "RT_new")
	upstream.Set("expires_in", jsonx.IntValue(3600))
	upstream.Set("scope", "user:inference user:profile")

	out := NormalizeOAuth(upstream, previous)

	for key, want := range map[string]string{
		"subscriptionType": "team",
		"rateLimitTier":    "default_teams",
	} {
		v, ok := out.Get(key)
		if !ok {
			t.Fatalf("NormalizeOAuth dropped %q — a creds file without it reads as NOT LOGGED IN "+
				"to Claude Code >= 2.1.200", key)
		}
		if s, _ := v.(string); s != want {
			t.Errorf("NormalizeOAuth %q = %#v, want %q", key, v, want)
		}
	}
	// The tokens must still rotate — preservation must not be "copy previous and ignore the
	// response", which would pass the assertions above while breaking the refresh itself.
	if v, _ := out.Get("accessToken"); v != "AT_new" {
		t.Errorf("accessToken = %#v, want AT_new (metadata preservation must not block rotation)", v)
	}
	if v, _ := out.Get("refreshToken"); v != "RT_new" {
		t.Errorf("refreshToken = %#v, want RT_new (metadata preservation must not block rotation)", v)
	}
}

// TestRefreshPreservesEntitlementMetadataOnDisk drives a full DoRefresh against a fake upstream
// and asserts the metadata survives the whole read -> normalize -> WriteTokens round trip, on
// disk, in the file every jail symlinks to. The unit test above pins NormalizeOAuth; this one
// pins that nothing between the shared file and the shared file loses the keys.
func TestRefreshPreservesEntitlementMetadataOnDisk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Note the absence of subscriptionType/rateLimitTier: the upstream token endpoint
		// does not return entitlement metadata, which is exactly why it must be preserved
		// rather than re-derived.
		_, _ = w.Write([]byte(`{"access_token":"AT_new","refresh_token":"RT_new",` +
			`"expires_in":3600,"scope":"user:inference"}`))
	}))
	defer srv.Close()
	t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)

	dir := t.TempDir()
	creds := filepath.Join(dir, "creds.json")
	root := jsonx.NewOrderedMap()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", "AT_old")
	oa.Set("refreshToken", "RT_old")
	oa.Set("expiresAt", jsonx.IntValue(0)) // expired -> cache miss -> real refresh
	oa.Set("scopes", []any{"user:inference"})
	oa.Set("subscriptionType", "team")
	oa.Set("rateLimitTier", "default_teams")
	root.Set("claudeAiOauth", oa)
	blob, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		t.Fatalf("DumpsIndent: %v", err)
	}
	if err := os.WriteFile(creds, []byte(blob), 0o600); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	defer func() { RefreshLockPath = "" }()

	if res := DoRefresh(creds); res != nil {
		if _, isErr := res.Get("error"); isErr {
			t.Fatalf("refresh errored: %v", res)
		}
	}

	after, err := oauthFromCreds(creds)
	if err != nil {
		t.Fatalf("re-read creds: %v", err)
	}
	if v, _ := after.Get("accessToken"); v != "AT_new" {
		t.Fatalf("refresh did not rotate the access token (got %#v) — the test proved nothing", v)
	}
	for key, want := range map[string]string{
		"subscriptionType": "team",
		"rateLimitTier":    "default_teams",
	} {
		v, ok := after.Get(key)
		if !ok {
			t.Errorf("shared creds lost %q across a broker refresh — every jail reading this "+
				"file is now LOGGED OUT despite holding a valid token", key)
			continue
		}
		if s, _ := v.(string); s != want {
			t.Errorf("shared creds %q = %#v after refresh, want %q", key, v, want)
		}
	}
}
