package luahook

import (
	"os"
	"path/filepath"
	"testing"
)

// `sign_in` is yolo's own vocabulary (MA-D11), like `provides`: a derive that copies an entry
// verbatim must not land it in an agent's config file, so the ctx boundary strips it.
func TestDeriveCtxStripsSignIn(t *testing.T) {
	script := `
yolo.derive("claude", "config", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do servers[name] = cfg end
  return { mcpServers = servers }
end)`
	ctx := &DeriveCtx{Agent: "claude", Surface: "config", RemoteMCPTransports: []string{"http"},
		Tables: map[string]map[string]any{"mcp_servers": {
			"slack": map[string]any{"url": "https://mcp.slack.com/mcp", "sign_in": "once"},
		}}}
	out, err := GopherLuaVM{}.Derive(script, ctx)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := out["mcpServers"].(map[string]any)
	slack, _ := servers["slack"].(map[string]any)
	if slack == nil || slack["url"] != "https://mcp.slack.com/mcp" {
		t.Fatalf("slack = %v, want the server delivered", servers)
	}
	if _, leaked := slack["sign_in"]; leaked {
		t.Fatalf("sign_in reached the derive: %v", slack)
	}
	// The caller's table is the live config, reused across surfaces: it must be untouched.
	if ctx.Tables["mcp_servers"]["slack"].(map[string]any)["sign_in"] != "once" {
		t.Fatal("the strip edited the caller's table")
	}
}

// A match function may name the entry's issuer, which the broker compares against the
// authorization server it discovers before it sends a refresh token anywhere (§9.3).
func TestMCPAuthMatchReturnsTheIssuer(t *testing.T) {
	script := `yolo.mcp_auth("test", {
  key = function() return "key" end,
  match = function(key, entry)
    return {name = entry.serverName, url = entry.serverUrl, issuer = entry.issuer}
  end,
})`
	m, err := MCPAuthMatch(script, "test", "k", map[string]any{"serverName": "s", "serverUrl": "https://x.test/mcp", "issuer": "https://x.test"})
	if err != nil || m == nil || m.Issuer != "https://x.test" {
		t.Fatalf("match = %+v, %v; want the issuer", m, err)
	}
	m, err = MCPAuthMatch(script, "test", "k", map[string]any{"serverName": "s", "serverUrl": "https://x.test/mcp"})
	if err != nil || m == nil || m.Issuer != "" {
		t.Fatalf("match without issuer = %+v, %v; want an empty issuer", m, err)
	}
}

// Claude Code keys `mcpOAuth` by name and the first 16 hex of SHA-256 over {type,url,headers}
// in that member order. The expected key is the one MEASURED on 2026-11-06 in a real store for
// the owner's Slack plugin server (a hash of public data, not a credential).
func TestShippedClaudeMCPAuthKeyAndMatch(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "packs", "claude", "derive.lua"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := MCPAuthKeys(string(src), "claude", []MCPServerRef{{Name: "plugin:slack:slack", URL: "https://mcp.slack.com/mcp", Transport: "http"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := keys["plugin:slack:slack"]; got != "plugin:slack:slack|38801a7d845718b3" {
		t.Fatalf("key = %q, want the measured plugin:slack:slack|38801a7d845718b3", got)
	}
	m, err := MCPAuthMatch(string(src), "claude", "plugin:slack:slack|38801a7d845718b3", map[string]any{
		"serverName": "plugin:slack:slack", "serverUrl": "https://mcp.slack.com/mcp", "issuer": "https://mcp.slack.com",
		"accessToken": "never-read"})
	if err != nil || m == nil || m.Name != "plugin:slack:slack" || m.URL != "https://mcp.slack.com/mcp" || m.Issuer != "https://mcp.slack.com" {
		t.Fatalf("match = %+v, %v", m, err)
	}
	if m, err := MCPAuthMatch(string(src), "claude", "x|0123456789abcdef", map[string]any{"serverName": "x"}); err != nil || m != nil {
		t.Fatalf("an entry with no server URL matched: %+v, %v", m, err)
	}
}
