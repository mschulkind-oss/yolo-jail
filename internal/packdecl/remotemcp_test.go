package packdecl

import (
	"strings"
	"testing"
)

func TestGroupedRemoteMCPTransportAndEntry(t *testing.T) {
	data := []byte(`mcp_remote_transports = ["http", "sse"]
[[mcp]]
name = "linear"
config = { url = "https://mcp.linear.app/mcp", transport = "http", headers = { X-Org = "acme" }, oauth = { client_id = "c1", scopes = ["read"], callback_port = 3118 } }
`)
	manifest, problems := DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	if got := manifest.MCPRemoteTransports(); len(got) != 2 || got[0] != "http" || got[1] != "sse" {
		t.Fatalf("remote transports = %v", got)
	}
	servers := manifest.MCPContributions()
	if len(servers) != 1 || !strings.Contains(string(servers[0].Entry), `"url":"https://mcp.linear.app/mcp"`) {
		t.Fatalf("MCP entries = %#v", servers)
	}
}

func TestPackStdioMCPEntryRejectsRemoteOnlyFields(t *testing.T) {
	_, problems := DecodeTOML([]byte(`[[mcp]]
name = "local"
config = { command = "npx", oauth = { client_id = "c1" } }
`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `"config"."oauth" applies only to a remote server (url)`) {
		t.Fatalf("remote-only field was accepted on stdio MCP entry: %s", got)
	}
}

func TestMCPRemoteTransportsRejectUnknownTransport(t *testing.T) {
	_, problems := DecodeTOML([]byte(`mcp_remote_transports = ["ws"]
`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `mcp_remote_transports: "ws" is not a transport yolo renders ("http", "sse")`) {
		t.Fatalf("unknown transport not refused: %s", got)
	}
}
