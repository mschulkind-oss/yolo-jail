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
	// `loophole` needs its own reason, and the reason is the INVERSE of the generic line.
	// A loophole's effect IS on the host — it spawns a daemon there — so "not applicable at
	// this confinement level" would be the single most confusing sentence in the command:
	// it reads as obviously wrong to anyone who knows what a loophole does.
	//
	// The honest reason is that its COUNTERPARTY is missing, not its mechanism. With no jail
	// there is no client for the daemon, no `--add-host` to write, no YOLO_JAIL_DAEMONS to
	// populate, and nothing for the endpoint file to be mounted into.
	//
	// And the refusal is a FEATURE of the trust story rather than a limitation to fix later.
	// `yolo host apply` is the one command that mutates the real machine, and it deliberately
	// runs no pack `hook` for the same reason. A loophole refused here means "selecting this
	// pack runs a daemon" is a statement about LAUNCHING A JAIL, not about applying a
	// config — which keeps the blast radius attached to a command the user runs deliberately.
	packdecl.KindLoophole: "a loophole is a host daemon whose only client is a container: " +
		"with no jail there is no client, no --add-host, no YOLO_JAIL_DAEMONS, and nothing " +
		"for its endpoint file to be mounted into. Launch a jail to run it",
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
	// `launch` and `env` are honored by the census and unbuilt for ONE reason, and the
	// wording has to name it precisely (plan §6b D3): `yolo host apply` never launches a
	// process. It is a limit of this COMMAND, not of the notch. The old text — "launch flags
	// need a launcher", "the only place to set these off-container is your shell profile" —
	// read as facts about being off-container, which they are not: at `guest` yolo already
	// execs the agent (macos-user does it today), and `yolo --at host -- <cmd>` (design §4.1)
	// would make both renderable at the host notch too, because then yolo is the one
	// spawning the process and can carry an argv and an environment. A `guest` target
	// inheriting the old sentences would refuse two kinds it can honor, which is exactly the
	// silent-inheritance failure the explicit Kind exists to stop.
	//
	// So both say the same thing about the same missing VERB, and the remedy is the same
	// one — which is why they are two entries with one reason rather than two reasons.
	packdecl.KindLaunch: "launch flags apply to a process yolo starts, and `yolo host apply` " +
		"only configures your tools — it never runs them, so there is no argv to inject " +
		"them into. `yolo host -- <program>` is the notch that does the launching",
	packdecl.KindEnv: "env vars apply to a process yolo starts, and `yolo host apply` only " +
		"configures your tools — it never runs them. Setting them for your whole session " +
		"would mean editing your shell rc, a much larger claim than a pack's env " +
		"contribution asks for. `yolo host -- <program>` delivers them at launch instead, " +
		"to that process only",
	// provider rides the same channel and so hits the same limit of the same COMMAND: its
	// service facts compose into the providers table a launch carries into the derives.
	// A host apply renders config files, and nothing in those files is a provider — an
	// agent's provider catalog is derived at launch, from the table, not written here.
	packdecl.KindProvider: "a shipped provider's facts feed the derives at LAUNCH, and " +
		"`yolo host apply` only configures your tools — it never runs one, so there is no " +
		"derive to feed. `yolo host -- <program>` (or a jail launch) composes the providers " +
		"table instead",
	// The three shipped hooks are all jail plumbing: shared_credentials symlinks a
	// credentials file into a machine-global dir, per_jail_history isolates a history
	// file PER JAIL, claude_plugins reconciles in-jail plugin installs. Off-container
	// each is either meaningless or a mutation of real user state that no pack should
	// perform unprompted. Refused deliberately, not merely unbuilt.
	packdecl.KindHook: "hooks are jail provisioning steps (credential symlinks, " +
		"per-jail history, plugin reconciliation) — `yolo host apply` does not run them " +
		"against your real home",
	// profile is the same limit of the same command, arrived at from the selector rather
	// than the verb: which variant of a pack applies is a LAUNCH decision (use_profiles /
	// -p), and this command writes config without launching anything, so it has no variant
	// to select and writes none — your base surfaces, unmodified. A jail launch (or a
	// future `yolo host -- <program>`) is where a selection exists to be honored.
	packdecl.KindProfile: "a profile is a VARIANT of this pack's own config, selected at " +
		"launch — and `yolo host apply` selects none, so it writes the pack's base surfaces " +
		"only. Launch a jail with `-p <name>` (or `--pack-profile <cli>=<name>`) to apply the " +
		"variant",
}

// HostUnimplemented returns the reason a kind is honored-but-unbuilt at a host target, and
// ok=false once it is implemented (or was never in the set). Callers report it the same way
// they report a refusal — the point is that NOTHING a pack declares is silently absent.
func HostUnimplemented(k packdecl.Kind) (string, bool) {
	r, ok := hostUnimplemented[k]
	return r, ok
}

// JailFields is every kind a jail RENDERS, which is every kind except the ones rendered
// by something other than the render path.
//
// Derived from packdecl.KnownKinds() minus jailRenderedElsewhere, so a new kind is honored
// by default (a jail is the maximal target) and an exclusion has to be written down.
func JailFields() FieldSet {
	all := map[packdecl.Kind]bool{}
	for _, k := range packdecl.KnownKinds() {
		if jailRenderedElsewhere[k] {
			continue
		}
		all[k] = true
	}
	return FieldSet{applies: all}
}

// jailRenderedElsewhere names the kinds whose jail-side effect exists but is NOT produced
// by the render path this FieldSet describes. Excluded EXPLICITLY rather than by
// derivation, because the census is supposed to be executable data and an entry it derives
// from nothing is an assertion no code reads.
//
// `loophole` is the case. Its jail-side effects are real — `--add-host`, bind mounts,
// devices, YOLO_JAIL_DAEMONS, an endpoint file — and every one of them is produced by
// `startLoopholes` in the HOST CLI, before the container exists, not by
// entrypoint.ConfigurePackSurfaces. Measured while designing the kind: `Target.Fields()`
// has no production caller at all (the only consumer of a FieldSet is
// `render.HostFields()`, at apply.go), so deriving `loophole: true` from KnownKinds()
// would have made the census claim "the jail render honors this" — true of nothing.
//
// The honest census answer at `jail` is therefore "rendered elsewhere; its actor is the
// run pipeline". A caller asking Honors() gets false and Refuse() gives the counterparty
// reason (refusalReasons), which is the right answer for the jail target too: whatever
// renders a loophole, it is not this.
var jailRenderedElsewhere = map[packdecl.Kind]bool{
	packdecl.KindLoophole: true,
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
		// provider is honored at this notch, and the reason is the channel rather than the
		// census: a shipped provider's facts compose into the providers table the LAUNCH
		// carries, exactly as a pack's `env` does — they are not a file this command writes.
		// Honored-but-unbuilt below is that limit stated (hostUnimplemented), the same
		// sentence env and launch get.
		packdecl.KindProvider: true,
		// profile is honored in the sense the census means — since OQ-PT8 it IS a
		// selection (`name` + `provider`), not a patch of its own, so there is nothing to
		// apply at this notch and nothing to gate either: it carries no key a config write
		// could render. Its config half lives in `config-overlay` contributions, which
		// reach the host render through the same collector the boot uses — with no table
		// passed, a gated overlay is a clean skip rather than a written key.
		packdecl.KindProfile: true,
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
//
// A SWITCH naming every kind rather than "jail, else host" (Q1). The `if` was correct while a
// Kind could only be one of three INFERRED values, and becomes a silent over-permission the
// moment a fourth exists — so the point of writing it out is that the default is now a
// decision on the record. That default is the REDUCED set, which is the fail-closed direction:
// a kind wrongly refused is a message, a kind wrongly honored is a write nobody asked for. In
// particular `guest` must not fall into the jail set, which would honor
// `mount`/`reads-host`/`state` at a notch with no mount namespace to honor them with; its real
// census is Phase 7's to state.
//
// KindPreview is in the default branch because that is where the shape inference put it, and
// this change is meant to be behavior-preserving — not because it is obviously right. A
// preview exists to show what the JAIL render produces, so the jail set is the likelier
// answer; nothing depends on it either way today (render.Preview has no production caller), so
// the wrong-looking half is at least now VISIBLE as a listed case rather than a fallthrough.
func (t Target) Fields() FieldSet {
	switch t.KindOf() {
	case KindJail:
		return JailFields()
	default: // KindHost, KindGuest, KindPreview, KindUnset
		return HostFields()
	}
}
