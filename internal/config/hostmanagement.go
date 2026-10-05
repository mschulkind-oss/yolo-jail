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
	// `unrendered`, and `yolo host apply` refuses, naming this key. It is also the UNSET
	// state since the `assert` retirement (OQ-CO14, §4.5), so a home yolo wrote into under
	// `assert` keeps exactly the bytes `assert` last rendered and nothing rewrites them.
	HostManagementNone HostManagement = "none"
	// HostManagementOwn: yolo owns the file; it is derived output. Host surfaces compose
	// whole-file and capture edits, exactly as a jail's do — the render keeps its capture
	// sidecars in the state-dir store render.Target.SidecarDir resolves for the host notch,
	// and host-side `yolo config reset` discards against it.
	HostManagementOwn HostManagement = "own"
)

// retiredHostManagementAssert is the value OQ-CO14 RETIRED (ruled 2026-10-05, building the
// 2026-09-20 ruling in config-ownership-and-promotion.md §4.5). It was shared ownership: yolo
// read-modify-wrote the keys its packs declare into a file the user also wrote.
//
// It is kept ONLY so a config still spelling it earns its own targeted refusal rather than the
// generic "is not one of" error every other unusable value gets — the shape every retired
// spelling in this config takes (retiredTopLevelConfigKeys). It is not a value: it resolves to
// `none` like any other unusable value (hostManagementValue), and nothing renders under it.
const retiredHostManagementAssert = "assert"

// KnownHostManagements is the accepted value set, in the order the two are explained —
// least yolo involvement first. It is what the validator's message enumerates, so there is
// one list rather than a constant set and a hand-written sentence that drift apart.
var KnownHostManagements = []HostManagement{HostManagementNone, HostManagementOwn}

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
//   - ABSENT KEY (including no config file at all) ⇒ `none`, with declared=false. Since the
//     `assert` retirement the unset state writes nothing (OQ-CO14, ruled 2026-10-05): no
//     prompt and no notice at upgrade, and a home yolo asserted into keeps the bytes `assert`
//     last rendered. The unset state is still not a third value — `yolo apply --sealed` is
//     the one reader that tells it apart, through the second return.
//   - UNREADABLE OR UNPARSEABLE CONFIG ⇒ `none`, with declared=false. Fail closed: a
//     declaration nobody could read has granted no write claim.
//
// The two absences give the same MODE now, and the load stays STRICT anyway
// (loadUserScopeConfig's error return, the same posture LoadHostFiles takes): the direction
// is a per-key choice each key must state, and `agent_updates` reads user scope through the
// same boundary and fails OPEN, because it is an opt-OUT. A reader rebuilt on the shared
// lenient helper would be right today by coincidence and wrong the day the default moves.
//
// A PRESENT BUT UNUSABLE VALUE — a typo, a bool, a JSON object, and the retired `"assert"` —
// is `none` for the same reason: it is a declaration that could not be read, and never a
// value yolo writes under. ValidateConfig reports the value itself through its own channel,
// and the retired spelling gets its own message there and at every host verb that would have
// written (HostManagementRetired).
func HostManagementMode() HostManagement {
	mode, _ := HostManagementDeclared()
	return mode
}

// HostManagementDeclared is HostManagementMode plus whether the user actually WROTE the key.
//
// The second return is what `yolo apply --sealed` needs and nothing else does (§4.3 item 3):
// an environment whose host-ownership contract is unstated is not sealed, and that is the
// ONE place the unset state bites. Every other caller wants the mode, in which unset and
// `none` are the same answer by ruling (OQ-CO14) — so the distinction is offered here rather
// than left for each caller to re-derive from the file.
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
		return HostManagementNone, false
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

// HostManagementRetired is the targeted refusal for a USER config that still says the retired
// `"assert"` (OQ-CO14 face 1), or "" when it does not. Every host verb that would have written
// under `assert` prints it in place of its `none` sentence — `yolo host apply` in both
// spellings, `--revert`, a wrapped `yolo host -- <bin>` launch and `yolo pack update`'s host
// half — because the value resolves to `none` (hostManagementValue) and a refusal saying
// `"none"` to a user whose file says `"assert"` would be a sentence about a file they did not
// write. ValidateConfig prints the same text through hostManagementProblem.
//
// Read from user scope like the mode, strictly: an unreadable config is not a retired value.
func HostManagementRetired() string {
	p := paths.UserConfigPath()
	cfg, err := loadUserScopeConfig(p, p, true, func(string) {})
	if err != nil || cfg == nil {
		return ""
	}
	v, _ := cfg.Get(hostManagementKey)
	if s, ok := v.(string); ok && s == retiredHostManagementAssert {
		return retiredHostManagementProblem()
	}
	return ""
}

// retiredHostManagementProblem is the retirement message's body, after each reader's own
// prefix (`config.host_management: ` in validation, `yolo host apply: host_management: ` at a
// verb). It names both values left, what each does to a home `assert` wrote into, and the verb
// that goes with each: `--revert` under `none`, `yolo config promote` before `own`.
func retiredHostManagementProblem() string {
	return `"assert" is RETIRED — it shared your agents' config files between you and yolo, ` +
		`and two values are left. "none", the default, has yolo write nothing into your home ` +
		`and leave those files as they are (` + "`yolo host apply --revert`" + ` takes out ` +
		`the keys yolo wrote); "own" has yolo compose them whole from your packs (` +
		"`yolo config promote`" + ` first declares a key you keep by hand into your local ` +
		`pack). Set one in ` + paths.UserConfigPath() + `, or delete the key for "none".`
}

// hostManagementProblem reports why a value is not a usable `host_management`, or "" when it
// is fine. Shared by the validator and by nothing else today; it exists as a function so the
// accepted values are stated once, from KnownHostManagements. The retired `"assert"` is the
// one value with a message of its own.
func hostManagementProblem(v any) string {
	s, ok := v.(string)
	if !ok {
		return "expected one of " + hostManagementList() + " (got " + pyReprValue(v) + ")"
	}
	if s == retiredHostManagementAssert {
		return retiredHostManagementProblem()
	}
	for _, known := range KnownHostManagements {
		if HostManagement(s) == known {
			return ""
		}
	}
	return pyReprValue(v) + " is not one of " + hostManagementList()
}

// hostManagementList renders the accepted values for a message: `"none"`, `"own"`.
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
