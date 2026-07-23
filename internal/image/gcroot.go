package image

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ImageRootsDir is where per-image durable nix GC roots live:
// BUILD_DIR/roots/<sha16>. Distinct from the per-PID build out-link
// (run-result-<pid>), which is removed the instant a build finishes — these
// roots persist for the lifetime of the loaded image so a `nix-collect-garbage`
// at any moment cannot delete the running jail's closure.
func ImageRootsDir() string {
	return filepath.Join(paths.BuildDir(), "roots")
}

// imageStoreKey is the first 16 hex chars of sha256(storePath) — the SAME key
// ImageCachePath uses. Both the cache tar (cache/images/<key>.tar) and the GC
// root (build/roots/<key>) are keyed by it, so a reaper can correlate a root
// with the store path it pins without a reverse lookup.
func imageStoreKey(storePath string) string {
	return keyFor(storePath)
}

// ImageRootLink is the durable GC-root symlink path for a store path (whether or
// not it exists yet). The reaper enumerates ImageRootsDir directly; this is for
// callers that want the specific link for one store path.
func ImageRootLink(storePath string) string {
	return filepath.Join(ImageRootsDir(), imageStoreKey(storePath))
}

// RegisterImageRoot creates a durable, per-image nix GC root for storePath so
// the running image's store closure survives an arbitrary `nix-collect-garbage`.
// The root is an indirect gcroot at BUILD_DIR/roots/<sha16>, keyed by
// sha256(storePath)[:16] (imageStoreKey) — so re-running the same image reuses
// one root and distinct images each keep their own. Returns the link path.
//
// MUST run host-side. From inside a jail /nix/var/nix/gcroots is not mounted and
// the host daemon prunes any root pointing into the jail's /home tree as stale
// (that path does not exist on the host filesystem) — verified 2026-07-22: an
// in-jail `nix-store --add-root` creates the symlink but `--query --roots` stays
// empty. The run slice gates the call on !inJail; in-jail the seam is a no-op.
//
// Best-effort: `nix-store --add-root … --realise <path>` is a no-op realisation
// for an already-valid store path (no substitution/download), and any failure is
// logged + swallowed — an unrooted-but-running jail is the pre-existing state,
// not a regression this must hard-fail on.
func RegisterImageRoot(storePath string, out io.Writer) (string, error) {
	if out == nil {
		out = io.Discard
	}
	rootsDir := ImageRootsDir()
	if err := os.MkdirAll(rootsDir, 0o755); err != nil {
		fmt.Fprintln(out, "Warning: could not create GC-root dir: "+err.Error())
		return "", err
	}
	link := filepath.Join(rootsDir, imageStoreKey(storePath))
	// --add-root creates an indirect GC root (a symlink under gcroots/auto/ back
	// to <link>); --realise on an already-valid path returns it without building
	// or substituting. Combined, this pins the closure without side effects.
	cmd := exec.Command("nix-store", "--add-root", link, "--realise", storePath)
	if outbuf, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(out, "Warning: could not register GC root for the running image "+
			"(a nix-collect-garbage could reclaim it): "+err.Error())
		if len(outbuf) > 0 {
			fmt.Fprintln(out, "  "+string(outbuf))
		}
		return "", err
	}
	return link, nil
}
