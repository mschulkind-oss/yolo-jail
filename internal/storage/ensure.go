package storage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// StorageLayoutVersion is the current storage layout version. v2 = split mise
// store: jails share GLOBAL_MISE at /mise and no longer mount the host's
// ~/.local/share/mise.
const StorageLayoutVersion = 2

// EnsureGlobalStorage makes sure ~/.local/share/yolo-jail/* exists, and that the MACHINE
// STORE, <state>/home (paths.GlobalHome), holds what only it can hold: the machine-scope
// shared dirs every shipped pack declares (packload.EmbeddedSharedDirs — the rw bind SOURCES
// of `.claude-shared-credentials` and its kin, on both container backends) and the Claude
// credential migration below. Then it runs migrate (pass MigrateStorageLayout wired with a
// liveness probe, or a no-op).
//
// It no longer provisions a HOME. <state>/home used to be bound :ro at /home/agent in every
// podman jail, so this also created the union of every shipped pack's writable dirs, core's
// own dirs, the single-file mountpoints and the three redirect links in it — the whole
// shared base. Each podman jail now gets its own skeleton, built on the fresh-launch path
// from that launch's selection (buildHomeSkeleton in internal/cli/run;
// docs/design/base-home-legacy-state.md#26-which-writers-move), and <state>/home is mounted
// at /home/agent by nothing. The dirs an older yolo created here are left as they are: a
// downgraded yolo still finds its base, and legacy bytes stay unmounted and unread.
//
// The shared dirs stay the EMBEDDED set rather than the selected one because this runs
// before the config is loaded (both callers: the run pipeline's ensureStorage and `yolo
// check`), and an empty directory in a store no jail mounts whole affects nothing.
func EnsureGlobalStorage(migrate func()) error {
	globalHome := paths.GlobalHome()
	for _, d := range []string{
		paths.GlobalStorage(), globalHome, paths.GlobalMise(), paths.GlobalCache(),
		paths.ContainerDir(), paths.AgentsDir(), paths.BuildDir(), paths.CapturesDir(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// The machine-scope shared dirs: bind sources, so they must exist before any argv
	// names them.
	for _, sub := range packload.EmbeddedSharedDirs() {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			return err
		}
	}

	ensureSharedCredentials(globalHome)

	if migrate != nil {
		migrate()
	}
	return nil
}

// EnsureCacheRelocations provisions both ends of every cache-relocation bind
// mount: the host target directory and the mountpoint at GlobalCache()/<subdir>
// that the target is mounted over. Separate from EnsureGlobalStorage, which
// stays config-free.
//
// Both ends are created because podman gets each one wrong on its own. It WILL
// create the missing mountpoint itself — but on the host side, inside the parent
// bind source, root-owned and mode drwxr-xr-t; we want the same ownership and
// perms as every other dir we hand the jail. And a missing TARGET is not a skip:
// podman fails the whole container with a bare
// "Error: statfs <path>: no such file or directory" and nothing ever starts.
//
// Only the target's LAST path component is created. Its parent must already
// exist (config.LoadCacheRelocations validates that, and this re-checks so the
// rule holds for any caller): MkdirAll-ing the whole path would turn a typo like
// /data/relcoated/... into a silently-wrong empty directory back on the root
// filesystem — the exact failure relocation exists to prevent.
func EnsureCacheRelocations(relocations []config.CacheRelocation) error {
	for _, rel := range relocations {
		parent := filepath.Dir(rel.Target)
		if st, err := os.Stat(parent); err != nil || !st.IsDir() {
			return fmt.Errorf("cache_relocations.%s: parent directory of the target does not exist: %s "+
				"(only the last path component is created for you)", rel.Subdir, parent)
		}
		if err := os.MkdirAll(rel.Target, 0o755); err != nil {
			return fmt.Errorf("cache_relocations.%s: creating target %s: %w", rel.Subdir, rel.Target, err)
		}
		mountpoint := filepath.Join(paths.GlobalCache(), rel.Subdir)
		if err := os.MkdirAll(mountpoint, 0o755); err != nil {
			return fmt.Errorf("cache_relocations.%s: creating mountpoint %s: %w", rel.Subdir, mountpoint, err)
		}
	}
	return nil
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

// sharedCredentialsDir and sharedCredentialsFile name the Claude credential file in the machine
// store's shared dir, the rw bind source of every claude jail's ~/.claude-shared-credentials.
const (
	sharedCredentialsDir  = ".claude-shared-credentials"
	sharedCredentialsFile = ".credentials.json"
)

// ensureSharedCredentials makes <globalHome>/.claude-shared-credentials/.credentials.json a
// regular file, first migrating the legacy <globalHome>/.claude/.credentials.json into it when
// the shared one is missing or empty. Best-effort, as it always was: a failure leaves the file
// missing, and the jail's Claude logs in again.
//
// THE SHARED DIR IS JAIL-WRITABLE (every claude jail binds it read-write), so this runs beneath
// an os.Root on it (paths.OpenStateDirRoot, refusing the dir itself as a link) and never follows
// a link at the file's name. Before, a link-following stat and an O_CREATE touch created the
// target of a dangling link the jail had left there, as the host user, on the next launch or
// `yolo check`, and the migration copied the legacy credential into a host file of the jail's
// choosing (docs/reference/jail-home.md, "Host code in jail-writable state"). A non-regular file
// at the name is replaced: the jail sees it only through the bind, so removing it loses
// nothing but the link.
func ensureSharedCredentials(globalHome string) {
	r, err := paths.OpenStateDirRoot(filepath.Join(globalHome, sharedCredentialsDir))
	if err != nil {
		return
	}
	defer r.Close()

	fi, err := r.Lstat(sharedCredentialsFile)
	if err == nil && !fi.Mode().IsRegular() {
		if r.Remove(sharedCredentialsFile) != nil {
			return
		}
		err = fs.ErrNotExist
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	missingOrEmpty := err != nil || fi.Size() == 0

	// Migrate credentials from the old single-file location into the shared dir.
	oldCred := filepath.Join(globalHome, ".claude", sharedCredentialsFile)
	if old, oldInfo := openRegularNoFollow(oldCred); old != nil {
		if missingOrEmpty {
			if err == nil { // an empty regular file is there: replace it, O_EXCL below
				_ = r.Remove(sharedCredentialsFile)
			}
			_ = copyIntoNew(r, sharedCredentialsFile, old, oldInfo.Mode().Perm())
		}
		old.Close()
		_ = os.Remove(oldCred) // may have restrictive perms — leave for now
	}
	if _, err := r.Lstat(sharedCredentialsFile); errors.Is(err, fs.ErrNotExist) {
		if f, err := r.OpenFile(sharedCredentialsFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			_ = f.Close()
		}
	}
}

// openRegularNoFollow opens p for reading only when p itself is a regular file, not a link to
// one, and returns nil when it is anything else or missing.
func openRegularNoFollow(p string) (*os.File, os.FileInfo) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil
	}
	return f, info
}

// copyIntoNew creates name beneath r, which must not exist, and copies in into it; a partial
// copy is removed.
func copyIntoNew(r *os.Root, name string, in io.Reader, perm os.FileMode) error {
	out, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = r.Remove(name)
		return err
	}
	return out.Close()
}

func sortDirEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
}
