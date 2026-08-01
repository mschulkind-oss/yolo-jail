package render

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// FieldSet declares which contribution kinds a target can honor, so an inapplicable
// kind produces a refusal that NAMES the kind rather than a silent skip (BACKLOG G5,
// host-render-target.md §2.1/§6.2). The silent skip is the failure mode G3 shipped —
// a backend rendering zero surfaces every launch with nothing in the output to say so.
//
// The census (host-render-target.md §2.1, restated for the twelve contributes[] kinds):
// only the composed-config kinds are target-independent; the provisioning kinds mean
// nothing without a container.
type FieldSet struct {
	// applies is the set of kinds this target honors. A kind absent from the map is
	// refused by name (Refuse below).
	applies map[packdecl.Kind]bool
}

// Honors reports whether the target renders this kind. A kind the FieldSet does not
// list is refused — callers use Refuse to produce the message.
func (f FieldSet) Honors(k packdecl.Kind) bool { return f.applies[k] }

// Refuse returns a one-line reason a kind is not honored on this target, or "" if it
// is honored (the caller should not have asked). The reasons are the census's, so the
// message tells the user why, not just that.
func (f FieldSet) Refuse(k packdecl.Kind) string {
	if f.applies[k] {
		return ""
	}
	if r, ok := refusalReasons[k]; ok {
		return r
	}
	return string(k) + " is not applicable at this confinement level"
}

// refusalReasons is the census reason per kind, used when a non-jail target refuses one.
var refusalReasons = map[packdecl.Kind]string{
	packdecl.KindProgram:   "install is refused below jail (a pack must not mutate a real toolchain unprompted)",
	packdecl.KindMount:     "mount needs a mount namespace — unavailable without a container",
	packdecl.KindReadsHost: "reads-host carries a host file INTO a jail — meaningless when there is no jail",
	packdecl.KindState:     "state names a jail-writable home subtree — off-container the home simply is writable",
	packdecl.KindFiles:     "files binds a pack tree into a jail — nothing to bind into off-container",
}

// JailFields is every kind: a jail honors the whole manifest.
func JailFields() FieldSet {
	all := map[packdecl.Kind]bool{}
	for _, k := range packdecl.KnownKinds() {
		all[k] = true
	}
	return FieldSet{applies: all}
}

// HostFields is the reduced set a host/guest target honors: the composed-config and
// prose kinds port; env is static; program is confirm-gated (honored, but the CALLER
// gates it — the FieldSet says it applies); the provisioning kinds are refused. This
// is §2.1's census as executable data.
//
// config-overlay tracks config (it lands in a composed surface). launch and hook are
// notch-dependent in degree, not applicability, so they are honored here and the caller
// narrows them (e.g. only 1 of 3 hooks on host); keeping them in the set means "this
// target can express them," which is true.
func HostFields() FieldSet {
	honored := map[packdecl.Kind]bool{
		packdecl.KindConfig:        true,
		packdecl.KindConfigOverlay: true,
		packdecl.KindSkills:        true,
		packdecl.KindBriefing:      true,
		packdecl.KindEnv:           true,
		packdecl.KindLaunch:        true,
		packdecl.KindHook:          true,
		packdecl.KindProgram:       true, // honored but confirm-gated by the caller (OQ-6/7)
	}
	return FieldSet{applies: honored}
}

// Fields returns the FieldSet for this target's kind.
func (t Target) Fields() FieldSet {
	if t.KindOf() == KindJail {
		return JailFields()
	}
	return HostFields()
}
