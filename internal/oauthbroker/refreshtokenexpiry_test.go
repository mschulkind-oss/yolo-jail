package oauthbroker

// refreshtokenexpiry_test.go pins where the broker gets refreshTokenExpiresAt, the claudeAiOauth
// field in which Claude Code keeps a login's refresh-token deadline (epoch milliseconds) and from
// which it raises its "login expires in N days" warning in the last three days.
//
// Claude's own rule, MEASURED in the 2.1.288 binary: a login's token exchange sets the field from
// the response's refresh_token_expires_in (search `refreshTokenExpiresAt:ver(`); a refresh sets it
// from the same response field when there is one, and its save keeps the stored value otherwise,
// only after checking that the stored record still holds the refresh token it just redeemed
// (search `refreshTokenExpiresAt:n.refreshTokenExpiresAt??e?.refreshTokenExpiresAt`). So a
// deadline is carried from one record to the next only within one login, never from a previous
// login onto a new one.
//
// The broker used to read neither: it copied the previous record forward whole, so its mirror of
// a /login wrote the previous LOGIN's deadline onto the new login's tokens, and a refresh
// response's own value was discarded.
//
// No real login: the token endpoint is an httptest server and every token is a fake.

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// rtDeadlineSeconds is a refresh_token_expires_in an upstream response carries. Deliberately not
// a round number of days: the 2026-10-02 login's response carried 2406789 (MEASURED from the
// deadline Claude wrote, which shares its millisecond with that login's expiresAt).
const rtDeadlineSeconds = 2406789

// pinClock fixes nowMS for one test and returns the instant.
func pinClock(t *testing.T) int64 {
	t.Helper()
	const now = int64(1_790_975_561_711)
	saved := nowFunc
	nowFunc = func() int64 { return now }
	t.Cleanup(func() { nowFunc = saved })
	return now
}

// singleFileStore points the broker at one credentials file in a temp dir, with no canonical and
// no views (store.go's single-file mode), and restores the globals it touched.
func singleFileStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	saved := []string{RefreshLockPath, CanonicalPath, ViewRegistryDir}
	RefreshLockPath, CanonicalPath, ViewRegistryDir = filepath.Join(dir, "refresh.lock"), "", ""
	t.Cleanup(func() { RefreshLockPath, CanonicalPath, ViewRegistryDir = saved[0], saved[1], saved[2] })
	return filepath.Join(dir, ".credentials.json")
}

// seedLoginWithDeadline writes a login whose record carries a refresh-token deadline, as Claude
// writes one.
func seedLoginWithDeadline(t *testing.T, path, at, rt string, expiresAt, rtDeadline int64) {
	t.Helper()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", at)
	oa.Set("refreshToken", rt)
	oa.Set("expiresAt", jsonx.IntValue(expiresAt))
	oa.Set(refreshTokenExpiresAtKey, jsonx.IntValue(rtDeadline))
	oa.Set("scopes", []any{"user:inference", "user:profile"})
	oa.Set("subscriptionType", "team")
	oa.Set("rateLimitTier", "default_claude_max_5x")
	root := jsonx.NewOrderedMap()
	root.Set("claudeAiOauth", oa)
	blob, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rtDeadlineOf reads the refresh-token deadline from a credentials file; present is false when
// the key is absent.
func rtDeadlineOf(t *testing.T, path string) (deadline int64, present bool) {
	t.Helper()
	oa, err := oauthFromCreds(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, ok := oa.Get(refreshTokenExpiresAtKey)
	if !ok {
		return 0, false
	}
	n, ok := asInt64(v)
	if !ok {
		t.Fatalf("%s %s = %#v, not a number", path, refreshTokenExpiresAtKey, v)
	}
	return n, true
}

// tokenBody is an upstream token response; deadline 0 leaves refresh_token_expires_in out, and
// -1 spells rtDeadlineSeconds as a JSON string, which is not a number Claude reads.
func tokenBody(at, rt string, deadline int) string {
	extra := ""
	switch {
	case deadline == -1:
		extra = `,"refresh_token_expires_in":"` + strconv.Itoa(rtDeadlineSeconds) + `"`
	case deadline != 0:
		extra = `,"refresh_token_expires_in":` + strconv.Itoa(deadline)
	}
	return `{"access_token":"` + at + `","refresh_token":"` + rt + `","expires_in":28800` + extra +
		`,"scope":"user:inference user:profile"}`
}

// TestTheProxyMirrorNeverWritesAPreviousLoginsRefreshDeadline drives the mirror the broker's proxy
// action runs after a token request a jail's Claude made through it (maybePropagateTokenResponse,
// called from BuildHandler's "proxy" case). The machine's login is inside its last two days, which
// is when Claude warns and a user logs in again.
func TestTheProxyMirrorNeverWritesAPreviousLoginsRefreshDeadline(t *testing.T) {
	now := pinClock(t)
	oldDeadline := now + 2*86_400_000
	fromResponse := now + rtDeadlineSeconds*1000

	cases := []struct {
		name     string
		body     string // the proxied request's body, as the jail's Claude sent it
		deadline int    // refresh_token_expires_in in the response; 0 for none
		want     int64  // 0 for "the key is absent"
	}{
		{"a /login whose response carries a deadline takes it",
			`{"grant_type":"authorization_code","code":"fake-code","client_id":"` + ClientID + `"}`,
			rtDeadlineSeconds, fromResponse},
		{"a /login whose response carries none has none, not the previous login's",
			`{"grant_type":"authorization_code","code":"fake-code","client_id":"` + ClientID + `"}`,
			0, 0},
		{"a /login whose response spells the deadline as a string has none, as Claude reads it",
			`{"grant_type":"authorization_code","code":"fake-code","client_id":"` + ClientID + `"}`,
			-1, 0},
		{"a refresh of the machine's own token keeps that login's deadline",
			`grant_type=refresh_token&refresh_token=RT_old&client_id=` + ClientID,
			0, oldDeadline},
		{"a refresh of the machine's own token takes a deadline its response carries",
			`{"grant_type":"refresh_token","refresh_token":"RT_old","client_id":"` + ClientID + `"}`,
			rtDeadlineSeconds, fromResponse},
		{"a refresh of some other login's token does not inherit the machine's deadline",
			`{"grant_type":"refresh_token","refresh_token":"RT_elsewhere","client_id":"` + ClientID + `"}`,
			0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds := singleFileStore(t)
			seedLoginWithDeadline(t, creds, "AT_old", "RT_old", now-60_000, oldDeadline)
			resp := jsonx.NewOrderedMap()
			resp.Set("status", jsonx.IntValue(200))
			resp.Set("body_b64", base64.StdEncoding.EncodeToString([]byte(tokenBody("AT_new", "RT_new", tc.deadline))))

			maybePropagateTokenResponse(creds,
				proxyRequest{method: "POST", path: "/v1/oauth/token", body: []byte(tc.body)}, resp)

			if oa, _ := oauthFromCreds(creds); str(oa, "refreshToken") != "RT_new" {
				t.Fatalf("the mirror did not write the response's tokens (refreshToken %q): the test proved nothing",
					str(oa, "refreshToken"))
			}
			got, present := rtDeadlineOf(t, creds)
			switch {
			case tc.want == 0 && present:
				t.Fatalf("%s = %d (the previous login's is %d); want none: a deadline from another "+
					"login makes Claude warn on the wrong date, or not at all", refreshTokenExpiresAtKey, got, oldDeadline)
			case tc.want != 0 && !present:
				t.Fatalf("%s is absent, want %d", refreshTokenExpiresAtKey, tc.want)
			case tc.want != 0 && got != tc.want:
				t.Fatalf("%s = %d, want %d (the previous login's is %d)", refreshTokenExpiresAtKey, got, tc.want, oldDeadline)
			}
		})
	}
}

// TestABrokerRefreshTakesTheResponsesRefreshDeadlineAndOtherwiseKeepsTheLogins drives DoRefresh,
// the path every due access token takes, against a fake token endpoint.
func TestABrokerRefreshTakesTheResponsesRefreshDeadlineAndOtherwiseKeepsTheLogins(t *testing.T) {
	now := pinClock(t)
	loginDeadline := now + 20*86_400_000

	for _, tc := range []struct {
		name     string
		deadline int
		want     int64
	}{
		{"the response carries one", rtDeadlineSeconds, now + rtDeadlineSeconds*1000},
		{"the response carries none", 0, loginDeadline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tokenBody("AT_new", "RT_new", tc.deadline)))
			}))
			t.Cleanup(srv.Close)
			t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
			creds := singleFileStore(t)
			seedLoginWithDeadline(t, creds, "AT_old", "RT_old", 0, loginDeadline)

			if res := DoRefresh(creds); res != nil {
				if _, isErr := res.Get("error"); isErr {
					t.Fatalf("refresh errored: %v", res)
				}
			}
			if oa, _ := oauthFromCreds(creds); str(oa, "refreshToken") != "RT_new" {
				t.Fatal("the refresh did not rotate the refresh token: the test proved nothing")
			}
			got, present := rtDeadlineOf(t, creds)
			if !present || got != tc.want {
				t.Fatalf("%s = %d (present %v), want %d", refreshTokenExpiresAtKey, got, present, tc.want)
			}
		})
	}
}

// TestAnEnrolledLoginKeepsItsOwnRefreshDeadline drives the view path's enrollment: a /login in a
// view jail writes a whole credential into its view, and the broker redeems that refresh token
// once. The redemption is a refresh within the jail's login, so the canonical takes the jail
// login's deadline, not the machine's previous one and not none.
func TestAnEnrolledLoginKeepsItsOwnRefreshDeadline(t *testing.T) {
	f := newViewFixture(t) // its token endpoint answers without refresh_token_expires_in
	now := nowMS()
	machineDeadline, jailDeadline := now+86_400_000, now+27*86_400_000
	seedLoginWithDeadline(t, CanonicalPath, "AT_machine", "RT_machine", now+7*3600_000, machineDeadline)
	alpha := f.workspace(t, "alpha")
	seedLoginWithDeadline(t, alpha.Path(), "AT_jail", "RT_jail", now+8*3600_000, jailDeadline)

	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)

	if got := f.redemptions(); len(got) != 1 || got[0] != "RT_jail" {
		t.Fatalf("upstream was sent %v, want the jail's refresh token once: the test proved nothing", got)
	}
	got, present := rtDeadlineOf(t, CanonicalPath)
	if !present || got != jailDeadline {
		t.Fatalf("canonical %s = %d (present %v), want the enrolled login's %d (the machine's previous was %d)",
			refreshTokenExpiresAtKey, got, present, jailDeadline, machineDeadline)
	}
}

// TestAForcedRefreshKeepsTheLoginsRefreshDeadline drives `yolo claude-auth refresh`
// (ForceRefresh), a refresh of the machine's own login whose response here carries no deadline.
func TestAForcedRefreshKeepsTheLoginsRefreshDeadline(t *testing.T) {
	f := newViewFixture(t)
	deadline := nowMS() + 20*86_400_000
	seedLoginWithDeadline(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, deadline)

	if _, err := ForceRefresh(0); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if got := f.redemptions(); len(got) != 1 || got[0] != "RT_machine" {
		t.Fatalf("upstream was sent %v, want the machine's refresh token once: the test proved nothing", got)
	}
	got, present := rtDeadlineOf(t, CanonicalPath)
	if !present || got != deadline {
		t.Fatalf("canonical %s = %d (present %v), want the login's %d", refreshTokenExpiresAtKey, got, present, deadline)
	}
}
