package render

// confinement.go models confinement as a set of independent PRIMITIVES with the three
// notches as presets over them (env-manager plan Phase 2 / OQ-10, design §4.0). The
// point, from the review: the enforcement primitives compose — a separate OS user, a
// Seatbelt profile, a bwrap/Landlock sandbox, a user namespace are independent knobs,
// and real combinations exist (a separate user without Seatbelt; a namespace with
// neither). Building the internal model to express those combinations from the start is
// what keeps a fourth combination (a Linux `guest`) from being a bolted-on special case
// rather than another preset.
//
// happy-path-principle.md still rules: only the three named presets are user-selectable
// (the `confinement` key). The primitive vector is an implementation fact that
// `describe` can print, not a matrix the user hand-assembles. So this file gives a
// primitive set and the three presets over it — not a config surface.

import "strconv"

// Primitive is one independent enforcement mechanism a confinement level may compose.
type Primitive int

const (
	// PrimNamespaces: Linux user/mount/pid/etc. namespaces — the container primitive
	// (podman) and the bwrap primitive share it. The strongest filesystem/process
	// isolation available without a VM.
	PrimNamespaces Primitive = iota
	// PrimVM: a per-container virtual machine (Apple Container). Materially stronger
	// than namespaces; why `jail` on macOS-AC is stronger than `jail` on podman.
	PrimVM
	// PrimSeatbelt: a macOS Seatbelt (sandbox_init) profile — a syscall/file-access
	// policy, weaker than a namespace, applied to a process in a real home.
	PrimSeatbelt
	// PrimLandlock: Linux Landlock LSM filesystem restrictions — the bwrap `guest`'s
	// confinement primitive, the rough Linux analogue of Seatbelt.
	PrimLandlock
	// PrimSeparateUser: a distinct OS user account (macos-user today) — a CREDENTIAL
	// boundary (own home, own keychain reach), orthogonal to the confinement above.
	PrimSeparateUser
	// PrimBakedImage: a nix-built OCI image (the jail's package closure). A provisioning
	// primitive, not a confinement one, but it travels with the jail notch and is absent
	// below it.
	PrimBakedImage
)

// PrimitiveOrder fixes the order primitives are listed in for OUTPUT. A Profile stores its
// primitives in a map, so without a fixed order the same notch would describe itself
// differently per run — and the whole point of stating the vector is that two notches (or two
// machines) can be compared. Declaration order above, strongest first.
//
// Returned as a fresh slice so a caller cannot reorder the canonical one in place.
func PrimitiveOrder() []Primitive {
	return []Primitive{
		PrimNamespaces, PrimVM, PrimSeatbelt, PrimLandlock, PrimSeparateUser, PrimBakedImage,
	}
}

// primitiveDoes names each enforcement primitive by WHAT IT DOES, not by its mechanism name
// alone: "Landlock" and "namespaces" mean nothing to most readers, and a vector nobody can
// interpret is not an improvement over not stating it.
//
// HERE, in the package that defines Primitive, because more than one surface renders this
// vector for a human — `yolo describe` prints it, and the per-notch briefing prose describes it
// to an agent. Two independent wordings for one primitive drift, and the reader who hits the
// disagreement has no way to tell which is current. One table, two consumers.
var primitiveDoes = map[Primitive]string{
	PrimNamespaces:   "namespaces — kernel-isolated filesystem, processes, PIDs and network",
	PrimVM:           "a virtual machine — its own kernel; the strongest boundary available",
	PrimSeatbelt:     "Seatbelt — a macOS kernel policy on which files and syscalls are allowed",
	PrimLandlock:     "Landlock — a Linux kernel policy on which paths are reachable",
	PrimSeparateUser: "a separate OS user — its own home and keychain reach (a credential boundary)",
	PrimBakedImage:   "a baked image — a nix-built package set, not your machine's PATH",
}

// PrimitiveDoes is the canonical one-line description of what a primitive does, for any
// human-facing surface. A primitive with no entry describes itself by number rather than
// returning "", so a newly added one shows up in the output as unlabelled instead of leaving
// a blank in it — the same rule Kind.String follows.
func PrimitiveDoes(p Primitive) string {
	if s, ok := primitiveDoes[p]; ok {
		return s
	}
	return "Primitive(" + strconv.Itoa(int(p)) + ")"
}

// Profile is a confinement level as a composed set of primitives — the thing the code
// assembles and `describe` prints. The three constructors below are the presets; a
// Profile with a non-preset combination is expressible (that is the point) but is not
// something the `confinement` config key can name.
//
// Beside the enforcement primitives it also carries one POLICY bit, AgentAutonomy (§4.2):
// whether packs render their AUTONOMOUS posture (permission prompts off — the "YOLO" mode
// that is safe only because something contains the agent) or their GUARDED posture (prompts
// on). It is a policy knob, not an enforcement mechanism, so it is a field rather than a
// Primitive; the presets set it (jail/guest on, host off) and a composed custom confinement
// can override it.
type Profile struct {
	prims map[Primitive]bool
	// AgentAutonomy is the §4.2 policy: true → render each pack's autonomous posture,
	// false → its guarded posture. On at jail/guest (the agent is contained), off at host.
	AgentAutonomy bool
}

// Has reports whether the profile composes a primitive.
func (p Profile) Has(prim Primitive) bool { return p.prims[prim] }

// with returns a Profile composing exactly the given primitives. autonomy sets the
// AgentAutonomy policy bit (§4.2) — the presets pass it explicitly so the default is
// never accidental.
func with(autonomy bool, prims ...Primitive) Profile {
	m := map[Primitive]bool{}
	for _, p := range prims {
		m[p] = true
	}
	return Profile{prims: m, AgentAutonomy: autonomy}
}

// JailProfile is the strongest preset: namespaces (or a VM on Apple Container) plus a
// baked image. The mechanism arg picks namespaces vs VM — the dial is ordinal within a
// platform, not absolute across, so `jail` means "the strongest here." Autonomy ON: the
// jail is the safety net, so the agent runs without permission prompts.
func JailProfile(useVM bool) Profile {
	if useVM {
		return with(true, PrimVM, PrimBakedImage)
	}
	return with(true, PrimNamespaces, PrimBakedImage)
}

// GuestProfileMacOS is macOS `guest`: a separate user (credential boundary) + Seatbelt
// (confinement), a real home, no image. Autonomy ON: still confined.
func GuestProfileMacOS() Profile { return with(true, PrimSeparateUser, PrimSeatbelt) }

// GuestProfileLinux is Linux `guest`: bwrap namespaces + Landlock, a real home, NO
// separate user (bwrap uses the same namespace primitive podman does) — the "weaker
// container, not a second account" combination the primitive model exists to express as
// a preset rather than a special case. Autonomy ON: still confined.
func GuestProfileLinux() Profile { return with(true, PrimNamespaces, PrimLandlock) }

// HostProfile is the weakest preset: no primitives at all — you, on your machine.
// Autonomy OFF: nothing contains the agent, so the guarded posture (permission prompts on)
// is what renders — the §4.2 fix for the `yolo host apply` bypass leak.
func HostProfile() Profile { return Profile{prims: map[Primitive]bool{}, AgentAutonomy: false} }

// ProfileFor is the notch → preset table: the ONE place that answers "what does this
// confinement level imply?", so a render path reads the policy instead of choosing a
// true/false literal per file (plan §6c step 1). Before this, `Profile` had no production
// caller at all while `AgentAutonomy` was decided by hardcoded booleans at four call sites —
// the same rot as an inferred Kind, in a second place.
//
// Total on purpose, including the zero value: an unset Kind gets HostProfile — no primitives,
// autonomy OFF. That is the fail-closed direction, and the asymmetry matters more than the
// tidiness. Getting it wrong toward `jail` means a real host gains an agent's permission
// bypass; getting it wrong toward `host` means a contained agent sees prompts it did not need.
// Only one of those is a security regression.
//
// ONE LIMITATION, stated rather than hidden: the jail and guest presets each have a platform
// variant (namespaces vs a VM; Seatbelt vs Landlock), and a render Target does not carry the
// platform — so this returns the Linux spelling of each. The difference between the variants
// is entirely in the primitive VECTOR and never in the policy bit (both jail variants contain
// the agent; so do both guests), so no decision this table feeds today can turn on it. When
// `describe` prints the vector (plan Q7) it must source it from the backend that knows the
// platform, NOT from here.
func ProfileFor(k Kind) Profile {
	switch k {
	case KindJail:
		return JailProfile(false)
	case KindGuest:
		return GuestProfileLinux()
	case KindPreview:
		// A preview shows what the JAIL render produces (that is what `yolo config render`
		// is for), so it must carry the jail's policy — previewing the guarded posture would
		// print a file the jail never writes.
		return JailProfile(false)
	default: // KindHost, KindUnset
		return HostProfile()
	}
}
