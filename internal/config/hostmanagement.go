package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostManagementKey is the top-level declaration of WHO OWNS the config files yolo renders
// into the user's real home (docs/design/config-ownership-and-promotion.md §4).
const hostManagementKey = "host_management"

// HostManagement is the declared ownership contract for the host notch. It is the value of
// `host_management`, and it selects the surface mode the host renders in — which is what
// makes "ownership is declared, never inferred" (§1 P1) literally true rather than
// aspirational.
type HostManagement string

const (
	// HostManagementNone: the user owns their files entirely. Host surfaces are
	// `unrendered`, and `yolo host apply` refuses, naming this key.
	HostManagementNone HostManagement = "none"
	// HostManagementAssert: shared ownership. Host surfaces are `rmw` — yolo owns the keys
	// it declares and the user owns the rest. This is shipped behavior, and the unset
	// state (§4.3).
	HostManagementAssert HostManagement = "assert"
	// HostManagementOwn: yolo owns the file; it is derived output. Host surfaces compose
	// whole-file and capture edits, exactly as a jail's do. NOT WIRED YET — the `own` step
	// of §10's build order, which is its last. Parsing and validation accept it so the contract is expressible
	// before the render engine can honor it; the host apply refuses it meanwhile, rather
	// than silently behaving as `assert`.
	HostManagementOwn HostManagement = "own"
)

// KnownHostManagements is the accepted value set, in the order the three are explained —
// least yolo involvement first. It is what the validator's message enumerates, so there is
// one list rather than a constant set and a hand-written sentence that drift apart.
var KnownHostManagements = []HostManagement{
	HostManagementNone, HostManagementAssert, HostManagementOwn,
}

// HostManagementMode reports the declared host ownership contract.
//
// # It is read from the USER config directly, and that is the security boundary
//
// The same construction `host_files`, `host_wrappers` and `host_apply_on_launch` use
// (§4.2), for a reason that is at its sharpest here: this key decides whether yolo may
// write the user's real `$HOME` at all, and — at `own` — whether their hand-written keys
// become derived output. Of the places a config key can come from, two are jail-writable:
// the workspace `yolo-jail{,.local}.jsonc` (/workspace is bind-mounted rw, so an agent can
// edit it) and `<workspace>/.yolo/config-assembled.json`. Reading this key from the merged
// config would let a cloned repository declare itself the owner of its user's home.
// Reading user scope directly makes workspace scope INEXPRESSIBLE rather than merely
// refused; validateHostManagement's workspace-scope error is defense-in-depth against a
// silent no-op, not the boundary itself.
//
// # The two absences are DIFFERENT ANSWERS, and that is why this does not use the shared helper
//
// UserScopeConfigOrEmpty returns an empty map for BOTH "there is no user config" and "the
// user config could not be read", so a reader built on it cannot tell the two apart — and
// §4.2 gives them opposite answers:
//
//   - ABSENT KEY (including no config file at all) ⇒ `assert`. That is today's behavior, so
//     upgrade day changes nothing for anyone and nobody is interrupted to be told so
//     (OQ-CO2). The unset state is not a fourth value; the default carries the migration.
//   - UNREADABLE OR UNPARSEABLE CONFIG ⇒ `none`. Fail closed: a declaration nobody could
//     read has granted no write claim, and `none` is the value that writes nothing.
//
// So the load is STRICT (loadUserScopeConfig's error return, the same posture LoadHostFiles
// takes) and the two cases are distinguished explicitly. ⚠ Do not infer the direction from
// the construction: `agent_updates` reads user scope through the same boundary and fails
// OPEN, because it is an opt-OUT. The direction is a per-key choice each key must state.
//
// A PRESENT BUT UNUSABLE VALUE — a typo, a bool, a JSON object — is `none` for the same
// reason: it is a declaration that could not be read. It is never silently `assert`, which
// would make a misspelled `"asert"` indistinguishable from a working declaration.
// ValidateConfig reports the value itself through its own channel.
func HostManagementMode() HostManagement {
	mode, _ := HostManagementDeclared()
	return mode
}

// HostManagementDeclared is HostManagementMode plus whether the user actually WROTE the key.
//
// The second return is what `yolo apply --sealed` needs and nothing else does (§4.3 item 3):
// an environment whose host-ownership contract is unstated is not sealed, and that is the
// ONE place the unset state bites. Every other caller wants the mode, in which unset and
// `assert` are the same answer by ruling — so the distinction is offered here rather than
// left for each caller to re-derive from the file.
//
// An unreadable config reports declared=false as well as `none`: this cannot prove a
// declaration it could not read, and `--sealed`'s whole question is whether one exists.
func HostManagementDeclared() (HostManagement, bool) {
	p := paths.UserConfigPath()
	// strict: a malformed user config must be an ERROR here rather than a silently empty
	// map, because "the file is broken" and "the key is absent" have opposite answers.
	cfg, err := loadUserScopeConfig(p, p, true, func(string) {})
	if err != nil || cfg == nil {
		return HostManagementNone, false
	}
	return hostManagementValue(cfg)
}

// hostManagementValue reads the key out of an already-loaded USER-SCOPE config map. Split
// out so the validator and the tests exercise the same reading without touching the real
// home — the split hostApplyOnLaunchValue makes, for the same reason.
//
// It is only ever handed a config that LOADED. The unreadable case is its caller's, because
// this function cannot see the difference between a map that came back empty and one that
// was never read.
func hostManagementValue(cfg *jsonx.OrderedMap) (HostManagement, bool) {
	v, present := cfg.Get(hostManagementKey)
	if !present || v == nil {
		return HostManagementAssert, false
	}
	s, ok := v.(string)
	if !ok {
		return HostManagementNone, false
	}
	for _, known := range KnownHostManagements {
		if HostManagement(s) == known {
			return known, true
		}
	}
	return HostManagementNone, false
}

// hostManagementProblem reports why a value is not a usable `host_management`, or "" when it
// is fine. Shared by the validator and by nothing else today; it exists as a function so the
// accepted values are stated once, from KnownHostManagements.
func hostManagementProblem(v any) string {
	s, ok := v.(string)
	if !ok {
		return "expected one of " + hostManagementList() + " (got " + pyReprValue(v) + ")"
	}
	for _, known := range KnownHostManagements {
		if HostManagement(s) == known {
			return ""
		}
	}
	return pyReprValue(v) + " is not one of " + hostManagementList()
}

// hostManagementList renders the accepted values for a message: `"none"`, `"assert"`, `"own"`.
func hostManagementList() string {
	out := ""
	for i, known := range KnownHostManagements {
		if i > 0 {
			out += ", "
		}
		out += `"` + string(known) + `"`
	}
	return out
}
