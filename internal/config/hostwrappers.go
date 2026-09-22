package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostWrappersKey is the top-level opt-in for host launch wrappers.
const hostWrappersKey = "host_wrappers"

// HostWrappersEnabled reports whether the user has opted in to host launch wrappers —
// the generated `bin/wrap` directory a user prepends to PATH so a bare `claude` composes
// its environment through `yolo host` (docs/reference/host-agent-environment.md §5.1).
//
// # It is read from the USER config directly, and that is the security boundary
//
// This key's effect is that yolo writes EXECUTABLES into a directory the user has put
// FIRST on their PATH. Of the places a config key can come from, two are jail-writable:
// the workspace `yolo-jail{,.local}.jsonc` (/workspace is bind-mounted rw, so an agent
// can edit it) and `<workspace>/.yolo/config-assembled.json` (same mount, read verbatim
// in-jail). Reading this key from the merged config would therefore let a repository —
// or an agent editing one — arrange for yolo to install programs at the front of its
// user's PATH on the next apply. Reading the user config directly makes workspace scope
// INEXPRESSIBLE rather than merely refused, which is the same construction `host_files`
// uses for its source-bearing half and for the same reason.
//
// validateHostWrappersScope's workspace-scope error is defense-in-depth against a silent
// no-op, not the boundary itself.
//
// A user config that cannot be read or parsed yields false: an opt-in nobody could read
// has not been given, and the alternative — defaulting a PATH claim to ON when the file
// is broken — is the wrong direction to fail in.
func HostWrappersEnabled() bool {
	return hostWrappersValue(UserScopeConfigOrEmpty())
}

// hostWrappersValue reads the key out of an already-loaded config map. Split out so the
// validator and the tests can exercise the same reading without touching the real home.
//
// # Unset DEFAULTS TO ON at `host_management: "own"` — a derivation, not a second opt-in
//
// `own` means yolo composes the agent's config file WHOLE; it is derived output. But a config
// file cannot carry a credential — `api_key_env_name` holds a variable's NAME, and the value
// arrives only through the environment channel, which is `yolo host -- <agent>`. Without a
// wrapper on PATH a bare `claude` gets the config and none of the environment it assumes, so
// `own` with no wrappers is a HALF-CONFIGURED host — and it was reachable by declaring one key
// without knowing a second existed.
//
// Deriving rather than asking twice is the move `host_apply_on_launch` already makes one level
// down (it defaults to THIS key's value), so the chain is
// `host_management: own` -> wrappers -> apply-on-launch, each link a derivation nobody spells.
//
// ⚠ An EXPLICIT `false` still wins, and that is why the key survives at all: someone who wants
// yolo to own their config files and refuses to have anything put on their PATH must be able to
// say so.
//
// ⚠ `assert` does NOT enable them, and the asymmetry is load-bearing rather than timid: `assert`
// is host_management's own UNSET default, so deriving from it would switch a PATH claim on for
// every user who has declared nothing at all — which is the one direction this key must never
// fail in.
func hostWrappersValue(cfg *jsonx.OrderedMap) bool {
	if v, present := cfg.Get(hostWrappersKey); present && v != nil {
		b, ok := v.(bool)
		return ok && b
	}
	mode, declared := hostManagementValue(cfg)
	return declared && mode == HostManagementOwn
}

// UserScopeConfig loads ONLY the machine-wide user config (plus its own
// include_if_found files) — never the workspace config, and never the assembled snapshot.
//
// # What it is for, and why the host env channel needs it
//
// `yolo host -- <agent>` composes the environment of a process that runs ON THE HOST,
// outside every sandbox. `/workspace` is bind-mounted rw and its `yolo-jail.jsonc` is
// agent-editable, so composing that process's environment from the MERGED config would
// let a cloned repository — or an agent editing one — set variables for a host process:
// LD_PRELOAD, NODE_OPTIONS, PATH. That is arbitrary code execution on the host, reached by
// cloning a repo and running an agent in it.
//
// Inside a jail the same keys are fine and stay merged: a workspace declaring env for its
// own sandboxed container is the ordinary case, and the container is the boundary. The
// asymmetry is the point — it is the same one `host_files` and `host_wrappers` draw, and
// the same construction: read user scope directly, so workspace scope is INEXPRESSIBLE
// rather than merely refused.
//
// An unreadable or unparseable user config yields an empty map rather than an error: the
// caller is launching an agent, not reconciling configuration, and ValidateConfig reports
// parse failures through its own channel.
//
// The name says the one thing that separates it from UserScopeConfig(strict, warn), whose
// error the loopholes/check callers reconcile: this variant CANNOT fail.
func UserScopeConfigOrEmpty() *jsonx.OrderedMap {
	path := paths.UserConfigPath()
	cfg, err := loadUserScopeConfig(path, path, false, func(string) {})
	if err != nil || cfg == nil {
		return jsonx.NewOrderedMap()
	}
	return cfg
}
