package run

import (
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

// syncClaudeJSONSeed delegates to storage.SyncClaudeJSONSeed.
func syncClaudeJSONSeed(seed, ws string) {
	storage.SyncClaudeJSONSeed(seed, ws)
}

// migrateOldOverlay copies files from a pre-refactor
// overlay dir into the new location, never overwriting an existing target. No-op
// when the old dir is missing or empty.
func migrateOldOverlay(oldDir, newDir string) {
	info, err := os.Stat(oldDir)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil || len(entries) == 0 {
		return
	}
	_ = os.MkdirAll(newDir, 0o755)
	copyTreeIfMissing(oldDir, newDir)
}

// copyTreeIfMissing recursively copies src→dst, skipping any file that already
// exists at the destination.
func copyTreeIfMissing(src, dst string) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := os.Stat(s)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = os.MkdirAll(d, 0o755)
			copyTreeIfMissing(s, d)
			continue
		}
		if fileExists(d) {
			continue
		}
		_ = copyFile2(s, d)
	}
}
