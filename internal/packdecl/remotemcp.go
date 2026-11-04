package packdecl

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
)

// MCPRemoteTransports returns the remote transports this pack declares its derives can render.
func (m *Manifest) MCPRemoteTransports() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.RemoteMCPTransportsDecl...)
}

func (m *Manifest) validateMCPRemoteTransports() []string {
	if m == nil {
		return nil
	}
	var problems []string
	for _, transport := range m.RemoteMCPTransportsDecl {
		if transport != "http" && transport != "sse" {
			problems = append(problems, fmt.Sprintf("mcp_remote_transports: %q is not a transport yolo renders (\"http\", \"sse\")", transport))
		}
	}
	return problems
}

func mcpRemoteEntryProblems(label string, entry map[string]json.RawMessage) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(label+": "+format, args...)) }
	var endpoint string
	if err := json.Unmarshal(entry["url"], &endpoint); err != nil || endpoint == "" {
		add("\"url\" must be a non-empty string")
	} else if parsed, err := url.Parse(endpoint); err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		add("\"url\" must be an absolute https:// URL")
	} else if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackHost(parsed.Hostname())) {
		add("\"url\" must be https:// (http:// only for localhost)")
	}
	if raw, ok := entry["transport"]; ok {
		var transport string
		if err := json.Unmarshal(raw, &transport); err != nil || (transport != "http" && transport != "sse") {
			add("\"transport\" must be \"http\" or \"sse\"")
		}
	}
	if raw, ok := entry["headers"]; ok {
		var headers map[string]string
		if err := json.Unmarshal(raw, &headers); err != nil {
			add("\"headers\" must be an object of strings")
		}
	}
	if raw, ok := entry["oauth"]; ok {
		var oauth map[string]json.RawMessage
		if err := json.Unmarshal(raw, &oauth); err != nil || oauth == nil {
			add("\"oauth\" must be an object")
		} else {
			for _, key := range sortedKeys(oauth) {
				if key != "client_id" && key != "scopes" && key != "callback_port" {
					add("\"oauth\" has unknown key %q", key)
				}
			}
			var clientID string
			if raw, ok := oauth["client_id"]; ok && json.Unmarshal(raw, &clientID) != nil {
				add("\"oauth\".\"client_id\" must be a string")
			}
			var scopes []string
			if raw, ok := oauth["scopes"]; ok && json.Unmarshal(raw, &scopes) != nil {
				add("\"oauth\".\"scopes\" must be a list of strings")
			}
			var port *int
			if raw, ok := oauth["callback_port"]; ok {
				var value int
				if json.Unmarshal(raw, &value) != nil || value < 1 || value > 65535 {
					add("\"oauth\".\"callback_port\" must be from 1 to 65535")
				} else {
					port = &value
				}
			}
			_ = port
		}
	}
	return out
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
