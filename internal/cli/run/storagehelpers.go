package run

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// jailMiseStoreDir returns /mise inside a jail (nested), else GLOBAL_MISE.
func jailMiseStoreDir(inJail bool) string {
	if inJail {
		return "/mise"
	}
	return paths.GlobalMise()
}

// yoloVersion resolves the yolo-jail version. version.Get resolves
// YOLO_VERSION → git describe → baked → "unknown".
func (o *Options) yoloVersion(repoRoot string) string {
	return version.Get(repoRoot)
}

// seedAgentDir is GONE (docs/design/base-home-legacy-state.md#27-the-seed). It copied every
// top-level file of <state>/home/.<dir> into each new workspace for every selected pack, and
// since the base went read-only its only inputs were legacy bytes, zero-byte mountpoint files
// and the Claude seed below — so the one thing it still did was carry an older yolo's files
// into workspaces that never asked for them. legacyseed_test.go pins its absence.

// syncClaudeJSONSeed delegates to storage.SyncClaudeJSONSeed, with the workspace side named
// beneath ws, the overlay's os.Root (wsstatebeneath.go): the file the jail reads as
// ~/.claude.json, and every directory above it, is one the jail can replace with a link.
func syncClaudeJSONSeed(seed string, ws *os.Root, wsRel string) {
	storage.SyncClaudeJSONSeed(seed, ws, wsRel)
}

// migrateOldOverlay copies the pre-refactor overlay dir srcRel below src into dstRel below
// dst, never overwriting an existing target. No-op when the old dir is missing, empty, or not
// a real directory.
//
// NOTHING ON EITHER SIDE IS FOLLOWED THROUGH A LINK (wsstatebeneath.go). Both sides are state
// a jail can write: the legacy dirs sit in the workspace overlay, and a shared dir's machine-wide
// copy is bound read-write into every jail with the pack. So a link at the legacy dir, or
// inside it, is never read through (following one copied a host tree, ~/.ssh say, into the
// jail's own state), and the copy lands below dst's root, never through a link there.
func migrateOldOverlay(src *os.Root, srcRel string, dst *os.Root, dstRel string) {
	if !dirHasEntriesBeneath(src, srcRel) {
		return
	}
	if filepath.Clean(dstRel) != "." {
		if err := dst.MkdirAll(dstRel, 0o755); err != nil {
			return
		}
	}
	copyTreeIfMissing(src, srcRel, dst, dstRel)
}

// dirHasEntriesBeneath reports whether rel below r is a real directory (not a link to one)
// with at least one entry.
func dirHasEntriesBeneath(r *os.Root, rel string) bool {
	fi, err := r.Lstat(rel)
	return err == nil && fi.IsDir() && len(readDirBeneath(r, rel)) > 0
}

// copyTreeIfMissing recursively copies srcRel below src to dstRel below dst, skipping any
// entry that already exists at the destination. A symbolic link is recreated as a link
// (copyLinkIfMissing), never followed; anything that is neither a directory, a regular file
// nor a link is skipped.
func copyTreeIfMissing(src *os.Root, srcRel string, dst *os.Root, dstRel string) {
	for _, e := range readDirBeneath(src, srcRel) {
		s := filepath.Join(srcRel, e.Name())
		d := filepath.Join(dstRel, e.Name())
		info, err := src.Lstat(s)
		if err != nil {
			continue
		}
		switch {
		case info.IsDir():
			if err := dst.MkdirAll(d, 0o755); err != nil {
				continue
			}
			copyTreeIfMissing(src, s, dst, d)
		case info.Mode()&fs.ModeSymlink != 0:
			_ = copyLinkIfMissing(src, s, dst, d)
		case info.Mode().IsRegular():
			_ = copyFileIfMissing(src, s, dst, d)
		}
	}
}

// rescueSharedDir copies a machine-scope shared dir's stranded per-workspace copy (rel below
// ws) into its machine-wide home, dest, the shared-tier rescue prepareWsState describes. dest
// is opened as its own root, refusing a link at it, so a link the jail leaves inside the
// shared dir cannot reach the rest of the machine store (the Claude login seed is a sibling).
func rescueSharedDir(ws *os.Root, rel, dest string) {
	if !dirHasEntriesBeneath(ws, rel) {
		return
	}
	dst, err := openDirRefusingLink(dest)
	if err != nil {
		return
	}
	defer dst.Close()
	migrateOldOverlay(ws, rel, dst, ".")
}
