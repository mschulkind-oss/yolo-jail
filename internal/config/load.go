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

// MergeConfig recursively merges override onto base: recursive dict merge, list
// union-merge, scalar/type-mismatch override. Returns a new OrderedMap; base's
// order is preserved, override-only keys are appended in override order.
//
// EVERY list key union-merges. There used to be an overrideListKeys exception for
// list keys that REPLACE wholesale, and `agents` was its only member — a workspace
// value replacing the user's is what let a repo-committed, agent-editable config
// decide agent selection, and through it which host files mounted. The key is gone
// (an agent arrives as a pack), so the exception has no members and the mechanism
// went with it rather than sitting inert waiting for a user it will not get.
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
			if el, ok := asList(existing); ok {
				if vl, ok := asList(value); ok {
					result.Set(key, mergeLists(el, vl))
					continue
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
	// Inside a jail, for THIS JAIL'S OWN workspace, do NOT re-assemble: COPY the
	// host's already-merged config from the delivered assembled config instead
	// (<workspace>/.yolo/config-assembled.json — see assembled.go). The user-level
	// `include_if_found` overrides (e.g. a machine-local overrides.jsonc carrying
	// mcp_servers) live on the HOST and are never mounted into the jail, so an
	// in-jail re-merge silently drops them — producing a reduced config that
	// mismatches the host. That file IS the assembled config serialized; reading it
	// verbatim keeps the in-jail view identical to the host's. Falls back to a normal
	// assemble when it is absent/unreadable (e.g. a workspace whose jail was launched
	// by a yolo that predates the file).
	//
	// It used to be the config-SNAPSHOT that was read here, and the second half of the
	// argument for the short-circuit used to be a ping-pong: an in-jail re-merge wrote
	// the reduced form back over the bind-mounted, host-owned approval record, so the
	// host re-prompted on every run. That half is now structural rather than argued —
	// the approval record moved host-side under OQ-D1 and no in-jail write can reach
	// it (ApprovalSnapshotPath). What is left here is the reduced-config half, which
	// is reason enough on its own.
	//
	// The jailOwnWorkspace() gate is load-bearing, not defensive. Only the OWN
	// workspace's copy was written by the host FOR THIS JAIL; another workspace's copy
	// is just the newest artifact of that workspace's own jail lineage, and reading it
	// makes the in-jail CLI act on a config nobody assembled for this launch. Two
	// things broke without the gate, both when an in-jail CLI launches a jail for a
	// DIFFERENT workspace (every nested launch, and every integration test):
	//
	//   - A workspace-config EDIT never took effect. Launch 1 wrote the file;
	//     launch 2 read it back instead of the edited yolo-jail.jsonc, so e.g.
	//     dropping a tool from `blocked_tools` left its shim generated forever
	//     (the shims are rendered from the config this returns).
	//   - CheckConfigChanges was silently disabled. It diffed the live config
	//     against that same file, so with the short-circuit it compared the
	//     file to ITSELF — always "unchanged", so the config-approval prompt
	//     could never fire for a nested launch. (Under OQ-D1 the approval record is
	//     no longer the same file, so this arm no longer follows from the
	//     short-circuit — but the first one still does, and the gate is one gate.)
	//
	// And the short-circuit's own rationale does not reach the other-workspace case,
	// so the gate gives up nothing. That copy was not written by the host; it was
	// written by an IN-JAIL assemble on a previous launch, through this very function.
	// So it is already the reduced merge (verified: a nested workspace's copy has
	// no mcp_servers, the host-only include_if_found key whose loss motivated the
	// short-circuit) — reading it back recovers no host-only override, it only
	// substitutes a staler copy of what assembling produces now.
	//
	// The --user-layer carve-out is load-bearing, not defensive. The delivered copy is a
	// FROZEN artifact of a previous launch, so it cannot contain a layer passed to THIS
	// invocation — returning it would make `yolo --user-layer x.jsonc check` silently
	// ignore the file the caller explicitly named, which is exactly the invisibility the
	// flag exists to avoid (a silently-ignored explicit argument is worse than no flag).
	// With a layer set we fall through and assemble, then merge it in.
	if inJail() && jailOwnWorkspace(workspace) && UserLayerPath() == "" {
		if snap, ok := loadAssembledSnapshot(workspace); ok {
			return snap, nil
		}
	}
	// The user half goes through loadUserScopeConfig so a --user-layer lands at user-level
	// precedence (a workspace config still wins over it — see userlayer.go).
	userCfg, err := loadUserScopeConfig(
		paths.UserConfigPath(), paths.UserConfigPath(), strict, warn)
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

// jailOwnWorkspace reports whether workspace is the workspace THIS jail was
// launched for — i.e. the one whose config-assembled.json the host wrote for this
// launch, and the only one the short-circuit may speak for.
//
// The jail's own workspace is its bind-mount root: "/workspace", or YOLO_WORKSPACE
// where the backend puts it elsewhere (the entrypoint resolves it the same way, see
// entrypoint.Env.WorkspaceDir). YOLO_HOST_DIR is deliberately NOT used: it is the
// HOST-side path of that mount, which never matches the in-jail path a caller
// passes here.
//
// Comparison is on resolved paths so a symlinked or non-clean workspace argument
// (t.TempDir() under /tmp → /private/tmp on darwin is the live case) still matches
// the mount root it actually denotes. An empty workspace means "the cwd", matching
// LoadConfig's own default.
func jailOwnWorkspace(workspace string) bool {
	if workspace == "" {
		workspace = cwd()
	}
	own := os.Getenv("YOLO_WORKSPACE")
	if own == "" {
		own = "/workspace"
	}
	a, aerr := resolve(workspace)
	b, berr := resolve(own)
	if aerr != nil || berr != nil {
		return filepath.Clean(workspace) == filepath.Clean(own)
	}
	return a == b
}

// loadAssembledSnapshot reads the host-delivered assembled config
// (<workspace>/.yolo/config-assembled.json) and returns it as the merged config.
// The file is the config serialized with sorted keys, so decoding it
// yields the same config the host assembled (dict keys sorted — cosmetic;
// list order, which is the only order that matters, is preserved). Returns
// ok=false when the file is missing or not a JSON object, so the caller falls
// back to a normal re-assemble.
func loadAssembledSnapshot(workspace string) (*jsonx.OrderedMap, bool) {
	if workspace == "" {
		workspace = cwd()
	}
	data, err := os.ReadFile(WorkspaceAssembledConfigPath(workspace))
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
