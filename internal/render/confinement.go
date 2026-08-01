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
type Profile struct {
	prims map[Primitive]bool
}

// Has reports whether the profile composes a primitive.
func (p Profile) Has(prim Primitive) bool { return p.prims[prim] }

// with returns a Profile composing exactly the given primitives.
func with(prims ...Primitive) Profile {
	m := map[Primitive]bool{}
	for _, p := range prims {
		m[p] = true
	}
	return Profile{prims: m}
}

// JailProfile is the strongest preset: namespaces (or a VM on Apple Container) plus a
// baked image. The mechanism arg picks namespaces vs VM — the dial is ordinal within a
// platform, not absolute across, so `jail` means "the strongest here."
func JailProfile(useVM bool) Profile {
	if useVM {
		return with(PrimVM, PrimBakedImage)
	}
	return with(PrimNamespaces, PrimBakedImage)
}

// GuestProfileMacOS is macOS `guest`: a separate user (credential boundary) + Seatbelt
// (confinement), a real home, no image.
func GuestProfileMacOS() Profile { return with(PrimSeparateUser, PrimSeatbelt) }

// GuestProfileLinux is Linux `guest`: bwrap namespaces + Landlock, a real home, NO
// separate user (bwrap uses the same namespace primitive podman does) — the "weaker
// container, not a second account" combination the primitive model exists to express as
// a preset rather than a special case.
func GuestProfileLinux() Profile { return with(PrimNamespaces, PrimLandlock) }

// HostProfile is the weakest preset: no primitives at all — you, on your machine.
func HostProfile() Profile { return Profile{prims: map[Primitive]bool{}} }
