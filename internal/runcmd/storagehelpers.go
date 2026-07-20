package runcmd

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// resolveLSPInstalls ports _resolve_lsp_installs over the config's lsp_servers
// keys, returning the newline-joined (npm, go) install lists.
func resolveLSPInstalls(cfg *jsonx.OrderedMap) (npm, goPkgs string) {
	return ResolveLSPInstalls(lspServerNames(cfg))
}

// jailMiseStoreDir ports _jail_mise_store_dir: /mise inside a jail (nested),
// else GLOBAL_MISE.
func jailMiseStoreDir(inJail bool) string {
	if inJail {
		return "/mise"
	}
	return paths.GlobalMise()
}

// yoloVersion ports _git_describe_version() or "unknown" (the YOLO_VERSION env
// value baked into the container). version.Get resolves YOLO_VERSION → git
// describe → baked → "unknown".
func (o *Options) yoloVersion(repoRoot string) string {
	return version.Get(repoRoot)
}

// seedAgentDir ports _seed_agent_dir: copy auth-related files from a GLOBAL_HOME
// agent dir into a per-workspace overlay, only when the target doesn't already
// exist. Subdirectories are skipped (the entrypoint recreates them). Errors on
// individual files are swallowed.
func seedAgentDir(src, dst string) {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// os.ReadDir returns DirEntry; a symlink to a file is not a regular
		// file — Python's item.is_file() follows symlinks, so Stat it.
		srcItem := filepath.Join(src, e.Name())
		si, err := os.Stat(srcItem)
		if err != nil || !si.Mode().IsRegular() {
			continue
		}
		target := filepath.Join(dst, e.Name())
		if fileExists(target) {
			continue
		}
		_ = copyFile2(srcItem, target)
	}
}

// syncClaudeJSONSeed delegates to the ported storage.SyncClaudeJSONSeed.
func syncClaudeJSONSeed(seed, ws string) {
	storage.SyncClaudeJSONSeed(seed, ws)
}

// migrateOldOverlay ports _migrate_old_overlay: copy files from a pre-refactor
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
// exists at the destination (shutil.copytree with dirs_exist_ok + a copy
// function that skips existing files).
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
