package oauthbroker

// scopeslineage_test.go pins where the broker gets `scopes`, the claudeAiOauth list of the OAuth
// scopes a login's token carries. Claude Code reads it before it uses the token: without
// user:inference it reports `oauth_no_inference_scope`, and it checks it for user:plugins too.
//
// Claude's own rule, MEASURED in the 2.1.288 binary: a login's token exchange and a refresh both
// set the list from the response's `scope` string (search `scopes:Evn(e.scope)` and
// `Z=Evn(B.scope)`), and its save writes that list and never the stored one (search
// `scopes:n.scopes`). So a list is never carried from one login onto another.
//
// The broker used to keep the previous record's list whenever it had one, so its mirror of a
// /login wrote the previous LOGIN's scopes onto the new login's tokens, and every refresh after
// it kept them. It now takes the response's `scope` when there is one, and otherwise keeps the
// previous record's list only when the response rotated that record's own refresh token, a
// refresh within one login: the rule refreshTokenExpiresAt follows (refreshtokenexpiry_test.go).
//
// No real login: the token endpoint is an httptest server and every token is a fake.

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// seededScopes is the list seedLoginWithDeadline gives the previous login, and widerScope a
// response's `scope` that differs from it.
var seededScopes = []string{"user:inference", "user:profile"}

const widerScope = "user:inference user:profile user:sessions:claude_code user:plugins"

// scopesOf reads the scopes list from a credentials file; nil when the key is absent.
func scopesOf(t *testing.T, path string) []string {
	t.Helper()
	oa, err := oauthFromCreds(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	v, ok := oa.Get("scopes")
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("%s scopes = %#v, not a list", path, v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("%s scopes holds %#v, not a string", path, e)
		}
		out = append(out, s)
	}
	return out
}

// scopesText renders a scopes list for a failure message, an absent key as such.
func scopesText(s []string) string {
	if s == nil {
		return "(absent)"
	}
	return strings.Join(s, " ")
}

// scopedTokenBody is an upstream token response whose scope member is scopeJSON verbatim: ""
// leaves `scope` out.
func scopedTokenBody(at, rt, scopeJSON string) string {
	return `{"access_token":"` + at + `","refresh_token":"` + rt + `","expires_in":28800` + scopeJSON + `}`
}

// TestTheProxyMirrorNeverWritesAPreviousLoginsScopes drives the mirror the broker's proxy action
// runs after a token request a jail's Claude made through it (maybePropagateTokenResponse, called
// from BuildHandler's "proxy" case).
func TestTheProxyMirrorNeverWritesAPreviousLoginsScopes(t *testing.T) {
	now := pinClock(t)
	login := `{"grant_type":"authorization_code","code":"fake-code","client_id":"` + ClientID + `"}`
	refreshOf := func(rt string) string {
		return `{"grant_type":"refresh_token","refresh_token":"` + rt + `","client_id":"` + ClientID + `"}`
	}

	cases := []struct {
		name      string
		body      string   // the proxied request's body, as the jail's Claude sent it
		scopeJSON string   // the response's scope member, verbatim; "" for none
		want      []string // nil for "the key is absent"
	}{
		{"a /login takes its response's scopes", login,
			`,"scope":"` + widerScope + `"`, strings.Fields(widerScope)},
		{"a /login whose response carries no scope has none, not the previous login's", login,
			"", nil},
		{"a /login whose response's scope is empty has none, not the previous login's", login,
			`,"scope":""`, nil},
		{"a refresh of the machine's own token takes its response's scopes", refreshOf("RT_old"),
			`,"scope":"` + widerScope + `"`, strings.Fields(widerScope)},
		{"a refresh of the machine's own token whose response carries none keeps the login's", refreshOf("RT_old"),
			"", seededScopes},
		{"a refresh of some other login's token does not inherit the machine's scopes", refreshOf("RT_elsewhere"),
			"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds := singleFileStore(t)
			seedLoginWithDeadline(t, creds, "AT_old", "RT_old", now-60_000, now+2*86_400_000)
			resp := jsonx.NewOrderedMap()
			resp.Set("status", jsonx.IntValue(200))
			resp.Set("body_b64", base64.StdEncoding.EncodeToString([]byte(scopedTokenBody("AT_new", "RT_new", tc.scopeJSON))))

			maybePropagateTokenResponse(creds,
				proxyRequest{method: "POST", path: "/v1/oauth/token", body: []byte(tc.body)}, resp)

			if oa, _ := oauthFromCreds(creds); str(oa, "refreshToken") != "RT_new" {
				t.Fatal("the mirror did not write the response's tokens: the test proved nothing")
			}
			if got := scopesOf(t, creds); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scopes = %s, want %s (the previous login's are %s): Claude decides from this "+
					"list whether the token may run inference", scopesText(got), scopesText(tc.want), scopesText(seededScopes))
			}
		})
	}
}

// TestABrokerRefreshTakesTheResponsesScopesAndOtherwiseKeepsTheLogins drives DoRefresh, the path
// every due access token takes, against a fake token endpoint.
func TestABrokerRefreshTakesTheResponsesScopesAndOtherwiseKeepsTheLogins(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scopeJSON string
		want      []string
	}{
		{"the response carries a scope", `,"scope":"` + widerScope + `"`, strings.Fields(widerScope)},
		{"the response carries none", "", seededScopes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(scopedTokenBody("AT_new", "RT_new", tc.scopeJSON)))
			}))
			t.Cleanup(srv.Close)
			t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
			creds := singleFileStore(t)
			seedLoginWithDeadline(t, creds, "AT_old", "RT_old", 0, nowMS()+20*86_400_000)

			if res := DoRefresh(creds); res != nil {
				if _, isErr := res.Get("error"); isErr {
					t.Fatalf("refresh errored: %v", res)
				}
			}
			if oa, _ := oauthFromCreds(creds); str(oa, "refreshToken") != "RT_new" {
				t.Fatal("the refresh did not rotate the refresh token: the test proved nothing")
			}
			if got := scopesOf(t, creds); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scopes = %s, want %s", scopesText(got), scopesText(tc.want))
			}
		})
	}
}
