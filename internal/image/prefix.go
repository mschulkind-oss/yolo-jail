package image

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// prefix.go realizes the /opt/yolo-jail install prefix the launch BIND-MOUNTS
// into the jail — the half of yolo that used to be baked into the image.
//
// WHY THIS EXISTS AT ALL. The image derivation no longer contains `goSrc`
// (flake.nix: installPrefix left corePackages), which is what makes a commit
// touching only cmd/ or internal/ cost no image rebuild and no `podman load`.
// The binaries still have to come from somewhere, and there are exactly two
// somewheres:
//
//   - A flake source that SHIPS prebuilt binaries — every bundle does
//     (`bin/linux-<arch>/`, staged by scripts/stage-source-bundle.sh, baked into
//     the mounted prefix by installPrefix itself, so a nested jail inherits one).
//     Nothing to build; the run path mounts that directory straight through.
//   - A LIVE CHECKOUT, which ships none. That is this file: one
//     `nix build .#installPrefix`, whose output has the prefix layout already.
//
// NO macOS OFFLOAD, and that is not an omission. `installPrefix` is built by
// `pkgs.runCommand` out of `goBinaries`, which cross-compiles with the HOST Go
// toolchain (CGO_ENABLED=0, GOOS=linux) — so on darwin it is a DARWIN derivation
// producing Linux binaries and needs no Linux builder, unlike `.#ociImage`. The
// container-builder session (builderoffload.go) is therefore not reachable from
// here, and adding it would start a builder container for a build that never
// needs one.
//
// THE OUT-LINK IS THE GC ROOT. Unlike the image's per-PID `run-result-<pid>`
// link (removed the instant the build finishes, with a durable
// `build/roots/<sha16>` registered in its place), the prefix keeps its out-link:
// it is an indirect nix GC root, one per source tree, replaced in place on every
// build. It must NOT go in `build/roots/`, whose reaper (prune.PruneOrphanImageRoots)
// deletes any root that is not a LOADED IMAGE — it would collect the very
// binaries the running jail executes. It must also not be named `run-result-*`,
// which prune.SweepDanglingOutLinks scans.

// JailPrefixOutLink is the durable `nix build --out-link` path for one source
// tree's install prefix: BUILD_DIR/jail-prefix-<sha16 of repoRoot>. Keyed by the
// SOURCE, not by the output, so re-building the same tree replaces one link
// rather than accumulating one per build — at most one prefix closure is pinned
// per checkout, and it is always the current one.
func JailPrefixOutLink(repoRoot string) string {
	return filepath.Join(paths.BuildDir(), "jail-prefix-"+keyFor(repoRoot))
}

// BuildJailPrefix builds `.#installPrefix` in repoRoot and returns the store
// path of the realized prefix, plus the retained nix stderr tail. A "" store
// path means the build failed (runNixBuild's contract) — the caller must refuse
// the launch rather than proceed, because the jail would have no yolo-entrypoint
// to exec.
//
// The returned path is the derivation root; the mountable prefix is
// <storePath>/opt/yolo-jail (JailPrefixSubdir).
func BuildJailPrefix(repoRoot string, out io.Writer) (string, []string) {
	if out == nil {
		out = io.Discard
	}
	outLink := JailPrefixOutLink(repoRoot)
	if err := os.MkdirAll(filepath.Dir(outLink), 0o755); err != nil {
		return "", []string{"could not create build dir: " + err.Error()}
	}
	fmt.Fprintln(out, "Building yolo's own binaries (.#installPrefix) — "+
		"they are mounted into the jail, not baked into the image…")
	return runNixBuild(
		flakeBuildArgv(installPrefixAttr, outLink, nil),
		repoRoot, os.Environ(), outLink, out)
}

// JailPrefixSubdir is the path INSIDE the installPrefix store output that has
// the prefix layout (bin/ + share/yolo-jail/). It mirrors the in-jail
// destination, which is what lets the same derivation serve as a host install
// prefix and as the jail's mount source.
const JailPrefixSubdir = "opt/yolo-jail"
