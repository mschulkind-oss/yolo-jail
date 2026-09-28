package oauthbroker

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// callerauth_test.go pins the broker's half of Claude OAuth caller authentication
// (docs/plans/notch-convergence.md §2.3, NC-D3). The terminator listens on 127.0.0.1:443, which a
// jail on `network.mode: host` shares with the host, and before this any process that POSTed a
// refresh grant there got the machine-wide access and refresh tokens. Now a refresh is served
// only when the caller presents the shared file's current refresh token — which a Claude re-reads
// immediately before it POSTs — or one this broker just replaced. Driven through the real
// BuildHandler behind a real endpoint, by the real terminator client, so deleting the
// DoRefreshAsCaller arm in BuildHandler fails it.

func seedCreds(t *testing.T, dir, refreshToken string) string {
	t.Helper()
	creds := filepath.Join(dir, "creds.json")
	root := jsonx.NewOrderedMap()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", "AT_old")
	oa.Set("refreshToken", refreshToken)
	oa.Set("expiresAt", jsonx.IntValue(0)) // expired: a served refresh goes upstream
	oa.Set("scopes", []any{"user:inference"})
	root.Set("claudeAiOauth", oa)
	blob, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creds, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	return creds
}

func serveBroker(t *testing.T, creds string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	endpoint := filepath.Join(dir, "claude-oauth-broker.endpoint")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = hostservice.ServeEndpoint(BuildHandler(creds), endpoint, stop)
		close(done)
	}()
	t.Cleanup(func() { close(stop); <-done })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(endpoint); err == nil {
			return endpoint
		}
		if time.Now().After(deadline) {
			t.Fatal("the broker never published its endpoint")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheBrokerRefreshesOnlyForACallerPresentingTheLogin(t *testing.T) {
	var upstream atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstream.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT_new","refresh_token":"RT_new","expires_in":3600}`))
	}))
	defer srv.Close()
	t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
	dir := t.TempDir()
	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	defer func() { RefreshLockPath = "" }()
	recentlyReplaced = &replacedTokens{}
	creds := seedCreds(t, dir, "RT_current")
	endpoint := serveBroker(t, creds)

	for _, stranger := range []string{"RT_stranger", ""} {
		res := oauthterminator.Refresh(endpoint, stranger)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("a caller presenting %q got %d %s, want 401", stranger, res.Status, res.Body)
		}
		if strings.Contains(string(res.Body), "AT_old") || strings.Contains(string(res.Body), "RT_current") ||
			strings.Contains(string(res.Body), "invalid_grant") {
			t.Fatalf("the refusal carries a token or invalid_grant: %s", res.Body)
		}
	}
	if upstream.Load() != 0 {
		t.Fatalf("an unauthenticated caller made the broker refresh upstream %d times", upstream.Load())
	}

	res := oauthterminator.Refresh(endpoint, "RT_current")
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "AT_new") {
		t.Fatalf("the caller presenting the login got %d %s, want 200 with the new token", res.Status, res.Body)
	}
	if upstream.Load() != 1 {
		t.Fatalf("upstream refreshes = %d, want 1", upstream.Load())
	}
	// THE REFRESH RACE: a second Claude read RT_current just before the refresh above rotated it.
	// It still presents a login this broker knows, and is answered from the cache.
	res = oauthterminator.Refresh(endpoint, "RT_current")
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "AT_new") {
		t.Fatalf("a Claude that read the just-replaced token got %d %s, want 200", res.Status, res.Body)
	}
	if res = oauthterminator.Refresh(endpoint, "RT_new"); res.Status != http.StatusOK {
		t.Fatalf("the current token got %d %s, want 200", res.Status, res.Body)
	}
	if upstream.Load() != 1 {
		t.Errorf("upstream refreshes = %d, want the cache to answer after the first", upstream.Load())
	}
}

// A frame with no presented-token field is a terminator from before caller authentication, in
// a jail still running the binaries it booted with: it is served as it always was, so a host
// upgrade does not log out every running jail's Claude.
func TestARefreshFromAnOlderTerminatorIsServedAsBefore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT_new","refresh_token":"RT_new","expires_in":3600}`))
	}))
	defer srv.Close()
	t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
	dir := t.TempDir()
	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	defer func() { RefreshLockPath = "" }()
	endpoint := serveBroker(t, seedCreds(t, dir, "RT_current"))
	req := jsonx.NewOrderedMap()
	req.Set("action", "refresh")
	resp, err := oauthterminator.AskHostBroker(endpoint, req)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resp.Get("access_token"); v != "AT_new" {
		t.Errorf("an older terminator's refresh = %v, want it served", resp)
	}
}

func TestReplacedTokenMemoryIsBounded(t *testing.T) {
	r := &replacedTokens{}
	for i := 0; i <= replacedTokenMemory; i++ {
		r.record("RT_" + string(rune('a'+i)))
	}
	if r.has("RT_a") {
		t.Error("the oldest replaced token is still presentable past the memory bound")
	}
	if !r.has("RT_" + string(rune('a'+replacedTokenMemory))) {
		t.Error("the newest replaced token is not presentable")
	}
	if callerPresentsLogin("RT_x", "") || callerPresentsLogin("", "") {
		t.Error("an empty presented token authenticated a caller")
	}
}
