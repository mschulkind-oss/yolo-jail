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
// build jail TreeBuildEnv, which launchedSealed reads too.
const SealedBuildEnv = "YOLO_SEALED_BUILD"

// launchedSealed reports whether the launcher made this boot a sealed build jail's: told so
// (SealedBuildEnv), or told which patched extension it builds (TreeBuildEnv, PPX-D30), which only
// a sealed build jail is. The launcher's word is its own `-e` argv, so runSteps asks before
// hydrate_user_env folds ~/.config/yolo-user-env.sh into Vars: that file carries a selected pack's
// ungated `env` vars and the user's env_sources, neither of which may switch a jail's agents off.
// A macos-user boot is never one, since that backend runs no sealed build (FP-D3), and its
// environment relays the same channel, so a gate name in it is never the launcher's.
func (e *Env) launchedSealed(target bootTarget) bool {
	if target != bootContainer {
		return false
	}
	return e.Getenv(SealedBuildEnv) != "" || e.Getenv(TreeBuildEnv) != ""
}

// sealedBuildSkipNote is the boot log's record of what a sealed build jail's boot did not do.
const sealedBuildSkipNote = "configure_pack_surfaces: a sealed build jail runs no agent, so no " +
	"pack-declared surface is rendered and no pack hook runs"
