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
	m, _, _, err := loadJSONCFile(path, label, strict, warn, false)
	return m, err
}

// LoadJSONCFileWithSources is LoadJSONCFile plus where each value sits in the file
// (sources.go), for a caller that reports on one file by itself.
func LoadJSONCFileWithSources(path, label string, strict bool, warn Warn) (*jsonx.OrderedMap, *Sources, error) {
	m, _, n, err := loadJSONCFile(path, label, strict, warn, true)
	return m, sourcesOf(n), err
}

// loadJSONCFile is LoadJSONCFile, returning the file as read (its path and bytes, for a
// problem the caller locates in it) and, when record is set, its provenance tree (sources.go).
// Both are nil whenever the map is the empty one a missing or unreadable file yields.
//
// record is false for every caller that does not report on the config: the tree is built from
// every value of every file, and a launch reads its config many times over, so a reader that
// would discard it does not pay for it.
func loadJSONCFile(path, label string, strict bool, warn Warn, record bool) (*jsonx.OrderedMap, *srcFile, *srcNode, error) {
	if warn == nil {
		warn = defaultWarn
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewOrderedMap(), nil, nil, nil
		}
		// A read error other than not-exist is surfaced as a parse failure.
		m, err := handleParseFailure(label, err, strict, warn)
		return m, nil, nil, err
	}
	parsed, perr := json5.Decode(data)
	if perr != nil {
		m, err := handleParseFailure(label, perr, strict, warn)
		return m, nil, nil, err
	}
	m, ok := asMap(parsed)
	if !ok {
		msg := label + " must contain a top-level JSON object"
		if strict {
			return nil, nil, nil, configErr("%s", msg)
		}
		warn(msg)
		return jsonx.NewOrderedMap(), nil, nil, nil
	}
	f := &srcFile{path: path, data: data}
	if !record {
		return m, f, nil, nil
	}
	return m, f, fileTree(f, m), nil
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
//
// It also folds the two lists' provenance (sources.go) when either is recorded: an override
// item the dedup drops is the base item written again, so that entry's record names both
// files. With neither recorded the node is nil and no record is built.
func mergeLists(base, override []any, bsrc, osrc *srcNode) ([]any, *srcNode) {
	track := bsrc != nil || osrc != nil
	merged := make([]any, len(base))
	copy(merged, base)
	var node *srcNode
	if track {
		node = &srcNode{writers: concatWriters(bsrc.writerList(), osrc.writerList()),
			elems: make([]*srcNode, len(base))}
		for i := range base {
			node.elems[i] = bsrc.elem(i)
		}
	}
	seen := make(map[string]int, len(merged)) // dedup key -> index of its first item
	for i, item := range merged {
		k := dedupKey(item)
		if _, dup := seen[k]; !dup {
			seen[k] = i
		}
	}
	for j, item := range override {
		k := dedupKey(item)
		if i, ok := seen[k]; ok {
			if track {
				node.elems[i] = alsoWrittenIn(node.elems[i], osrc.elem(j))
			}
			continue
		}
		merged = append(merged, item)
		seen[k] = len(merged) - 1
		if track {
			node.elems = append(node.elems, osrc.elem(j))
		}
	}
	return merged, node
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
	merged, _ := mergeConfig(base, override, nil, nil)
	return merged
}

// mergeConfig is MergeConfig, folding the two configs' provenance (sources.go) beside the
// values when either is recorded. ONE function for both, so the record cannot disagree with
// the merge about which file's value won: every branch below decides the value and its
// record together. With neither recorded the node is nil and no record is built.
func mergeConfig(base, override *jsonx.OrderedMap, bsrc, osrc *srcNode) (*jsonx.OrderedMap, *srcNode) {
	track := bsrc != nil || osrc != nil
	result := jsonx.NewOrderedMap()
	var node *srcNode
	if track {
		node = &srcNode{writers: concatWriters(bsrc.writerList(), osrc.writerList()),
			keys: make(map[string]*srcNode, base.Len()+override.Len())}
	}
	for _, k := range base.Keys() {
		v, _ := base.Get(k)
		result.Set(k, v)
		if track {
			node.keys[k] = bsrc.key(k)
		}
	}
	for _, key := range override.Keys() {
		value, _ := override.Get(key)
		existing, present := result.Get(key)
		if present {
			if em, ok := asMap(existing); ok {
				if vm, ok := asMap(value); ok {
					merged, mn := mergeConfig(em, vm, node.key(key), osrc.key(key))
					result.Set(key, merged)
					if track {
						node.keys[key] = mn
					}
					continue
				}
			}
			if el, ok := asList(existing); ok {
				if vl, ok := asList(value); ok {
					merged, ln := mergeLists(el, vl, node.key(key), osrc.key(key))
					result.Set(key, merged)
					if track {
						node.keys[key] = ln
					}
					continue
				}
			}
		}
		result.Set(key, value)
		if track {
			if present {
				node.keys[key] = replacedBy(node.keys[key], osrc.key(key))
			} else {
				node.keys[key] = osrc.key(key)
			}
		}
	}
	return result, node
}

// LoadJSONCWithIncludes loads a JSONC file and its includes. Include entries are
// relative paths resolved against the including file's directory; missing files
// skip; overrides win (later wins); cycles are detected via the shared seen set.
// The include_if_found key is consumed and removed from the returned config.
func LoadJSONCWithIncludes(path, label string, strict bool, warn Warn, seen map[string]struct{}) (*jsonx.OrderedMap, error) {
	m, _, err := loadWithIncludes(path, label, strict, warn, seen, false)
	return m, err
}

// loadWithIncludes is LoadJSONCWithIncludes, returning the composed provenance beside the
// map when record is set (loadJSONCFile): the file's own tree with each include's merged over
// it, as the includes' values are. A problem with the file's own include_if_found is located
// in it either way.
func loadWithIncludes(path, label string, strict bool, warn Warn, seen map[string]struct{}, record bool) (*jsonx.OrderedMap, *srcNode, error) {
	if warn == nil {
		warn = defaultWarn
	}
	if seen == nil {
		seen = map[string]struct{}{}
	}
	resolved := resolvePathForSeen(path)
	if _, ok := seen[resolved]; ok {
		return jsonx.NewOrderedMap(), nil, nil
	}
	seen[resolved] = struct{}{}

	raw, file, node, err := loadJSONCFile(path, label, strict, warn, record)
	if err != nil {
		return nil, nil, err
	}
	if raw.Len() == 0 {
		// An empty (falsy) map is returned directly WITHOUT consuming includes.
		return raw, node, nil
	}
	// Anchor this file's relative env_sources entries at THIS file's directory, before
	// the include walk merges anyone else's in. Per-file is the point (see
	// AnchorEnvSources): an include's entries anchor at the include's dir when the
	// recursion loads it, so provenance survives the concat that is about to happen.
	AnchorEnvSources(raw, filepath.Dir(path))

	includesVal, hasIncludes := raw.Get("include_if_found")
	raw.Delete("include_if_found") // consumed; not part of the returned config
	node = node.without("include_if_found")
	if !hasIncludes || includesVal == nil {
		return raw, node, nil
	}

	includes, ok := asList(includesVal)
	if !ok {
		msg := includeProblem(file, label, "include_if_found: expected a list of strings", json5.Key("include_if_found"))
		if strict {
			return nil, nil, configErr("%s", msg)
		}
		warn(msg)
		return raw, node, nil
	}

	baseDir := filepath.Dir(path)
	result := raw
	for idx, entry := range includes {
		entryLabel := fmt.Sprintf("include_if_found[%d]", idx)
		s, ok := asStr(entry)
		if !ok {
			msg := includeProblem(file, label, entryLabel+": expected a string path",
				json5.Key("include_if_found"), json5.Elem(idx))
			if strict {
				return nil, nil, configErr("%s", msg)
			}
			warn(msg)
			continue
		}
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
			msg := includeProblem(file, label, fmt.Sprintf("%s: must be a relative path (got %s); "+
				"absolute paths and '~' are not supported", entryLabel, pytext.Repr(s)),
				json5.Key("include_if_found"), json5.Elem(idx))
			if strict {
				return nil, nil, configErr("%s", msg)
			}
			warn(msg)
			continue
		}
		incPath := resolveJoin(baseDir, s)
		if !pathExists(incPath) {
			continue
		}
		included, incNode, err := loadWithIncludes(incPath, incPath, strict, warn, seen, record)
		if err != nil {
			return nil, nil, err
		}
		result, node = mergeConfig(result, included, node, incNode)
	}
	return result, node, nil
}

// ResolveWorkspaceConfigPath is where LoadWorkspaceConfig reads the workspace config file
// baseName (WorkspaceConfigName or WorkspaceLocalConfigName) from, and the name it reads it
// under: the `.jsonc` name when that file exists, else its `.json` fallback when that one does,
// else the `.jsonc` name, which is the file to create.
//
// It is the loader's own answer, so every other reader of a workspace config file asks it rather
// than joining a name of its own: a reader that joined the `.jsonc` name passed over a workspace
// configured in `yolo-jail.json`, whose keys the launch honored all the same.
func ResolveWorkspaceConfigPath(workspace, baseName string) (path, name string) {
	for _, name := range workspaceConfigCandidates(baseName) {
		if p := filepath.Join(workspace, name); pathExists(p) {
			return p, name
		}
	}
	return filepath.Join(workspace, baseName), baseName
}

// WorkspaceConfigFileNames is every file name LoadWorkspaceConfig can read, in its order: each
// base name, then its `.json` fallback.
func WorkspaceConfigFileNames() []string {
	var names []string
	for _, base := range []string{WorkspaceConfigName, WorkspaceLocalConfigName} {
		names = append(names, workspaceConfigCandidates(base)...)
	}
	return names
}

// workspaceConfigCandidates is the names baseName is read under, in the order the loader tries
// them: the `.jsonc` name, then its `.json` fallback.
func workspaceConfigCandidates(baseName string) []string {
	return []string{baseName, strings.TrimSuffix(baseName, "c")}
}

// LoadWorkspaceConfig loads yolo-jail.jsonc (or yolo-jail.json) plus
// yolo-jail.local.jsonc (or yolo-jail.local.json) (local wins), sharing the seen
// set so a config that also includes the local file doesn't merge it twice.
func LoadWorkspaceConfig(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	m, _, err := loadWorkspaceConfig(workspace, strict, warn, false)
	return m, err
}

// LoadWorkspaceConfigWithSources is LoadWorkspaceConfig plus where each value was written
// (sources.go).
func LoadWorkspaceConfigWithSources(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, *Sources, error) {
	m, n, err := loadWorkspaceConfig(workspace, strict, warn, true)
	return m, sourcesOf(n), err
}

// loadWorkspaceConfig is LoadWorkspaceConfig, with the provenance beside it when record is set.
func loadWorkspaceConfig(workspace string, strict bool, warn Warn, record bool) (*jsonx.OrderedMap, *srcNode, error) {
	if workspace == "" {
		workspace = cwd()
	}
	seen := map[string]struct{}{}
	wsPath, wsLabel := ResolveWorkspaceConfigPath(workspace, WorkspaceConfigName)
	wsCfg, wsNode, err := loadWithIncludes(wsPath, wsLabel, strict, warn, seen, record)
	if err != nil {
		return nil, nil, err
	}
	localPath, localLabel := ResolveWorkspaceConfigPath(workspace, WorkspaceLocalConfigName)
	localCfg, localNode, err := loadWithIncludes(localPath, localLabel, strict, warn, seen, record)
	if err != nil {
		return nil, nil, err
	}
	m, n := mergeConfig(wsCfg, localCfg, wsNode, localNode)
	return m, n, nil
}

// LoadConfig merges the user-level config under the workspace config, and the workspace's
// per-workspace file (workspacefile.go) over both.
func LoadConfig(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	m, _, err := loadConfig(workspace, strict, warn, false)
	return m, err
}

// LoadConfigWithSources is LoadConfig plus where each value of the merged config was written
// (sources.go), for a caller that reports validation problems: Annotate the messages with it.
// The Sources is nil where LoadConfig reads the in-jail copy of the host's assembled config,
// whose source files the jail does not have.
func LoadConfigWithSources(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, *Sources, error) {
	m, n, err := loadConfig(workspace, strict, warn, true)
	return m, sourcesOf(n), err
}

// LoadConfigWithoutWorkspaceFile is LoadConfig less the per-workspace file (workspacefile.go):
// the user config under the workspace config, and nothing over them. It is what a launch composes
// the user scope a jail INHERITS from (internal/cli/run/inheritscope.go), because that file is
// keyed by a host workspace path no jail has, the reason `brokered` is not inherited either: a
// switch made for this workspace would otherwise become the jail's user scope, and apply to
// every workspace a launch inside it opens.
func LoadConfigWithoutWorkspaceFile(workspace string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	m, _, err := composeConfig(workspace, strict, warn, false, false)
	return m, err
}

// loadConfig is LoadConfig, with the provenance beside it when record is set.
func loadConfig(workspace string, strict bool, warn Warn, record bool) (*jsonx.OrderedMap, *srcNode, error) {
	return composeConfig(workspace, strict, warn, record, true)
}

// composeConfig is loadConfig, with the per-workspace file merged last when withWorkspaceFile
// is set.
func composeConfig(workspace string, strict bool, warn Warn, record, withWorkspaceFile bool) (*jsonx.OrderedMap, *srcNode, error) {
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
			return snap, nil, nil
		}
	}
	// The user half goes through loadUserScopeConfig so a --user-layer lands at user-level
	// precedence (a workspace config still wins over it — see userlayer.go).
	userCfg, userNode, err := loadUserScope(
		paths.UserConfigPath(), paths.UserConfigPath(), strict, warn, record)
	if err != nil {
		return nil, nil, err
	}
	wsCfg, wsNode, err := loadWorkspaceConfig(workspace, strict, warn, record)
	if err != nil {
		return nil, nil, err
	}
	m, n := mergeConfig(userCfg, wsCfg, userNode, wsNode)
	if withWorkspaceFile {
		// LAST, over the workspace config too (workspacefile.go): every key the file may carry
		// is a switch a human made for this one workspace, which outranks a file the
		// workspace's agent can edit.
		m, n = applyWorkspaceFile(m, n, workspace, record)
	}
	return m, n, nil
}

// inJail reports whether we are executing inside a yolo jail (the host always
// sets YOLO_VERSION to a non-empty version string in the container env).
func inJail() bool {
	return os.Getenv("YOLO_VERSION") != ""
}

// InJail is inJail for callers outside this package — a DELEGATION rather than a
// second copy of the probe, so there is one answer to "am I in a jail?" and
// re-siting the discriminator moves every caller at once.
//
// The first outside caller is the host-launch gate (internal/cli's
// hostApplyGate), which must be a hard no-op in a jail: it re-renders the
// INVOKING USER'S REAL HOME, and paths.Home() in here is /home/agent — the
// container's own disposable home, which no host render is about.
func InJail() bool { return inJail() }

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
	own := JailWorkspace()
	a, aerr := resolve(workspace)
	b, berr := resolve(own)
	if aerr != nil || berr != nil {
		return filepath.Clean(workspace) == filepath.Clean(own)
	}
	return a == b
}

// JailWorkspace is this jail's own workspace as the jail sees it, its bind-mount root:
// "/workspace", or YOLO_WORKSPACE where the backend puts it elsewhere (jailOwnWorkspace). It
// answers for a process in a jail (InJail) and is meaningless on the host.
func JailWorkspace() string {
	if own := os.Getenv("YOLO_WORKSPACE"); own != "" {
		return own
	}
	return "/workspace"
}

// IsJailOwnWorkspace reports whether workspace is the one this jail was launched for
// (jailOwnWorkspace), for a caller outside this package that must tell it from a workspace a
// launch inside the jail would open.
func IsJailOwnWorkspace(workspace string) bool { return jailOwnWorkspace(workspace) }

// JailLaunchConfig is the merged config the host launched this jail with — its delivery copy,
// <workspace>/.yolo/config-assembled.json — when this process runs in a jail and workspace is
// that jail's own; ok=false anywhere else. It is the one place in a jail that holds what the
// host's per-workspace file switched (workspacefile.go), since the file itself never crosses.
func JailLaunchConfig(workspace string) (*jsonx.OrderedMap, bool) {
	if !inJail() || !jailOwnWorkspace(workspace) {
		return nil, false
	}
	return loadAssembledSnapshot(workspace)
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
