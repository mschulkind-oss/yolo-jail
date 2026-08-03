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
}

// hostUnimplemented names the kinds a host target's FieldSet HONORS but whose renderer is
// not built yet, with the reason to print. It is the fix for the failure mode G1 shipped:
// HostFields() promised `skills` and `briefing` applied, while RenderHostPack iterated
// config surfaces only — so a kind that was applicable but had no surface was neither
// rendered NOR refused, and vanished with no output line at all. `refusalReasons` could not
// cover them precisely because the census says they DO apply.
//
// Kept as DATA rather than two `if`s at the call site so each phase that implements one
// deletes an entry here instead of untangling a conditional. An empty map is the end state.
// Four of these were found by the no-silent-skip TEST, not by the gap report that
// prompted this map — G1 named only skills and briefing. `launch`, `env`,
// `config-overlay` and (visibly) `autonomy` were skipped just as silently. That is the
// argument for asserting the invariant over the whole kind set rather than patching the
// two kinds someone happened to notice.
//
// `config-overlay` was here and is GONE, which is what an entry's removal looks like: it
// is applied at both targets now (packoverlay.Collect feeds Inputs.Overlays), and like
// `autonomy` it renders INVISIBLY — an overlay folds into a surface another pack owns, so
// it shows up as that surface's own line. The caller prints the contributing packs there
// (HostRenderResult.Overlays) and names an ownerless overlay in its own line, so the kind
// still produces output on every path; it just is not this map's kind of output.
var hostUnimplemented = map[packdecl.Kind]string{
	// Honored in the census because a host target CAN express the concept, but there is
	// nothing to express it INTO: below jail, yolo launches no process, so there is no
	// shim and no argv to inject flags after.
	packdecl.KindLaunch: "launch flags need a launcher — apply --host configures your " +
		"tools but never runs them, so there is nowhere to inject them",
	// Same shape: a jail gets these as `-e` on the container. On a real host the only
	// place to put them is a shell profile, and editing your shell rc unprompted is a
	// much larger claim than a pack's env contribution asks for.
	packdecl.KindEnv: "host env render not implemented — the only place to set these " +
		"off-container is your shell profile, which apply --host does not write",
	// The three shipped hooks are all jail plumbing: shared_credentials symlinks a
	// credentials file into a machine-global dir, per_jail_history isolates a history
	// file PER JAIL, claude_plugins reconciles in-jail plugin installs. Off-container
	// each is either meaningless or a mutation of real user state that no pack should
	// perform unprompted. Refused deliberately, not merely unbuilt.
	packdecl.KindHook: "hooks are jail provisioning steps (credential symlinks, " +
		"per-jail history, plugin reconciliation) — apply --host does not run them " +
		"against your real home",
}

// HostUnimplemented returns the reason a kind is honored-but-unbuilt at a host target, and
// ok=false once it is implemented (or was never in the set). Callers report it the same way
// they report a refusal — the point is that NOTHING a pack declares is silently absent.
func HostUnimplemented(k packdecl.Kind) (string, bool) {
	r, ok := hostUnimplemented[k]
	return r, ok
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
		// requires is honored, and REPORTED with its hints — that is the kind's entire
		// host-side purpose. It asserts a binary must exist, which is exactly the question a
		// host target answers (below jail, yolo bakes no image, so every dep is the host's);
		// and it generates nothing, so there is no install to gate. Refusing it would leave a
		// content-only pack unable to carry a remedy for the tool it needs — the gap that
		// motivated the kind, since `program` was the only way to get install_hints and it
		// implies an install nobody wanted.
		packdecl.KindRequires: true,
		packdecl.KindAutonomy: true, // honored: host renders the GUARDED posture (§4.2)
		// files is honored by WRITING the tree, not binding it. The old refusal ("nothing
		// to bind into off-container") was true of the mechanism and false of the intent: a
		// pack that owns ~/.claude/file-suggestion.sh means "this file is mine to
		// maintain", and off-container the way to honor that is a real copy. Ownership does
		// NOT carry over though — see internal/entrypoint/hostfilestree.go.
		packdecl.KindFiles: true,
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
