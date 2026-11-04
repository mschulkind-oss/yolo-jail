package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestRemoteMCPDestinationOverrideDoesNotInheritHeaders(t *testing.T) {
	base := decode(t, `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","headers":{"Authorization":"Bearer user-secret","X-Org":"acme"}}}}`)
	for _, tc := range []struct {
		name, override, want string
	}{
		{"foreign host", `{"mcp_servers":{"linear":{"url":"https://foreign.example/mcp"}}}`, `"url":"https://foreign.example/mcp"`},
		{"scheme", `{"mcp_servers":{"linear":{"url":"http://mcp.linear.app/mcp"}}}`, `"url":"http://mcp.linear.app/mcp"`},
		{"port", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app:9443/mcp"}}}`, `"url":"https://mcp.linear.app:9443/mcp"`},
		{"path", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/other"}}}`, `"url":"https://mcp.linear.app/other"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := MergeConfig(base, decode(t, tc.override))
			servers, _ := merged.Get("mcp_servers")
			serverMap, _ := asMap(servers)
			linear, _ := serverMap.Get("linear")
			entry, _ := asMap(linear)
			if headers, ok := entry.Get("headers"); ok {
				t.Fatalf("destination override retained headers %v", headers)
			}
			if url, _ := entry.Get("url"); url == nil || !strings.Contains(url.(string), strings.Split(tc.want, `"`)[3]) {
				t.Fatalf("url = %v, want %s", url, tc.want)
			}
		})
	}
	unchanged := MergeConfig(base, decode(t, `{"mcp_servers":{"linear":{"requires_env":["TOKEN"]}}}`))
	servers, _ := unchanged.Get("mcp_servers")
	serverMap, _ := asMap(servers)
	linear, _ := serverMap.Get("linear")
	entry, _ := asMap(linear)
	if headers, ok := entry.Get("headers"); !ok || headers == nil {
		t.Fatalf("same-destination merge lost headers: %v", headers)
	}

	transportChanged := MergeConfig(base, decode(t, `{"mcp_servers":{"linear":{"transport":"sse"}}}`))
	servers, _ = transportChanged.Get("mcp_servers")
	serverMap, _ = asMap(servers)
	linear, _ = serverMap.Get("linear")
	entry, _ = asMap(linear)
	if got, _ := entry.Get("transport"); got != "sse" {
		t.Fatalf("transport-only override = %v, want sse", got)
	}
	if headers, ok := entry.Get("headers"); ok {
		t.Fatalf("transport-only destination change retained inherited headers: %v", headers)
	}

	explicitHeaders := MergeConfig(base, decode(t, `{"mcp_servers":{"linear":{"transport":"sse","headers":{"Authorization":"Bearer replacement"}}}}`))
	servers, _ = explicitHeaders.Get("mcp_servers")
	serverMap, _ = asMap(servers)
	linear, _ = serverMap.Get("linear")
	entry, _ = asMap(linear)
	if headersValue, _ := entry.Get("headers"); headersValue == nil {
		t.Fatalf("explicit replacement headers were not retained: %v", headersValue)
	} else if headers, ok := asMap(headersValue); !ok {
		t.Fatalf("replacement headers are not an object: %v", headersValue)
	} else if authorization, _ := headers.Get("Authorization"); authorization != "Bearer replacement" {
		t.Fatalf("replacement Authorization = %v", authorization)
	}

	baseSSE := decode(t, `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","transport":"sse","headers":{"Authorization":"Bearer user-secret"}}}}`)
	sameURL := MergeConfig(baseSSE, decode(t, `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp"}}}`))
	servers, _ = sameURL.Get("mcp_servers")
	serverMap, _ = asMap(servers)
	linear, _ = serverMap.Get("linear")
	entry, _ = asMap(linear)
	if got, _ := entry.Get("transport"); got != "sse" {
		t.Fatalf("same-URL override without transport lost inherited SSE transport: %v", got)
	}
	if headers, ok := entry.Get("headers"); !ok || headers == nil {
		t.Fatalf("same-URL override without transport lost inherited SSE headers: %v", headers)
	}

	stdio := MergeConfig(base, decode(t, `{"mcp_servers":{"linear":{"url":null,"command":"node"}}}`))
	servers, _ = stdio.Get("mcp_servers")
	serverMap, _ = asMap(servers)
	linear, _ = serverMap.Get("linear")
	entry, _ = asMap(linear)
	for _, key := range []string{"url", "transport", "headers", "oauth"} {
		if _, ok := entry.Get(key); ok {
			t.Errorf("remote-to-stdio transition retained %s: %v", key, entry)
		}
	}
	stdioBase := decode(t, `{"mcp_servers":{"local":{"command":"node","args":["mcp.js"],"env":{"X":"1"}}}}`)
	remote := MergeConfig(stdioBase, decode(t, `{"mcp_servers":{"local":{"url":"https://remote.example/mcp"}}}`))
	servers, _ = remote.Get("mcp_servers")
	serverMap, _ = asMap(servers)
	local, _ := serverMap.Get("local")
	entry, _ = asMap(local)
	for _, key := range []string{"command", "args", "env"} {
		if _, ok := entry.Get(key); ok {
			t.Errorf("stdio-to-remote transition retained %s: %v", key, entry)
		}
	}
}

func TestReadableConfigDoesNotCarryUserMCPHeadersToWorkspaceDestination(t *testing.T) {
	home := useProfileKeysHome(t)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := `[mcp_servers.linear]
url = "https://user.example/mcp"
headers = { Authorization = "Bearer synthetic-user-secret", X-Org = "user-org" }
`
	if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	project := `[mcp_servers.linear]
url = "https://foreign.example/mcp"
`
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := got.Get("mcp_servers")
	serverMap, _ := asMap(servers)
	linear, _ := serverMap.Get("linear")
	entry, _ := asMap(linear)
	if headers, ok := entry.Get("headers"); ok {
		t.Fatalf("workspace URL inherited user headers: %v", headers)
	}
}

func TestReadableConfigMergesMCPTransportAgainstEffectiveDestination(t *testing.T) {
	for _, tc := range []struct {
		name, user, workspace string
		wantHeader            bool
		wantTransport         string
	}{
		{
			name: "transport-only destination change clears inherited auth",
			user: `[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
headers = { Authorization = "Bearer readable-user-secret" }
`,
			workspace: `[mcp_servers.linear]
transport = "sse"
`,
			wantTransport: "sse",
		},
		{
			name: "same URL with omitted transport inherits SSE and headers",
			user: `[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
transport = "sse"
headers = { Authorization = "Bearer readable-user-secret" }
`,
			workspace: `[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
`,
			wantHeader: true, wantTransport: "sse",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := useProfileKeysHome(t)
			userDir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(userDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte(tc.user), 0o600); err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(tc.workspace), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadReadableConfig(workspace, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			servers, _ := got.Get("mcp_servers")
			serverMap, _ := asMap(servers)
			linear, _ := serverMap.Get("linear")
			entry, _ := asMap(linear)
			if transport, _ := entry.Get("transport"); transport != tc.wantTransport {
				t.Fatalf("effective transport = %v, want %s", transport, tc.wantTransport)
			}
			headers, hasHeaders := entry.Get("headers")
			if hasHeaders != tc.wantHeader {
				t.Fatalf("effective headers = %v, present=%v, want present=%v", headers, hasHeaders, tc.wantHeader)
			}
		})
	}
}

func TestValidateRemoteMCPServers(t *testing.T) {
	cases := []struct{ name, body, wantErr string }{
		{"remote ok", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","headers":{"X-Org":"acme"},"oauth":{"client_id":"c1","scopes":["read"],"callback_port":3118}}}}`, ""},
		{"loopback http ok", `{"mcp_servers":{"dev":{"url":"http://127.0.0.1:8808/mcp"}}}`, ""},
		{"both", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","command":"npx"}}}`, "has both command and url"},
		{"neither", `{"mcp_servers":{"linear":{"requires_env":["X"]}}}`, "needs command (a local server) or url (a remote one)"},
		{"args with url", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","args":["-y"]}}}`, "applies only to a local server (command)"},
		{"oauth with stdio", `{"mcp_servers":{"local":{"command":"npx","oauth":{"client_id":"c1"}}}}`, "oauth: applies only to a remote server (url)"},
		{"bad transport", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","transport":"ws"}}}`, `must be "http" or "sse", got "ws"`},
		{"plain http", `{"mcp_servers":{"linear":{"url":"http://mcp.linear.app/mcp"}}}`, `must be an https:// URL (http:// only for localhost)`},
		{"port range", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","oauth":{"callback_port":70000}}}}`, "expected a port from 1 to 65535"},
		{"headers not strings", `{"mcp_servers":{"linear":{"url":"https://mcp.linear.app/mcp","headers":{"X-Org":3}}}}`, "headers.X-Org: expected a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := ValidateConfig(decode(t, tc.body), t.TempDir(), nil)
			joined := strings.Join(errs, "\n")
			if tc.wantErr == "" {
				if strings.Contains(joined, "config.mcp_servers") {
					t.Fatalf("valid remote server errors: %s", joined)
				}
			} else if !strings.Contains(joined, tc.wantErr) {
				t.Fatalf("errors %q do not contain %q", joined, tc.wantErr)
			}
		})
	}
}

func TestValidateRemoteMCPServersJSONC(t *testing.T) {
	path := filepath.Join(t.TempDir(), paths.WorkspaceConfigLegacyJSONCName)
	body := `{
  // Remote MCP declarations are valid in the transitional JSONC reader.
  "mcp_servers": {"linear": {"url": "https://mcp.linear.app/mcp", "headers": {"X-Org": "acme"}}},
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadJSONCFile(path, path, true, nil)
	if err != nil {
		t.Fatalf("read JSONC remote MCP server: %v", err)
	}
	errs, _ := ValidateConfig(config, t.TempDir(), nil)
	for _, message := range errs {
		if strings.HasPrefix(message, "config.mcp_servers") {
			t.Errorf("JSONC remote server error: %s", message)
		}
	}
}

func TestReadableRemoteMCPServerTOMLAndRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	body := `[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
headers = { X-Org = "acme" }
oauth = { client_id = "c1", scopes = ["read"], callback_port = 3118 }
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReadableWorkspaceConfig(dir, true, nil); err != nil {
		t.Fatalf("read remote MCP TOML: %v", err)
	}
	if err := os.WriteFile(path, []byte("mcp_servers.linear = 'off'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := LoadReadableWorkspaceConfig(dir, true, nil)
	if err != nil {
		t.Fatalf("read remote MCP removal: %v", err)
	}
	value, _ := removed.Get("mcp_servers")
	servers, ok := asMap(value)
	if !ok {
		t.Fatalf("removed MCP server table = %#v", value)
	}
	if entry, present := servers.Get("linear"); !present || entry != "off" {
		t.Fatalf("off removal marker = %v, present=%v", entry, present)
	}
	composed := packload.ComposeMCPServers(servers, nil, dir)
	if entry, present := composed.Get("linear"); !present || entry != nil {
		t.Fatalf("composition did not lower MCP off to a removal: %v, present=%v", entry, present)
	}
	if scopes := packload.MCPServerScopes(composed, nil); len(scopes) != 0 {
		t.Fatalf("removed server still has a scope: %#v", scopes)
	}
}
