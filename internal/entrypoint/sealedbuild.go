package entrypoint

// sealedbuild.go is the jail's half of a SEALED BUILD JAIL: a jail the host launched to build a
// fork's program or a patched extension's tree under the seal (docs/design/forked-programs-as-packs.md
// FP-D9), with its pack selection narrowed to the packs that build needs (patched-extensions.md
// PPX-D5, PPX-D39). It runs one build line and no agent, so the boot renders no pack-declared
// surface and runs no pack hook there (PPX-D41): each one is an agent's configuration or an agent's
// session state, and the narrowing leaves a contributing pack's surface for another pack's agent
// with no writable home directory to land in. Core's own surfaces still render, mise/config among
// them.

// SealedBuildEnv tells a jail, in a sealed build jail alone, that it is one: `1`, on a fork's build
// and a patched extension's alike (run.Options.Sealed). Additive across the host↔jail contract,
// whose two halves deploy on different cadences: an entrypoint that predates it renders every
// staged pack's surfaces, as before, and a host that predates it still tells a patched extension's
// build jail TreeBuildEnv, which sealedBuild reads too.
const SealedBuildEnv = "YOLO_SEALED_BUILD"

// sealedBuild reports whether this boot is a sealed build jail's: told so (SealedBuildEnv), or told
// which patched extension it builds (TreeBuildEnv, PPX-D30), which only a sealed build jail is.
func (e *Env) sealedBuild() bool {
	return e.Getenv(SealedBuildEnv) != "" || e.Getenv(TreeBuildEnv) != ""
}

// sealedBuildSkipNote is the boot log's record of what a sealed build jail's boot did not do.
const sealedBuildSkipNote = "configure_pack_surfaces: a sealed build jail runs no agent, so no " +
	"pack-declared surface is rendered and no pack hook runs"
