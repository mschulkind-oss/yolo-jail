package oauthbroker

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestHasInferenceScope(t *testing.T) {
	cases := []struct {
		scope string
		want  bool
	}{
		{"user:inference user:profile", true},
		{"user:file_upload user:inference user:mcp_servers user:profile user:sessions:claude_code", true},
		{"workspace:inference", true},
		{"user:developer", true},
		{"workspace:developer", true},
		{"workspace:messages_create", true},
		{"user:ccr_inference", true},
		{"org:service_key_inference", true},
		{"user:voice", true},
		{"user:design user:preview", false},
		{"user:profile user:email", false},
		{"read:design write:design", false},
		{"", false},
	}
	for _, tc := range cases {
		got := HasInferenceScope(tc.scope)
		if got != tc.want {
			t.Errorf("HasInferenceScope(%q) = %v, want %v", tc.scope, got, tc.want)
		}
	}
}

func TestIsClaudeCodeClientID(t *testing.T) {
	cases := []struct {
		req  proxyRequest
		want bool
	}{
		{
			req:  proxyRequest{path: "/v1/oauth/token", body: []byte(`{"client_id":"` + ClientID + `","grant_type":"authorization_code"}`)},
			want: true,
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token?client_id=" + ClientID, body: nil},
			want: true,
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token", body: []byte(`client_id=` + ClientID + `&grant_type=authorization_code`)},
			want: true,
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token", body: []byte(`{"grant_type":"authorization_code"}`)},
			want: true, // omitted client_id defaults to true
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token", body: []byte(`{"client_id":"other-app-id","grant_type":"authorization_code"}`)},
			want: false,
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token?client_id=other-app-id", body: nil},
			want: false,
		},
		{
			req:  proxyRequest{path: "/v1/oauth/token", body: []byte(`client_id=other-app-id&grant_type=authorization_code`)},
			want: false,
		},
	}
	for i, tc := range cases {
		got := isClaudeCodeClientID(tc.req)
		if got != tc.want {
			t.Errorf("case %d: isClaudeCodeClientID(%+v) = %v, want %v", i, tc.req, got, tc.want)
		}
	}
}

func TestMaybePropagateTokenResponse(t *testing.T) {
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "creds.json")
	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	defer func() { RefreshLockPath = "" }()

	seedCreds := func(at, rt string) {
		root := jsonx.NewOrderedMap()
		oa := jsonx.NewOrderedMap()
		oa.Set("accessToken", at)
		oa.Set("refreshToken", rt)
		oa.Set("expiresAt", jsonx.IntValue(100000))
		oa.Set("scopes", []any{"user:inference"})
		oa.Set("subscriptionType", "max")
		oa.Set("rateLimitTier", "default_claude_max_20x")
		root.Set("claudeAiOauth", oa)
		blob, err := jsonx.DumpsIndent(root, 2)
		if err != nil {
			t.Fatalf("dumps: %v", err)
		}
		if err := os.WriteFile(credsPath, []byte(blob), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	makeResponse := func(status int, bodyJSON string) *jsonx.OrderedMap {
		resp := jsonx.NewOrderedMap()
		resp.Set("status", jsonx.IntValue(int64(status)))
		resp.Set("body_b64", base64.StdEncoding.EncodeToString([]byte(bodyJSON)))
		return resp
	}

	t.Run("skips_when_scope_lacks_inference", func(t *testing.T) {
		seedCreds("primary-at", "primary-rt")
		req := proxyRequest{
			method: "POST",
			path:   "/v1/oauth/token",
			body:   []byte(`{"client_id":"` + ClientID + `"}`),
		}
		resp := makeResponse(200, `{"access_token":"design-at","refresh_token":"design-rt","expires_in":3600,"scope":"user:design user:preview"}`)

		maybePropagateTokenResponse(credsPath, req, resp)

		after, err := oauthFromCreds(credsPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if v, _ := after.Get("accessToken"); v != "primary-at" {
			t.Fatalf("accessToken overwritten to %v, want primary-at", v)
		}
		if v, _ := after.Get("refreshToken"); v != "primary-rt" {
			t.Fatalf("refreshToken overwritten to %v, want primary-rt", v)
		}
	})

	t.Run("skips_when_client_id_mismatched", func(t *testing.T) {
		seedCreds("primary-at", "primary-rt")
		req := proxyRequest{
			method: "POST",
			path:   "/v1/oauth/token",
			body:   []byte(`{"client_id":"design-app-client-id"}`),
		}
		resp := makeResponse(200, `{"access_token":"design-at","refresh_token":"design-rt","expires_in":3600,"scope":"user:inference"}`)

		maybePropagateTokenResponse(credsPath, req, resp)

		after, err := oauthFromCreds(credsPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if v, _ := after.Get("accessToken"); v != "primary-at" {
			t.Fatalf("accessToken overwritten to %v, want primary-at", v)
		}
	})

	t.Run("mirrors_valid_inference_token", func(t *testing.T) {
		seedCreds("old-at", "old-rt")
		req := proxyRequest{
			method: "POST",
			path:   "/v1/oauth/token",
			body:   []byte(`{"client_id":"` + ClientID + `"}`),
		}
		resp := makeResponse(200, `{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600,"scope":"user:inference user:profile"}`)

		maybePropagateTokenResponse(credsPath, req, resp)

		after, err := oauthFromCreds(credsPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if v, _ := after.Get("accessToken"); v != "new-at" {
			t.Fatalf("accessToken = %v, want new-at", v)
		}
		if v, _ := after.Get("refreshToken"); v != "new-rt" {
			t.Fatalf("refreshToken = %v, want new-rt", v)
		}
		if v, _ := after.Get("subscriptionType"); v != "max" {
			t.Fatalf("subscriptionType lost: %v", v)
		}
		if v, _ := after.Get("rateLimitTier"); v != "default_claude_max_20x" {
			t.Fatalf("rateLimitTier lost: %v", v)
		}
	})
}
