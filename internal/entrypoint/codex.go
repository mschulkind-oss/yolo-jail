package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// [A-Za-z0-9_-]+.
var codexBareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func codexTomlKey(key string) string {
	if codexBareKeyRe.MatchString(key) {
		return key
	}
	return `"` + codexTomlEscape(key) + `"`
}

func codexTomlEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// int/float -> Python str, else quoted escaped string.
func codexTomlScalar(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return pyStr(t)
	default:
		// jsonInt and other integer literals: Python str(int) == the digits.
		if isJSONInt(v) {
			return pyStr(v)
		}
		return `"` + codexTomlEscape(pyScalarStr(v)) + `"`
	}
}

// pyScalarStr renders str(v) for the scalar path in _toml_scalar: a Python str
// stays itself; anything reaching here that isn't int/float/bool is coerced via
// str() — in practice always a string.
func pyScalarStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyStr(v)
}

func codexTomlInlineTable(d *jsonx.OrderedMap) string {
	parts := make([]string, 0, d.Len())
	for _, k := range d.Keys() {
		v, _ := d.Get(k)
		parts = append(parts, codexTomlKey(k)+" = "+codexTomlScalar(v))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func codexTomlArray(values []any) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, codexTomlScalar(v))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// scalars/arrays first (skipping mcp_servers and dropping non-mcp_servers
// tables with a warning), then the [mcp_servers.<name>] sub-tables.
func (e *Env) dumpCodexTOML(doc *jsonx.OrderedMap) string {
	var lines []string
	var dropped []string
	for _, key := range doc.Keys() {
		if key == "mcp_servers" {
			continue
		}
		val, _ := doc.Get(key)
		switch t := val.(type) {
		case *jsonx.OrderedMap:
			dropped = append(dropped, key)
			continue
		case []any:
			lines = append(lines, codexTomlKey(key)+" = "+codexTomlArray(t))
		default:
			lines = append(lines, codexTomlKey(key)+" = "+codexTomlScalar(val))
		}
	}
	if mcpVal, ok := doc.Get("mcp_servers"); ok {
		if mcp, isMap := mcpVal.(*jsonx.OrderedMap); isMap {
			for _, name := range mcp.Keys() {
				cfgVal, _ := mcp.Get(name)
				cfg, isCfgMap := cfgVal.(*jsonx.OrderedMap)
				if !isCfgMap {
					continue
				}
				lines = append(lines, "")
				lines = append(lines, "[mcp_servers."+codexTomlKey(name)+"]")
				for _, k := range cfg.Keys() {
					v, _ := cfg.Get(k)
					switch t := v.(type) {
					case *jsonx.OrderedMap:
						lines = append(lines, codexTomlKey(k)+" = "+codexTomlInlineTable(t))
					case []any:
						lines = append(lines, codexTomlKey(k)+" = "+codexTomlArray(t))
					default:
						lines = append(lines, codexTomlKey(k)+" = "+codexTomlScalar(v))
					}
				}
			}
		}
	}
	if len(dropped) > 0 {
		sorted := append([]string(nil), dropped...)
		sort.Strings(sorted)
		e.warn("warning: codex config: dropped un-serializable table(s): " + strings.Join(sorted, ", "))
	}
	return strings.Join(lines, "\n") + "\n"
}

func ConfigureCodex(e *Env) error {
	dir := e.CodexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "config.toml")
	managedPath := filepath.Join(dir, "yolo-managed-mcp-servers.json")

	// Translate shared MCP servers into Codex's TOML table shape.
	configured := e.LoadMCPServers()
	codexMCP := jsonx.NewOrderedMap()
	for _, name := range configured.Keys() {
		v, _ := configured.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		entry := jsonx.NewOrderedMap()
		entry.Set("command", getOr(cfg, "command", ""))
		args := []any{}
		if a, ok := cfg.Get("args"); ok {
			if arr, isArr := a.([]any); isArr {
				args = append(args, arr...)
			}
		}
		entry.Set("args", args)
		if envVal, ok := cfg.Get("env"); ok {
			if envMap, isMap := envVal.(*jsonx.OrderedMap); isMap && envMap.Len() > 0 {
				entry.Set("env", envMap)
			}
		}
		codexMCP.Set(name, entry)
	}

	// Load current config (order-preserving).
	var current *jsonx.OrderedMap
	if pathExists(configPath) {
		raw, err := os.ReadFile(configPath)
		if err == nil {
			if decoded, derr := tomlx.DecodeOrdered(raw); derr == nil {
				current = decoded
			}
		}
	}
	if current == nil {
		current = jsonx.NewOrderedMap()
	}

	current.Set("approval_policy", "never")
	current.Set("sandbox_mode", "danger-full-access")

	// Reconcile yolo-managed MCP servers.
	var servers *jsonx.OrderedMap
	if v, ok := current.Get("mcp_servers"); ok {
		if m, isMap := v.(*jsonx.OrderedMap); isMap {
			servers = m
		}
	}
	if servers == nil {
		servers = jsonx.NewOrderedMap()
	}
	for _, name := range loadManagedSet(managedPath) {
		servers.Delete(name)
	}
	updateFrom(servers, codexMCP)
	if servers.Len() > 0 {
		current.Set("mcp_servers", servers)
	} else if _, ok := current.Get("mcp_servers"); ok {
		current.Delete("mcp_servers")
	}

	if err := writeInPlaceString(configPath, e.dumpCodexTOML(current)); err != nil {
		return err
	}
	return writeInPlaceString(managedPath, managedSidecar(codexMCP.Keys()))
}

// isJSONInt reports whether v is a jsonx integer literal (re-encodes without a
// decimal point). jsonx keeps integers as an unexported jsonInt type; we detect
// it by round-tripping through DumpsCompact (an int has no '.'/'e').
func isJSONInt(v any) bool {
	switch v.(type) {
	case float64, bool, string:
		return false
	}
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return false
	}
	return !strings.ContainsAny(s, ".eE") && looksLikeNumber(s)
}

func looksLikeNumber(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
