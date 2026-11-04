package packload

import (
	"strings"
	"testing"
)

func TestMCPServerScopesUsesNameAndSignInIdentity(t *testing.T) {
	cases := []struct {
		name, effective, userOnly string
		level, reason             string
	}{
		{"user only", `{"linear":{"url":"https://mcp.example.test","transport":"http"}}`, `{"linear":{"url":"https://mcp.example.test"}}`, "user", "same name and sign-in identity"},
		{"workspace only", `{"linear":{"url":"https://mcp.example.test"}}`, `{}`, "workspace", "not present"},
		{"requires env is not identity", `{"linear":{"url":"https://mcp.example.test","requires_env":["KEY"]}}`, `{"linear":{"url":"https://mcp.example.test"}}`, "user", "same name and sign-in identity"},
		{"url differs", `{"linear":{"url":"https://staging.example.test"}}`, `{"linear":{"url":"https://mcp.example.test"}}`, "workspace", "its url differs from your user-level server"},
		{"headers differ", `{"linear":{"url":"https://mcp.example.test","headers":{"Org":"workspace"}}}`, `{"linear":{"url":"https://mcp.example.test","headers":{"Org":"user"}}}`, "workspace", "its headers differ from your user-level server"},
		{"transport differs", `{"linear":{"url":"https://mcp.example.test","transport":"sse"}}`, `{"linear":{"url":"https://mcp.example.test","transport":"http"}}`, "workspace", "its transport differs from your user-level server"},
		{"callback port is not identity", `{"linear":{"url":"https://mcp.example.test","oauth":{"client_id":"c1","callback_port":3000}}}`, `{"linear":{"url":"https://mcp.example.test","oauth":{"client_id":"c1"}}}`, "user", "same name and sign-in identity"},
		{"client differs", `{"linear":{"url":"https://mcp.example.test","oauth":{"client_id":"c2"}}}`, `{"linear":{"url":"https://mcp.example.test","oauth":{"client_id":"c1"}}}`, "workspace", "its OAuth client id differs"},
		{"same url different name", `{"notion-staging":{"url":"https://mcp.example.test"}}`, `{"notion":{"url":"https://mcp.example.test"}}`, "workspace", "not present"},
		{"stdio identity", `{"local":{"command":"npx","args":["server"],"env":{"K":"v"}}}`, `{"local":{"command":"npx","args":["server"],"env":{"K":"other"}}}`, "user", "same name and sign-in identity"},
		{"stdio args differ", `{"local":{"command":"npx","args":["other"]}}`, `{"local":{"command":"npx","args":["server"]}}`, "workspace", "its arguments differ"},
		{"removed", `{"linear":null}`, `{"linear":{"url":"https://mcp.example.test"}}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MCPServerScopes(userTable(t, tc.effective), userTable(t, tc.userOnly))
			name := "linear"
			if tc.name == "same url different name" {
				name = "notion-staging"
			}
			if tc.name == "stdio identity" || tc.name == "stdio args differ" {
				name = "local"
			}
			scope, exists := got[name]
			if tc.level == "" {
				if exists {
					t.Fatalf("removed server scope = %#v", scope)
				}
				return
			}
			if !exists || scope.Level != tc.level {
				t.Fatalf("scope = %#v (present=%v), want level %q", scope, exists, tc.level)
			}
			if tc.reason != "" && !strings.Contains(scope.Reason, tc.reason) {
				t.Errorf("reason = %q, want containing %q", scope.Reason, tc.reason)
			}
		})
	}
}
