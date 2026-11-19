package mcpsignin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Server is inventory only. Reason nonempty means it cannot become a point.
type Server struct {
	Key          string `json:"key"`
	URL          string `json:"url,omitempty"`
	Transport    string `json:"transport,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	CallbackPort int    `json:"callback_port,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// Inventory retains identity content for delivery preservation guards.
type Inventory struct {
	ManifestName string
	Servers      []Server
	Problems     []string
	RootMCP      []byte
	ManifestMCP  json.RawMessage
}

// ReadInventory dispatches by content format, never an agent or server name.
func ReadInventory(reader FileReader, bundle, format string) Inventory {
	if format != "claude-plugin" {
		return Inventory{Problems: []string{"unsupported format"}}
	}
	if !fs.ValidPath(bundle) {
		return Inventory{Problems: []string{"bundle path escape refused"}}
	}
	var out Inventory
	body, err := reader.ReadFile(path.Join(bundle, ".claude-plugin/plugin.json"))
	if err != nil {
		out.Problems = append(out.Problems, "manifest unreadable: "+err.Error())
		return out
	}
	value, err := decodeUnique(body)
	if err != nil {
		out.Problems = append(out.Problems, "malformed manifest: "+err.Error())
		return out
	}
	manifest, ok := value.(map[string]any)
	if !ok {
		out.Problems = append(out.Problems, "malformed manifest: expected object")
		return out
	}
	out.ManifestName, _ = manifest["name"].(string)
	if out.ManifestName == "" {
		out.Problems = append(out.Problems, "malformed manifest: missing name")
		return out
	}
	sources := []map[string]any{}
	root, err := reader.ReadFile(path.Join(bundle, ".mcp.json"))
	if err == nil {
		out.RootMCP = append([]byte(nil), root...)
		source, e := serverMap(root)
		if e != nil {
			out.Problems = append(out.Problems, "malformed root MCP: "+e.Error())
		} else {
			sources = append(sources, source)
		}
	} else if !isMissing(err) {
		out.Problems = append(out.Problems, "root MCP unreadable: "+err.Error())
	}
	if v, exists := manifest["mcpServers"]; exists {
		out.ManifestMCP, _ = json.Marshal(v)
		var source map[string]any
		switch v := v.(type) {
		case map[string]any:
			source = v
		case string:
			if !fs.ValidPath(v) || v == "." || strings.Contains(v, "\\") {
				out.Problems = append(out.Problems, "manifest MCP path escape refused")
				break
			}
			data, e := reader.ReadFile(path.Join(bundle, v))
			if e != nil {
				out.Problems = append(out.Problems, "manifest MCP unreadable: "+e.Error())
				break
			}
			source, e = serverMap(data)
			if e != nil {
				out.Problems = append(out.Problems, "malformed manifest MCP: "+e.Error())
			}
		default:
			out.Problems = append(out.Problems, "unsupported manifest MCP shape")
		}
		if source != nil {
			sources = append(sources, source)
		}
	}
	entries := map[string][]any{}
	for _, source := range sources {
		for key, v := range source {
			entries[key] = append(entries[key], v)
		}
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(entries[key]) != 1 {
			out.Servers = append(out.Servers, Server{Key: key, Reason: "ambiguous"})
			continue
		}
		s, ignore := parseServer(key, entries[key][0])
		if !ignore {
			out.Servers = append(out.Servers, s)
		}
	}
	// Partial parse is not evidence of a unique identity across all sources.
	if len(out.Problems) > 0 {
		for i := range out.Servers {
			if out.Servers[i].Reason == "" {
				out.Servers[i].Reason = "bundle not usable"
			}
		}
	}
	return out
}
func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
func serverMap(data []byte) (map[string]any, error) {
	v, err := decodeUnique(data)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	if wrapper, exists := m["mcpServers"]; exists {
		if len(m) != 1 {
			return nil, fmt.Errorf("unsupported wrapper fields")
		}
		m, ok = wrapper.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected server map")
		}
	}
	return m, nil
}
func parseServer(key string, value any) (Server, bool) {
	s := Server{Key: key, Transport: "http"}
	m, ok := value.(map[string]any)
	if !ok {
		s.Reason = "unsupported shape"
		return s, false
	}
	if _, command := m["command"]; command {
		return s, true
	}
	if m["type"] == "stdio" {
		return s, true
	}
	refuse := func() (Server, bool) { s.Reason = "unsupported shape"; return s, false }
	for k := range m {
		if k != "url" && k != "type" && k != "oauth" {
			return refuse()
		}
	}
	s.URL, ok = m["url"].(string)
	if !ok || strings.Contains(s.URL, "${") {
		return refuse()
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return refuse()
	}
	if transport, exists := m["type"]; exists {
		s.Transport, ok = transport.(string)
		if !ok {
			return refuse()
		}
	}
	if s.Transport != "http" && s.Transport != "sse" {
		return refuse()
	}
	if raw, exists := m["oauth"]; exists {
		oauth, ok := raw.(map[string]any)
		if !ok {
			return refuse()
		}
		for k := range oauth {
			if k != "clientId" && k != "callbackPort" {
				return refuse()
			}
		}
		if id, exists := oauth["clientId"]; exists {
			s.ClientID, ok = id.(string)
			if !ok || s.ClientID == "" || strings.Contains(s.ClientID, "${") {
				return refuse()
			}
		}
		if rawPort, exists := oauth["callbackPort"]; exists {
			number, ok := rawPort.(json.Number)
			if !ok {
				return refuse()
			}
			port, e := number.Int64()
			if e != nil || port < 1024 || port > 65535 {
				return refuse()
			}
			s.CallbackPort = int(port)
		}
	}
	return s, false
}

// JSON objects are parsed token-by-token: map last-wins must never bless a
// duplicate server or identity field, including duplicates inside one source.
func decodeUnique(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := uniqueValue(d)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return v, nil
}
func uniqueValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := t.(json.Delim)
	if !compound {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			t, e := d.Token()
			if e != nil {
				return nil, e
			}
			k, ok := t.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := m[k]; exists {
				return nil, fmt.Errorf("ambiguous duplicate key %q", k)
			}
			v, e := uniqueValue(d)
			if e != nil {
				return nil, e
			}
			m[k] = v
		}
		_, err = d.Token()
		return m, err
	case '[':
		var a []any
		for d.More() {
			v, e := uniqueValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}
