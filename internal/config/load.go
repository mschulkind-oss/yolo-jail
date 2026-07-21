package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// Warn is called for non-strict warnings. The loader factors this out so
// callers (yolo check, run) can route them to the same stderr/console. Nil
// means discard. The default writes "Warning: <msg>" to stderr.
type Warn func(msg string)

func defaultWarn(msg string) {
	fmt.Fprintln(os.Stderr, "Warning: "+msg)
}

// LoadJSONCFile loads a JSONC file. Missing file -> empty map. A parse error or
// a non-object top level is a ConfigError in strict mode, else warns and returns
// an empty map.
func LoadJSONCFile(path, label string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	if warn == nil {
		warn = defaultWarn
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewOrderedMap(), nil
		}
		// A read error other than not-exist is surfaced as a parse failure.
		return handleParseFailure(label, err, strict, warn)
	}
	parsed, perr := json5.Decode(data)
	if perr != nil {
		return handleParseFailure(label, perr, strict, warn)
	}
	m, ok := asMap(parsed)
	if !ok {
		msg := label + " must contain a top-level JSON object"
		if strict {
			return nil, configErr("%s", msg)
		}
		warn(msg)
		return jsonx.NewOrderedMap(), nil
	}
	return m, nil
}

func handleParseFailure(label string, err error, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	msg := "Failed to parse " + label + ": " + err.Error()
	if strict {
		return nil, configErr("%s", msg)
	}
	warn(msg)
	return jsonx.NewOrderedMap(), nil
}

// mergeLists appends override items not already present, with equality by the
// canonical dedup key (sorted-key JSON of the item). The base list is copied;
// order is base-then-new-override.
func mergeLists(base, override []any) []any {
	merged := make([]any, len(base))
	copy(merged, base)
	seen := make(map[string]struct{}, len(merged))
	for _, item := range merged {
		seen[dedupKey(item)] = struct{}{}
	}
	for _, item := range override {
		k := dedupKey(item)
		if _, ok := seen[k]; !ok {
			merged = append(merged, item)
			seen[k] = struct{}{}
		}
	}
	return merged
}

// overrideListKeys names list keys that REPLACE wholesale rather than
// union-merging.
var overrideListKeys = set("agents")

// MergeConfig recursively merges override onto base: recursive dict merge, list
// union-merge (except overrideListKeys), scalar/type-mismatch override. Returns
// a new OrderedMap; base's order is preserved, override-only keys are appended
// in override order.
func MergeConfig(base, override *jsonx.OrderedMap) *jsonx.OrderedMap {
	result := jsonx.NewOrderedMap()
	for _, k := range base.Keys() {
		v, _ := base.Get(k)
		result.Set(k, v)
	}
	for _, key := range override.Keys() {
		value, _ := override.Get(key)
		existing, present := result.Get(key)
		if present {
			if em, ok := asMap(existing); ok {
				if vm, ok := asMap(value); ok {
					result.Set(key, MergeConfig(em, vm))
					continue
				}
			}
			if _, isOverrideKey := overrideListKeys[key]; !isOverrideKey {
				if el, ok := asList(existing); ok {
					if vl, ok := asList(value); ok {
						result.Set(key, mergeLists(el, vl))
						continue
					}
				}
			}
		}
		result.Set(key, value)
	}
	return result
}

// LoadJSONCWithIncludes loads a JSONC file and its includes. Include entries are
// relative paths resolved against the including file's directory; missing files
// skip; overrides win (later wins); cycles are detected via the shared seen set.
// The include_if_found key is consumed and removed from the returned config.
func LoadJSONCWithIncludes(path, label string, strict bool, warn Warn, seen map[string]struct{}) (*jsonx.OrderedMap, error) {
	if warn == nil {
		warn = defaultWarn
	}
	if seen == nil {
		seen = map[string]struct{}{}
	}
	resolved := resolvePathForSeen(path)
	if _, ok := seen[resolved]; ok {
		return jsonx.NewOrderedMap(), nil
	}
	seen[resolved] = struct{}{}

	raw, err := LoadJSONCFile(path, label, strict, warn)
	if err != nil {
		return nil, err
	}
	if raw.Len() == 0 {
		// An empty (falsy) map is returned directly WITHOUT consuming includes.
		return raw, nil
	}

	includesVal, hasIncludes := raw.Get("include_if_found")
	raw.Delete("include_if_found") // consumed; not part of the returned config
	if !hasIncludes || includesVal == nil {
		return raw, nil
	}

	includes, ok := asList(includesVal)
	if !ok {
		msg := label + ".include_if_found: expected a list of strings"
		if strict {
			return nil, configErr("%s", msg)
		}
		warn(msg)
		return raw, nil
	}

	baseDir := filepath.Dir(path)
	result := raw
	for idx, entry := range includes {
		entryLabel := fmt.Sprintf("%s.include_if_found[%d]", label, idx)
		s, ok := asStr(entry)
		if !ok {
			msg := entryLabel + ": expected a string path"
			if strict {
				return nil, configErr("%s", msg)
			}
			warn(msg)
			continue
		}
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
			msg := fmt.Sprintf("%s: must be a relative path (got %s); "+
				"absolute paths and '~' are not supported", entryLabel, pytext.Repr(s))
			if strict {
				return nil, configErr("%s", msg)
			}
			warn(msg)
			continue
		}
		incPath := resolveJoin(baseDir, s)
		if !pathExists(incPath) {
			continue
		}
		included, err := LoadJSONCWithIncludes(incPath, incPath, strict, warn, seen)
		if err != nil {
			return nil, err
		}
		result = MergeConfig(result, included)
	}
	return result, nil
}

// LoadWorkspaceConfig loads yolo-jail.jsonc plus yolo-jail.local.jsonc (local
// wins), sharing the seen set so a config that
// also includes the local file doesn't merge it twice.
func LoadWorkspaceConfig(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	if workspace == "" {
		workspace = cwd()
	}
	seen := map[string]struct{}{}
	wsCfg, err := LoadJSONCWithIncludes(
		filepath.Join(workspace, WorkspaceConfigName), WorkspaceConfigName, strict, warn, seen)
	if err != nil {
		return nil, err
	}
	localCfg, err := LoadJSONCWithIncludes(
		filepath.Join(workspace, WorkspaceLocalConfigName), WorkspaceLocalConfigName, strict, warn, seen)
	if err != nil {
		return nil, err
	}
	return MergeConfig(wsCfg, localCfg), nil
}

// LoadConfig merges the user-level config under the workspace config.
func LoadConfig(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	// Inside a jail, do NOT re-assemble: COPY the host's already-merged config
	// from the workspace snapshot instead. The user-level `include_if_found`
	// overrides (e.g. a machine-local overrides.jsonc carrying mcp_servers) live
	// on the HOST and are never mounted into the jail, so an in-jail re-merge
	// silently drops them — producing a reduced config that (a) mismatches the
	// host and (b) rewrites the bind-mounted, host-owned snapshot with the
	// reduced form, so the host then re-prompts on every run (the ping-pong).
	// The snapshot IS the assembled config serialized; reading it verbatim keeps
	// the in-jail view identical to the host's. Falls back to a normal assemble
	// when the snapshot is absent/unreadable (e.g. never run through approval).
	if inJail() {
		if snap, ok := loadAssembledSnapshot(workspace); ok {
			return snap, nil
		}
	}
	userCfg, err := LoadJSONCWithIncludes(
		paths.UserConfigPath(), paths.UserConfigPath(), strict, warn, nil)
	if err != nil {
		return nil, err
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, strict, warn)
	if err != nil {
		return nil, err
	}
	return MergeConfig(userCfg, wsCfg), nil
}

// inJail reports whether we are executing inside a yolo jail (the host always
// sets YOLO_VERSION to a non-empty version string in the container env).
func inJail() bool {
	return os.Getenv("YOLO_VERSION") != ""
}

// loadAssembledSnapshot reads the host-written config snapshot
// (<workspace>/.yolo/config-snapshot.json) and returns it as the merged config.
// The snapshot is the config serialized with sorted keys, so decoding it
// yields the same config the host assembled (dict keys sorted — cosmetic;
// list order, which is the only order that matters, is preserved). Returns
// ok=false when the file is missing or not a JSON object, so the caller falls
// back to a normal re-assemble.
func loadAssembledSnapshot(workspace string) (*jsonx.OrderedMap, bool) {
	if workspace == "" {
		workspace = cwd()
	}
	data, err := os.ReadFile(ConfigSnapshotPath(workspace))
	if err != nil {
		return nil, false
	}
	decoded, err := jsonx.Decode(data)
	if err != nil {
		return nil, false
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	return m, true
}
