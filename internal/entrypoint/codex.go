package entrypoint

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// buildCodexMCPServers translates the live shared MCP servers (LoadMCPServers)
// into codex's config.toml table shape: one OrderedMap per server of
// {command, args, [env]} keyed by server name, ready to sit under the
// mcp_servers key. This is the raw yolo-owned set consumed by ConfigureCodexPrism
// as the computed layer, where the §5 last_render anchor makes an explicit
// managed sidecar unnecessary. Mirrors buildGeminiMCPServers.
func buildCodexMCPServers(e *Env) *jsonx.OrderedMap {
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
	return codexMCP
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
