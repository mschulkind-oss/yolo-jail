// Package config provides yolo-jail.jsonc parsing, merging, validation,
// env_sources resolution, and the config-snapshot diff. It is built on
// internal/json5 (JSONC/JSON5 decode), internal/jsonx (OrderedMap +
// DumpsSnapshot — the config-snapshot bytes), and internal/pytext (Python repr
// for the {x!r} bits of validation error strings).
// The snapshot writer bytes, the merge/dedup semantics, and every validation
// error/warning string
// (in identical order) must match the live Python, verified by the differential
// oracle in config_parity_test.go. Surprising Python behavior is PRESERVED and
// noted, never "fixed".
// Config data flows through *jsonx.OrderedMap everywhere (never a plain Go map):
// key order is load-bearing for the snapshot bytes.
package config

import (
	"fmt"
	"regexp"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ConfigError is the only error type users ever see when their yolo-jail.jsonc
// is malformed. Mirrors Python's ConfigError(ValueError).
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

func configErr(format string, args ...any) *ConfigError {
	return &ConfigError{Msg: fmt.Sprintf(format, args...)}
}

// Config file names (config.py:WORKSPACE_CONFIG_NAME / WORKSPACE_LOCAL_CONFIG_NAME).
const (
	WorkspaceConfigName      = "yolo-jail.jsonc"
	WorkspaceLocalConfigName = "yolo-jail.local.jsonc"
)

// ---------------------------------------------------------------------------
// Schema constants (config.py "Schema constants" block)
// ---------------------------------------------------------------------------

var DefaultHostClaudeFiles = []string{"settings.json"}

var DefaultHostPiFiles = []string{"settings.json"}

var knownTopLevelConfigKeys = set(
	"runtime", "repo_path", "agents", "packages", "mounts", "workspace_readonly",
	"per_side_paths", "network", "security", "mise_tools", "lsp_servers",
	"mcp_servers", "mcp_presets", "devices", "gpu", "resources", "env_sources",
	"host_claude_files", "host_pi_files", "loopholes", "host_processes", "journal",
	"kvm", "prune", "ephemeral_storage", "include_if_found", "agents_md_extra",
)

var journalModes = []string{"off", "user", "full"}

var ephemeralStorageModes = []string{"volume", "tmpfs"}

var (
	knownNetworkKeys          = set("mode", "ports", "forward_host_ports")
	knownSecurityKeys         = set("blocked_tools")
	knownBlockedToolKeys      = set("name", "message", "suggestion", "block_flags")
	knownHostProcessesKeys    = set("visible", "fields")
	knownPackageKeys          = set("name", "nixpkgs", "version", "url", "hash", "outputs")
	knownLSPServerKeys        = set("command", "args", "fileExtensions")
	knownMCPServerKeys        = set("command", "args", "env", "requires_env")
	knownDeviceKeys           = set("usb", "description", "cgroup_rule")
	knownResourcesKeys        = set("memory", "cpus", "pids_limit")
	knownHostServiceKeys      = set("command", "env", "jail_socket")
	knownLoopholeOverrideKeys = set("enabled", "env", "jail_env")
	knownGPUKeys              = set(
		"enabled", "devices", "capabilities", "vendor", "mode",
		"hsa_override_gfx_version", "seccomp_unconfined", "vaapi",
	)
)

// PACKAGE_NAME_RE / PACKAGE_OUTPUT_RE and friends. Go's regexp is RE2 (no
// backtracking) which is sufficient for these simple anchored patterns.
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

// merged result, but this default has a single entry).
var defaultMiseToolsKeys = []string{"neovim"}
var defaultMiseToolsVals = map[string]string{"neovim": "stable"}

var defaultMiseDisabledTools = []string{"pnpm"}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// set builds a Go set from string literals. Membership only — never iterated
// for output, so ordering does not matter (matches Python `set` literals which
// config.py only tests membership on; the one place it iterates a known-key set
// for output — _report_unknown_keys — sorts the MAPPING keys, not the set).
func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

// asMap returns v as *jsonx.OrderedMap and true, mirroring Python
// isinstance(v, dict).
func asMap(v any) (*jsonx.OrderedMap, bool) {
	m, ok := v.(*jsonx.OrderedMap)
	return m, ok
}

// asList returns v as []any and true, mirroring isinstance(v, list).
func asList(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok
}

// asStr returns v as string and true, mirroring isinstance(v, str).
func asStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// isBool mirrors isinstance(v, bool). Python bool is a subclass of int, but a
// decoded JSON bool is a Go bool (jsonx decodes true/false to bool), so this is
// exact.
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
