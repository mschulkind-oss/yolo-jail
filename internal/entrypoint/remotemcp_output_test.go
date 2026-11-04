package entrypoint

import (
	"path/filepath"
	"testing"
)

const remoteMCPInput = `{"linear":{"url":"https://mcp.linear.app/mcp","headers":{"X-Org":"acme"},"oauth":{"client_id":"c1","scopes":["read"],"callback_port":3118}},"legacy":{"url":"https://sse.example.test/sse","transport":"sse"},"stdio":{"command":"mcp-fs","args":["--serve"]}}`

func TestClaudeRemoteMCPServerDialect(t *testing.T) {
	e := siblingEnv(t, map[string]string{"YOLO_MCP_SERVERS": remoteMCPInput})
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatal(err)
	}
	config := decodeJSONFile(t, filepath.Join(e.Home, ".claude.json"))
	servers, _ := config["mcpServers"].(map[string]any)
	linear, _ := servers["linear"].(map[string]any)
	if linear["type"] != "http" || linear["url"] != "https://mcp.linear.app/mcp" {
		t.Fatalf("linear = %#v", linear)
	}
	if got := linear["headers"].(map[string]any)["X-Org"]; got != "acme" {
		t.Errorf("headers = %#v", linear["headers"])
	}
	oauth, _ := linear["oauth"].(map[string]any)
	if oauth["clientId"] != "c1" || oauth["callbackPort"] != float64(3118) {
		t.Errorf("oauth = %#v", oauth)
	}
	if _, exists := linear["transport"]; exists {
		t.Errorf("transport key leaked: %#v", linear)
	}
	if _, exists := linear["scopes"]; exists {
		t.Errorf("scopes should not be rendered: %#v", linear)
	}
	legacy, _ := servers["legacy"].(map[string]any)
	if legacy["type"] != "sse" {
		t.Errorf("SSE dialect = %#v", legacy)
	}
	stdio, _ := servers["stdio"].(map[string]any)
	if stdio["command"] != "mcp-fs" {
		t.Errorf("stdio compatibility lost: %#v", stdio)
	}
}

func TestOpencodeRemoteMCPServerDialect(t *testing.T) {
	e := siblingEnv(t, map[string]string{"YOLO_MCP_SERVERS": remoteMCPInput})
	if err := ConfigurePackByName(e, "opencode"); err != nil {
		t.Fatal(err)
	}
	config := decodeJSONFile(t, filepath.Join(e.Home, ".config", "opencode", "opencode.json"))
	servers, _ := config["mcp"].(map[string]any)
	linear, _ := servers["linear"].(map[string]any)
	if linear["type"] != "remote" || linear["url"] != "https://mcp.linear.app/mcp" {
		t.Fatalf("linear = %#v", linear)
	}
	if got := linear["headers"].(map[string]any)["X-Org"]; got != "acme" {
		t.Errorf("headers = %#v", linear["headers"])
	}
	oauth, _ := linear["oauth"].(map[string]any)
	if oauth["clientId"] != "c1" {
		t.Errorf("oauth = %#v", oauth)
	}
	if _, exists := linear["scopes"]; exists {
		t.Errorf("scopes should not be rendered: %#v", linear)
	}
	legacy, _ := servers["legacy"].(map[string]any)
	if legacy["type"] != "remote" || legacy["url"] != "https://sse.example.test/sse" {
		t.Errorf("SSE dialect = %#v", legacy)
	}
	stdio, _ := servers["stdio"].(map[string]any)
	if stdio["type"] != "local" || stdio["command"].([]any)[0] != "mcp-fs" {
		t.Errorf("stdio compatibility lost: %#v", stdio)
	}
}
