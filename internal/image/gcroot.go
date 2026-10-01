package image

import (
	"errors"
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

// ImageStoreKey is the first 16 hex chars of sha256(storePath) — the SAME key
// ImageCachePath uses. Both the cache tar (cache/images/<key>.tar) and the GC
// root (build/roots/<key>) are keyed by it, so a reaper (or the storage §3
// store-GC rooting confirmation in internal/prune) can correlate a root with the
// store path it pins without a reverse lookup, against any BUILD_DIR.
func ImageStoreKey(storePath string) string {
	return keyFor(storePath)
}

// ImageRootLink is the durable GC-root symlink path for a store path (whether or
// not it exists yet). The reaper enumerates ImageRootsDir directly; this is for
// callers that want the specific link for one store path.
func ImageRootLink(storePath string) string {
	return filepath.Join(ImageRootsDir(), ImageStoreKey(storePath))
}

// RegisterImageRoot creates a durable, per-image nix GC root for storePath so
// the running image's store closure survives an arbitrary `nix-collect-garbage`.
// The root is an indirect gcroot at BUILD_DIR/roots/<sha16>, keyed by
// sha256(storePath)[:16] (ImageStoreKey) — so re-running the same image reuses
// one root and distinct images each keep their own. Returns the link path.
//
// root is HOW the link is registered, and the caller picks it by where it runs
// (run's gcRooter): AddRoot on the host, and from inside a jail the translated
// root of docs/design/in-jail-nix-roots.md §4. A jail cannot use AddRoot: its
// `nix-store --add-root` sends the jail's spelling of the link, which does not
// exist on the host, so the host daemon prunes the root as stale — verified
// 2026-07-22 (an in-jail `--add-root` makes the symlink, `--query --roots` stays
// empty) and the mechanism measured 2026-09-28 (that doc's §2 and §3).
//
// Best-effort: any failure is reported by root and swallowed by every caller — an
// unrooted-but-running jail is the pre-existing state, not a regression this must
// hard-fail on.
func RegisterImageRoot(storePath string, root Rooter, out io.Writer) (string, error) {
	if out == nil {
		out = io.Discard
	}
	rootsDir := ImageRootsDir()
	link := filepath.Join(rootsDir, ImageStoreKey(storePath))
	return registerGCRoot(rootsDir, link, storePath, root, out,
		"could not register GC root for the running image (a nix-collect-garbage could reclaim it)")
}

// A Rooter makes link an indirect nix GC root for storePath, creating or
// replacing the symlink itself. A failure it judges worth saying is written to
// out as a warning that begins with failMsg, which names what is left
// unprotected; the error is returned either way.
type Rooter func(link, storePath string, out io.Writer, failMsg string) error

// AddRoot is the host's Rooter: `nix-store --add-root <link> --realise
// <storePath>`. --add-root creates an indirect GC root (a symlink under
// gcroots/auto/ back to <link>); --realise on an already-valid path returns it
// without building or substituting. Combined, this pins the closure without side
// effects.
//
// It also REFRESHES the link's own mtime even when the link already points at
// this exact store path — which is what makes an age policy meaningful for the
// image roots without any bookkeeping of its own (PruneOrphanImageRoots). Every
// failure is a warning with nix-store's own output, as it always was.
func AddRoot(link, storePath string, out io.Writer, failMsg string) error {
	cmd := exec.Command("nix-store", "--add-root", link, "--realise", storePath)
	if outbuf, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(out, "Warning: "+failMsg+": "+err.Error())
		if len(outbuf) > 0 {
			fmt.Fprintln(out, "  "+string(outbuf))
		}
		return err
	}
	return nil
}

// errNoRooter is a registration handed no Rooter: a launch with no root the host would honor
// passes none rather than asking, so nothing is created.
var errNoRooter = errors.New("no GC-root registrar for this launch")

// registerGCRoot is the shared mechanism behind RegisterImageRoot and
// RegisterPrefixRoot: create the directory, register the link through root,
// swallow failure with a named warning.
//
// Factored out with the DIRECTORY as a parameter and the two callers as separate
// exported functions on purpose. The two roots have DIFFERENT retention policies
// — images reap on age unless a container runs on the image (OQ-LS1, OQ-LS4),
// prefixes are held by the liveness of the jail executing from them (OQ-BF4) —
// so the directory is the only thing that keeps them apart, and a caller choosing
// it from a variable is how they would silently merge.
func registerGCRoot(rootsDir, link, storePath string, root Rooter, out io.Writer, failMsg string) (string, error) {
	if root == nil {
		return "", errNoRooter
	}
	if err := os.MkdirAll(rootsDir, 0o755); err != nil {
		fmt.Fprintln(out, "Warning: could not create GC-root dir: "+err.Error())
		return "", err
	}
	if err := root(link, storePath, out, failMsg); err != nil {
		return "", err
	}
	return link, nil
}
