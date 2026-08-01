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
// is what renders — the §4.2 fix for the apply --host bypass leak.
func HostProfile() Profile { return Profile{prims: map[Primitive]bool{}, AgentAutonomy: false} }
