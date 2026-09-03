// Package config provides yolo-jail.jsonc parsing, merging, validation,
// env_sources resolution, and the config-snapshot diff. It is built on
// internal/json5 (JSONC/JSON5 decode), internal/jsonx (OrderedMap +
// DumpsSnapshot — the config-snapshot bytes), and internal/pytext (repr
// for the {x!r} bits of validation error strings).
// The snapshot writer bytes, the merge/dedup semantics, and every validation
// error/warning string are emitted in a fixed order that is a frozen contract:
// callers diff snapshots, and `yolo internal config-dump` prints the errors and
// warnings as data. No oracle pins the ORDER any more — the differential
// config_parity_test.go this comment used to cite went away with the Python
// implementation it compared against — so the freeze is a convention the tests
// around each rule uphold, not a checked invariant.
// Non-obvious edge-case behavior is PRESERVED and noted, never "fixed".
// Config data flows through *jsonx.OrderedMap everywhere (never a plain Go map):
// key order is load-bearing for the snapshot bytes.
package config

import (
	"fmt"
	"regexp"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ConfigError is the only error type users ever see when their yolo-jail.jsonc
// is malformed.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

func configErr(format string, args ...any) *ConfigError {
	return &ConfigError{Msg: fmt.Sprintf(format, args...)}
}

// Config file names.
const (
	WorkspaceConfigName      = "yolo-jail.jsonc"
	WorkspaceLocalConfigName = "yolo-jail.local.jsonc"
)

// ---------------------------------------------------------------------------
// Schema constants
// ---------------------------------------------------------------------------

// knownTopLevelConfigKeys is the accepted top-level key set.
var knownTopLevelConfigKeys = set(
	// `repo_path`, `agents`, `host_processes` and `journal` are RETIRED but stay listed:
	// a key in this set gets only its own targeted retirement message (validateRepoPath /
	// validateAgentsRetired / validateHostProcessesRetired / validateJournalRetired),
	// whereas removing it here would ALSO trigger the generic "unknown key" error and the
	// user would see the same problem reported twice — once uselessly.
	//
	// `host_processes` and `journal` are the two newest, they retired within a day of
	// each other, and their retirement is a MOVE rather than a deletion: each key's
	// values are now declared by its own loophole's manifest, shipped in the official
	// pack of the same name (docs/design/pack-config-keys.md, loophole-activation.md
	// §1.4). THEY WERE ALSO THE ONLY TWO LOOPHOLES THIS SCHEMA NAMED, which is what
	// makes the pair worth reading together: with both gone, core's config schema names
	// no loophole at all, and "convert the loophole to a pack" stops being a separation
	// in appearance only.
	"runtime", "confinement", "repo_path", "agents", "packages", "mounts", "workspace_readonly",
	"per_side_paths", "network", "security", "mise_tools", "lsp_servers",
	"mcp_servers", "mcp_presets", "devices", "gpu", "resources", "env_sources",
	"loopholes", "host_processes", "journal",
	"kvm", "prune", "ephemeral_storage", "include_if_found", "agents_md_extra",
	"cache_relocations", "writable_home_dirs", "host_files", "host_wrappers",
	"host_apply_on_launch", "packs",
	"providers", "profiles", "use_profiles", "required_capabilities",
	// `agent_profiles` retired 2026-09-01, renamed to `pack_profiles` (the keys were
	// always CLI names, and core knows packs, not agents — docs/design/
	// profiles-as-pack-variants.md §3.3), which was itself renamed to `use_profiles`
	// on 2026-09-02 (docs/reference/providers.md — Profiles and options: `pack` named neither of
	// the two things the key holds). NEITHER intermediate name ever shipped in a
	// release, which is why `pack_profiles` earns no census entry of its own — see
	// knownProviderKeys below for the rule. `agent_profiles` KEEPS its entry: that
	// spelling is written into every host-generated jail snapshot in existence.
	// Listed so the retirement message is the only error, per the convention above.
	"agent_profiles",
)

// `journalModes` — the off/user/full vocabulary — went with the key on 2026-08-18.
// TYPE AND ENUM CHECKS GO WITH A RETIRED KEY, always: reporting
// `config.journal: expected one of ['off', 'user', 'full']` beside "this key was
// removed" asks the user to fix the shape of something they must delete. The mode is
// now the `journal` loophole's own `full` setting, a BOOLEAN core type-checks through
// the settings declaration (validateLoopholeSettings), which is a narrower vocabulary
// than the string ever was — see the manifest for why a string could not be validated
// at all.

var ephemeralStorageModes = []string{"volume", "tmpfs"}

// knownEndpointKeys is the census for ONE protocol's entry inside a provider's
// `endpoints` map — the two keys a derive can consume, which is also what the
// pack-declared form of an endpoint carries (packdecl.ProviderEndpoint).
var knownEndpointKeys = set("base_url", "wire_api")

var (
	knownNetworkKeys     = set("mode", "ports", "forward_host_ports")
	knownSecurityKeys    = set("blocked_tools")
	knownBlockedToolKeys = set("name", "message", "suggestion", "block_flags")
	knownPackageKeys     = set("name", "nixpkgs", "version", "url", "hash", "outputs")
	knownLSPServerKeys   = set("command", "args", "fileExtensions")
	knownMCPServerKeys   = set("command", "args", "env", "requires_env", "provides")
	// `api_key_env` was renamed to `api_key_env_name` on 2026-09-01 and is NOT listed here.
	// It carried a by-name rename message for one day; the maintainer's call was that a key
	// that never shipped in a release does not earn a deprecation path, so the old spelling
	// is now an ordinary unknown key. `pack_profiles` is the rule's second application
	// (renamed to `use_profiles` on 2026-09-02, having landed 2026-08-31 — after
	// `v0.8.0`, 2026-08-13), and it is not listed here either. `agent_profiles` KEEPS its
	// rename message — that one is written into every host-generated jail snapshot in
	// existence, which is the distinction: a retired-key message is for a spelling that is
	// out there, not for one that was briefly in the tree. `env_shape` is the rule's third
	// application (deleted with its whole vocabulary on 2026-09-02, never in a release):
	// a provider's delivery is the agent pack's env derive now (OQ-CS8), so the key is an
	// ordinary unknown key and nothing checks its values.
	knownProviderKeys = set("base_url", "endpoints", "wire_api", "api_key_env_name",
		"models", "region", "capabilities", "options")
	knownDeviceKeys    = set("usb", "description", "cgroup_rule")
	knownResourcesKeys = set("memory", "cpus", "pids_limit")
	// knownHostServiceKeys is the INLINE loophole entry's key census. It must
	// cover every key the loader reads: `description` and `doctor_cmd` are read
	// by discover.go's synthesizeConfigLoopholes, and `jail_endpoint` is the
	// canonical form of the `jail_socket` alias that validateInlineService
	// itself prefix-checks — all three used to be "unknown key" errors here
	// while the rest of the machinery honored them (loophole-packaging.md R5).
	//
	// `preamble` is the same rule applied on arrival rather than in arrears:
	// synthesizeConfigLoopholes reads it (defaulting FALSE, the opposite of a
	// manifest's), so it validates. It belongs to the INLINE census and not to
	// knownLoopholeOverrideKeys, because applyWorkspaceOverrides does not read
	// it — on an override the key would be inert, which is precisely what the
	// doctor_cmd refusal in validateLoopholeOverride exists to prevent.
	//
	// `enabled` is the FOURTH key the reconciliation missed, and the one it could
	// least afford to: synthesizeConfigLoopholes reads it (defaulting TRUE — an
	// inline entry is a service the user wrote out by hand, so writing it IS the
	// deliberate act), and it is the only way to switch an inline service off
	// without deleting its argv. Omitting it here made
	// `{"command": [...], "enabled": false}` a hard config ERROR, which also made
	// `yolo loopholes disable`'s own instruction — "that key works for every
	// source (bundled, pack-shipped, config-inline)" — false for the third source
	// it names. It is in BOTH censuses because both loaders read it: on an
	// override applyWorkspaceOverrides honors it too.
	knownHostServiceKeys = set("command", "env", "jail_socket", "jail_endpoint",
		"doctor_cmd", "description", "preamble", "enabled")
	// `settings` is the pack-declared config-key block
	// (docs/design/pack-config-keys.md): a NESTED map whose inner keys are checked
	// against the loophole's manifest declarations, which is what keeps THIS census
	// closed while still letting a pack own a key. It is deliberately NOT in
	// knownHostServiceKeys: an INLINE config loophole has no manifest, hence no
	// declarations, hence nothing core could validate a value against — and the one
	// rule this whole mechanism rests on is that core never hands a host daemon a
	// value it could not validate (OQ-K1). An inline entry writes its daemon's argv
	// by hand and can put the values there.
	knownLoopholeOverrideKeys = set("enabled", "env", "jail_env", "settings")
	knownGPUKeys              = set(
		"enabled", "devices", "capabilities", "vendor", "mode",
		"hsa_override_gfx_version", "seccomp_unconfined", "vaapi",
	)
)

// Package/name/id validation patterns. Go's regexp is RE2 (no backtracking),
// sufficient for these simple anchored patterns.
var (
	packageNameRe   = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)?$`)
	packageOutputRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	hostServiceName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
	usbIDRe         = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$`)
	memoryRe        = regexp.MustCompile(`^\d+[bkmgBKMG]?$`)
	envVarNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

var vaapiPackages = []string{"mesa", "libva-utils"}

var validMCPPresets = set("chrome-devtools", "sequential-thinking")

// Default mise tools, merged under any user overrides.
//
// DELIBERATELY EMPTY. yolo ships no mise tool of its own: every default runtime is
// baked into the image as a nix package, which is self-contained (RPATH, no
// LD_LIBRARY_PATH dependency) and present at boot rather than downloaded on first use
// — see the `imagePkgs.go` comment in flake.nix for the same reasoning applied to Go.
// `neovim: stable` used to live here; it was removed because a tool yolo wants in every
// jail belongs in the image, not in a per-workspace mise store that re-installs it.
//
// Kept as a seam rather than deleted: `mise_tools` remains a real user knob (the
// legitimate case is "mine, in every jail, but not on my host" — see
// docs/design/composed-file-permissions.md §3.0.1), and MergeMiseTools still folds user
// entries over this empty base. If a future default is genuinely un-bakeable, it goes
// here; anything bakeable goes in flake.nix instead.
var defaultMiseToolsKeys = []string{}
var defaultMiseToolsVals = map[string]string{}

var defaultMiseDisabledTools = []string{"pnpm"}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// set builds a Go set from string literals. Membership only — never iterated
// for output, so ordering does not matter (the one place a known-key set feeds
// output — reportUnknownKeys — sorts the MAPPING keys, not the set).
func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

// asMap returns v as *jsonx.OrderedMap and true, for a JSON-object value.
func asMap(v any) (*jsonx.OrderedMap, bool) {
	m, ok := v.(*jsonx.OrderedMap)
	return m, ok
}

// asList returns v as []any and true, for a JSON-array value.
func asList(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok
}

// asStr returns v as string and true, for a JSON-string value.
func asStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// isBool reports whether v is a JSON boolean (jsonx decodes true/false to a Go
// bool).
func isBool(v any) bool {
	_, ok := v.(bool)
	return ok
}

// getOr is m.get(key, default): returns the value if present, else def.
func getOr(m *jsonx.OrderedMap, key string, def any) any {
	if v, ok := m.Get(key); ok {
		return v
	}
	return def
}
