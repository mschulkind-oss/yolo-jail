package darwinpkg

// gcroot.go answers one question: WHERE the `packages:` profile's GC root lives.
// The rooting itself is not here — it is `nix build --out-link <this path>` in
// BuildProfileArgv, because nix creating the root as part of the build it is
// already running has no window a concurrent GC can slip through (see that
// function's comment for the contrast with image.RegisterImageRoot's two-step).
//
// The defect this closes: before it, the profile was built with `--no-link` and
// nothing rooted the result. `nix store gc` — or the nix daemon's own auto-GC,
// or a `nix-collect-garbage` a user runs to free disk — could collect the
// buildEnv out from under a launch that had just resolved it, and equally out
// from under a LONG-RUNNING session still executing binaries from it. On a
// non-container notch that closure IS the agent's toolset; there is no baked
// image to fall back to.
//
// SAME HOST-SIDE CAVEAT as image.RegisterImageRoot, and for the same reason: an
// indirect gcroot is only as good as the path it points at being resolvable BY
// THE DAEMON. Verified 2026-08-05 from inside a jail — `nix build --out-link`
// does register the root (it appears in /nix/var/nix/gcroots/auto/), but the
// host daemon then reports it stale and prunes it, because the link's path is
// the jail's spelling of a directory the host mounts elsewhere. That costs
// nothing in practice: every caller of this is a NON-CONTAINER notch, which by
// definition provisions a real host home and never runs inside a jail. Worth
// knowing before someone reuses this from in-jail code and wonders why the root
// evaporates.

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// profileRootLeaf is the GC-root link's filename. FIXED, not derived from the
// store path — and that is the whole design of this root's lifetime:
//
// nix's `--out-link` REPLACES an existing link in place (verified: building a
// different derivation into the same path leaves exactly one entry, retargeted).
// So a changed `packages:` list moves this one link to the new profile and the
// OLD profile becomes collectable at the next GC, which is precisely the wanted
// behavior — the previous closure is not in use once the new one is resolved.
//
// The alternative, keying the leaf by sha256(storePath) the way
// image.ImageRootsDir does, would accumulate one permanent root per distinct
// package set the user ever configured, pinning every historical closure
// forever. That is right for images (several are legitimately live at once,
// across runtimes, and prune has a whole liveness protocol to decide which) and
// wrong here: at most one non-container profile is current per home, so a
// content-keyed root would be a slow disk leak with no reaper.
const profileRootLeaf = "packages"

// ProfileRootLink returns the GC-root path for the non-container `packages:`
// profile under the given home: <home>/.local/share/yolo-jail/build/package-roots/packages.
//
// Keyed on an EXPLICIT home rather than $HOME for the reason
// paths.GlobalStorageUnder documents: a caller that has already resolved which
// home it is provisioning must not re-derive it from the environment. There is
// one live caller (Materialize) and it passes the invoking user's home, but a
// `guest` notch provisions a home it was handed.
func ProfileRootLink(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "build", packageRootsLeaf, profileRootLeaf)
}

// packageRootsLeaf mirrors the leaf in paths.PackageRootsDir. It is spelled once
// here and asserted equal to that accessor by test, rather than calling
// PackageRootsDir directly, because that accessor resolves $HOME and this
// function must not (see ProfileRootLink).
const packageRootsLeaf = "package-roots"
