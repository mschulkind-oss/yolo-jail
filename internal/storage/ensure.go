package storage

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// StorageLayoutVersion is the current storage layout version. v2 = split mise
// store: jails share GLOBAL_MISE at /mise and no longer mount the host's
// ~/.local/share/mise.
const StorageLayoutVersion = 2

// fileMountpoints are the paths under GLOBAL_HOME that must exist as files (not
// dirs) for single-file bind mounts.
var fileMountpoints = []string{
	".bash_history",
	".yolo-bootstrap.sh",
	".yolo-venv-precreate.sh",
	".yolo-perf.log",
	".yolo-socat.log",
	".yolo-entrypoint.lock",
	".yolo-ca-bundle.crt",
	".yolo-installed-lsps",
}

// EnsureGlobalStorage makes sure ~/.local/share/yolo-jail/* and the GLOBAL_HOME
// mountpoints exist and are the right shape (files vs dirs, symlinks for atomic-
// write paths).
// (pass MigrateStorageLayout wired with a liveness probe, or a no-op).
func EnsureGlobalStorage(migrate func()) error {
	globalHome := paths.GlobalHome()
	for _, d := range []string{
		paths.GlobalStorage(), globalHome, paths.GlobalMise(), paths.GlobalCache(),
		paths.ContainerDir(), paths.AgentsDir(), paths.BuildDir(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// Per-agent overlay dirs (UNION across all known agents) + shared dirs.
	overlaySubdirs := append([]string{}, agents.AllOverlayDirs...)
	overlaySubdirs = append(overlaySubdirs,
		".claude-shared-credentials",
		filepath.Join(".config", "git"),
		".npm-global",
		".local",
		"go",
		".yolo-shims",
		".config",
		".cache",
		".ssh",
	)
	for _, sub := range overlaySubdirs {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			return err
		}
	}

	// Migrate credentials from old single-file location to new shared dir.
	oldCred := filepath.Join(globalHome, ".claude", ".credentials.json")
	newCred := filepath.Join(globalHome, ".claude-shared-credentials", ".credentials.json")
	if isRegularFile(oldCred) && !isSymlink(oldCred) {
		if info, err := os.Stat(newCred); os.IsNotExist(err) || (err == nil && info.Size() == 0) {
			_ = copyFile(oldCred, newCred)
		}
		_ = os.Remove(oldCred) // may have restrictive perms — leave for now
	}
	if !pathExists(newCred) {
		_ = touch(newCred)
	}

	// File mountpoints — create only if missing (existing files may have
	// restrictive perms from prior container UID mapping).
	for _, fname := range fileMountpoints {
		p := filepath.Join(globalHome, fname)
		if !pathExists(p) {
			if err := touch(p); err != nil {
				return err
			}
		}
	}

	// Atomic-write files that must be symlinks into writable overlay dirs.
	if err := EnsureSymlink(filepath.Join(globalHome, ".claude.json"), filepath.Join(".claude", "claude.json")); err != nil {
		return err
	}
	if err := EnsureSymlink(filepath.Join(globalHome, ".gitconfig"), filepath.Join(".config", "git", "config")); err != nil {
		return err
	}
	if err := EnsureSymlink(filepath.Join(globalHome, ".bashrc"), filepath.Join(".config", "bashrc")); err != nil {
		return err
	}

	if migrate != nil {
		migrate()
	}
	return nil
}

// EnsureSymlink ensures link is a relative symlink to target (a path relative to
// link's parent), migrating a pre-existing regular file's data into the target
// location first.
func EnsureSymlink(link, target string) error {
	if isSymlink(link) {
		cur, err := os.Readlink(link)
		if err == nil && cur != target {
			if err := os.Remove(link); err != nil {
				return err
			}
			return os.Symlink(target, link)
		}
		return nil
	}
	if pathExists(link) {
		// Migrate data from old regular file to new target location.
		real := filepath.Join(filepath.Dir(link), target)
		if !pathExists(real) {
			if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
				return err
			}
			_ = copyFile(link, real) // unreadable (bad perms) — skip data
		}
		// Replace file with symlink; can't replace ⇒ leave as-is (no error).
		if err := os.Remove(link); err != nil {
			return nil //nolint:nilerr // Python: except OSError: pass
		}
		_ = os.Symlink(target, link)
		return nil
	}
	return os.Symlink(target, link)
}

// HostMiseDir returns the host's own mise data dir (~/.local/share/mise). Host-
// only: consulted for migration/doctor accounting, never as a mount source or
// target. May not exist.
func HostMiseDir() string {
	return filepath.Join(homeDir(), ".local", "share", "mise")
}

// homeDir resolves the user's home the way Python's Path.home() does: $HOME if
// set (empty ⇒ "/"), else the passwd database, else "/". Local to storage so it
// need not widen the paths package's exported surface.
func homeDir() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		if h == "" {
			return "/"
		}
		return h
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return "/"
}

// JailMiseStoreDir returns the source dir for the jail-land mise store mounted
// at /mise. When the CLI runs inside a jail (YOLO_VERSION set), that store is
// already at /mise; return "/mise" so every nesting depth shares one store.
// Otherwise GLOBAL_MISE.
func JailMiseStoreDir() string {
	if _, ok := os.LookupEnv("YOLO_VERSION"); ok {
		return "/mise"
	}
	return paths.GlobalMise()
}

// FindDanglingMiseSymlinks scans <miseDir>/installs/<tool>/<entry> for symlinks
// whose targets don't resolve (the v2 heal set), in sorted order.
// dangling-collection loop of _migrate_storage_layout. A dangling link is
// is_symlink() AND not exists() (exists follows the link).
func FindDanglingMiseSymlinks(miseDir string) []string {
	installs := filepath.Join(miseDir, "installs")
	info, err := os.Stat(installs)
	if err != nil || !info.IsDir() {
		return nil
	}
	var dangling []string
	toolDirs, _ := os.ReadDir(installs)
	sortDirEntries(toolDirs)
	for _, td := range toolDirs {
		toolPath := filepath.Join(installs, td.Name())
		ti, err := os.Stat(toolPath) // follows symlink; require a real dir
		if err != nil || !ti.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(toolPath)
		sortDirEntries(entries)
		for _, e := range entries {
			entryPath := filepath.Join(toolPath, e.Name())
			if isSymlink(entryPath) && !pathExists(entryPath) {
				dangling = append(dangling, entryPath)
			}
		}
	}
	return dangling
}

// MigrateStorageLayout performs the one-time, versioned, marker-stamped layout
// migration.
// safe to unlink dangling links (Python: rt is not None AND live == empty set) —
// inject a probe that returns false on unknown/live siblings (fail-safe). insideJail
// short-circuits (never runs inside a jail). Writes messages via warnf (stderr).
func MigrateStorageLayout(insideJail bool, canReclaim func() bool, warnf func(string)) {
	if insideJail {
		return
	}
	marker := filepath.Join(paths.GlobalStorage(), "layout-version")
	if data, err := os.ReadFile(marker); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && v >= StorageLayoutVersion {
			return
		}
	}
	dangling := FindDanglingMiseSymlinks(HostMiseDir())
	if len(dangling) > 0 {
		if canReclaim == nil || !canReclaim() {
			return // live/unknown siblings — defer, retry next invocation
		}
		for _, entry := range dangling {
			if err := os.Remove(entry); err == nil && warnf != nil {
				warnf(fmt.Sprintf("Removed dangling mise store symlink: %s", entry))
			}
		}
	}
	_ = os.WriteFile(marker, []byte(fmt.Sprintf("%d\n", StorageLayoutVersion)), 0o644)
}

// ---- small fs helpers (Python pathlib semantics) ----
func pathExists(p string) bool {
	_, err := os.Stat(p) // follows symlinks (Path.exists)
	return err == nil
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func touch(p string) error {
	f, err := os.OpenFile(p, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sortDirEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
}
