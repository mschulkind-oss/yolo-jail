package runcmd

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// resolveRepoRoot ports _resolve_repo_root: locate the yolo-jail repo root for
// nix image builds. Returns (path, ok); ok=false is the Python
// SystemExit(1) branch (with the same actionable message printed to stderr).
//
// Resolution order:
//  1. YOLO_REPO_ROOT env var (set inside jails and CI), validated to actually
//     contain source (nested jails' /opt/yolo-jail bind may be empty).
//  2. Source-checkout detection (walk up from cwd for flake.nix — the Go binary
//     has no __file__; during dev/CI the cwd is the workspace or repo).
//  3. Installed-package staging (a bundled flake.nix+src tree staged into
//     GLOBAL_STORAGE/nix-build-root with the FROZEN rename-aside invariant).
//  4. User config repo_path field.
//  5. Error with the helpful message.
func resolveRepoRoot(getenv func(string) string, stderr io.Writer, color bool) (string, bool) {
	// 1. Env var, validated for source.
	if env := getenv("YOLO_REPO_ROOT"); env != "" {
		if fileExists(filepath.Join(env, "flake.nix")) ||
			fileExists(filepath.Join(env, "src", "entrypoint", "__init__.py")) {
			return absOr(env), true
		}
	}

	// 2. Source checkout: walk up from cwd looking for flake.nix.
	if dir, err := os.Getwd(); err == nil {
		for {
			if fileExists(filepath.Join(dir, "flake.nix")) {
				return absOr(dir), true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// 3. Installed-package staging (bundled flake.nix+src). Deferred by the
	// check slice; owned here. Only fires when a bundled source dir is present
	// (a Go distribution ships share/yolo-jail/ next to the binary); a
	// source-checkout / jail launch never reaches here.
	if pkgDir, ok := bundledSourceDir(); ok {
		if staged, ok := stageInstalledWheel(pkgDir); ok {
			return staged, true
		}
	}

	// 4. User config repo_path.
	userPath := paths.UserConfigPath()
	if fileExists(userPath) {
		if cfg, err := config.LoadJSONCFile(userPath, userPath, false, func(string) {}); err == nil {
			if v, _ := cfg.Get("repo_path"); v != nil {
				if repoPath, ok := v.(string); ok && repoPath != "" {
					p := absOr(expandUser(repoPath))
					if fileExists(filepath.Join(p, "flake.nix")) {
						return p, true
					}
				}
			}
		}
	}

	// 5. Error.
	if stderr != nil {
		pr := printer{w: stderr}
		pr.print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
			"The yolo CLI needs the repo for nix image builds.\n\n" +
			"Fix: add [bold]repo_path[/bold] to ~/.config/yolo-jail/config.jsonc:\n" +
			`  { "repo_path": "~/code/yolo-jail" }`)
	}
	return "", false
}

// bundledSourceDir discovers a bundled flake.nix+src tree shipped alongside the
// Go binary (Go's analog of the wheel's package data). Candidates, relative to
// the executable: ../share/yolo-jail, then the executable's own dir. Returns
// (dir, true) only when dir/flake.nix exists. In a source checkout or jail this
// returns ("", false), so step 3 is a no-op there.
func bundledSourceDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	exeDir := filepath.Dir(exe)
	for _, cand := range []string{
		filepath.Join(exeDir, "..", "share", "yolo-jail"),
		exeDir,
	} {
		if fileExists(filepath.Join(cand, "flake.nix")) {
			if c, err := filepath.Abs(cand); err == nil {
				return c, true
			}
			return cand, true
		}
	}
	return "", false
}

// stageInstalledWheel ports step 3 of _resolve_repo_root: stage the bundled
// source tree (pkgDir, containing flake.nix + flake.lock + the package) into
// GLOBAL_STORAGE/nix-build-root so nix sees the expected layout and podman can
// bind it :ro into the jail.
//
// FROZEN INVARIANT (do not change): NEVER rmtree the old build_root — a jail
// launched from the previous copy may still hold that inode bind-mounted at
// /opt/yolo-jail; deleting it out from under the live mount serves a //deleted
// inode. Instead rename the old tree ASIDE to a UNIQUE nix-build-root.old.<hex>
// name (unique so concurrent repopulates never collide on one fixed .old, which
// would ENOTEMPTY), leave it on disk, and let the liveness-gated prune sweeper
// reclaim it. Also: do NOT pre-create build_root (an empty placeholder would be
// renamed aside on the first populate, leaking an empty generation).
func stageInstalledWheel(pkgDir string) (string, bool) {
	globalStorage := paths.GlobalStorage()
	buildRoot := filepath.Join(globalStorage, "nix-build-root")

	// Ensure the PARENT exists for the temp dir + rename swap, but do NOT
	// pre-create build_root itself.
	if err := os.MkdirAll(globalStorage, 0o755); err != nil {
		return "", false
	}

	// Idempotence: skip the whole dance when the existing build_root already
	// matches the bundled flake.nix mtime and has cli/__init__.py in place.
	srcCli := filepath.Join(buildRoot, "src", "cli", "__init__.py")
	brFlake := filepath.Join(buildRoot, "flake.nix")
	if isFile(srcCli) && isFile(brFlake) {
		if pkgMtime, ok := mtimeNs(filepath.Join(pkgDir, "flake.nix")); ok {
			if brMtime, ok := mtimeNs(brFlake); ok && brMtime >= pkgMtime {
				return absOr(buildRoot), true
			}
		}
	}

	// Repopulate: build the new tree in a temp dir, then swap it in with two
	// inode-preserving renames.
	tmpRoot, err := os.MkdirTemp(globalStorage, "nix-build-tmp-")
	if err != nil {
		return "", false
	}
	ok := func() bool {
		for _, fname := range []string{"flake.nix", "flake.lock"} {
			if err := copyFile2(filepath.Join(pkgDir, fname), filepath.Join(tmpRoot, fname)); err != nil {
				return false
			}
		}
		if err := copyTree(pkgDir, filepath.Join(tmpRoot, "src")); err != nil {
			return false
		}
		aside := buildRoot + ".old." + randHex()
		renamedAside := true
		if err := os.Rename(buildRoot, aside); err != nil {
			if os.IsNotExist(err) {
				renamedAside = false // nothing to move aside (first populate)
			} else {
				return false
			}
		}
		if err := os.Rename(tmpRoot, buildRoot); err != nil {
			return false
		}
		// Stamp the aside dir's mtime to now: rename doesn't touch a dir's
		// mtime, but the prune sweeper uses mtime as the age grace floor.
		// Best-effort.
		if renamedAside {
			now := time.Now()
			_ = os.Chtimes(aside, now, now)
		}
		return true
	}()
	if !ok {
		_ = os.RemoveAll(tmpRoot)
		return "", false
	}
	return absOr(buildRoot), true
}

// --- small filesystem helpers (Python Path methods) ---

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func absOr(p string) string {
	if r, err := filepath.Abs(p); err == nil {
		return r
	}
	return p
}

func mtimeNs(p string) (int64, bool) {
	info, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	return info.ModTime().UnixNano(), true
}

// expandUser mirrors Path(p).expanduser(): expand a leading "~"/"~/…" against
// $HOME (or the passwd home). A "~user" form is left untouched.
func expandUser(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i != 1 {
		return p // ~user form
	}
	home := homeDir()
	if home == "" {
		return p
	}
	return home + p[i:]
}

func homeDir() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		if h == "" {
			return "/"
		}
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/"
}

// configRuntime returns config["runtime"] as a string, or "".
func configRuntime(cfg *jsonx.OrderedMap) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg.Get("runtime")
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func inStrSlice(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var _ = time.Now
