package storage

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// claudeJSONSeedKeys are the login-state keys back-propagated from a workspace
// claude.json into the GLOBAL_HOME seed. Allowlist only — mcpServers, projects,
// and other workspace-specific keys must never leak into the shared seed.
var claudeJSONSeedKeys = []string{"oauthAccount", "hasCompletedOnboarding"}

// SyncClaudeJSONSeed performs the two-way sync of Claude login/onboarding state
// between the GLOBAL_HOME seed and a per-workspace overlay's claude.json.
//
//   - Forward (seed → workspace): fill keys the workspace is missing from the
//     seed, preserving workspace-specific config; rewrite the workspace file
//     only if something was actually merged.
//   - Reverse (workspace → seed): if the workspace has oauthAccount and the
//     seed lacks it, write the allowlisted login keys up into the seed,
//     preserving unrelated seed keys.
//
// Never raises: a parse/IO error degrades to a no-op for that direction (an
// unparseable file reads as {}). Output uses json.dumps(indent=2) + "\n".
func SyncClaudeJSONSeed(seedPath, wsPath string) {
	seedData := readJSONDict(seedPath)
	wsData := readJSONDict(wsPath)

	// Forward: seed → workspace (fill missing keys, preserve order).
	if seedData.Len() > 0 {
		merged := false
		for _, key := range seedData.Keys() {
			if _, ok := wsData.Get(key); !ok {
				val, _ := seedData.Get(key)
				wsData.Set(key, val)
				merged = true
			}
		}
		if merged {
			writeJSONDict(wsPath, wsData)
		}
	}

	// Reverse: workspace → seed (allowlisted login keys) when the workspace is
	// logged in but the seed is not.
	if truthy(wsData, "oauthAccount") && !truthy(seedData, "oauthAccount") {
		for _, key := range claudeJSONSeedKeys {
			if val, ok := wsData.Get(key); ok {
				seedData.Set(key, val)
			}
		}
		writeJSONDict(seedPath, seedData)
	}
}

// readJSONDict reads path as a JSON object, returning an empty OrderedMap on any
// error or when the top-level value is not an object
// "data if isinstance(data, dict) else {}").
func readJSONDict(path string) *jsonx.OrderedMap {
	data, err := os.ReadFile(path)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	if m, ok := v.(*jsonx.OrderedMap); ok {
		return m
	}
	return jsonx.NewOrderedMap()
}

// writeJSONDict writes m as json.dumps(m, indent=2) + "\n", best-effort
// (mkdir -p the parent, ignore IO errors — matches the Python except: pass).
func writeJSONDict(path string, m *jsonx.OrderedMap) {
	s, err := jsonx.DumpsIndent(m, 2)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(s+"\n"), 0o644)
}

// truthy reports whether m[key] is present and Python-truthy. The only values
// stored here are the decoded oauthAccount object (truthy when non-empty) or a
// bool; we treat a present non-nil, non-empty value as truthy — matching
// `ws_data.get("oauthAccount")` used as a boolean.
func truthy(m *jsonx.OrderedMap, key string) bool {
	v, ok := m.Get(key)
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case *jsonx.OrderedMap:
		return t.Len() > 0
	case []any:
		return len(t) > 0
	default:
		// Numbers/other: present and non-nil ⇒ truthy unless it's a zero int.
		return true
	}
}
