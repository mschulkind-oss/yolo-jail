// Package flakebundle owns the GENERATIONS of the staged flake bundle: the
// directory a from-source `just install` writes, that every launch mounts into
// the jail as its yolo binaries.
//
// THE BUG THIS EXISTS FOR. `paths.FlakeBundleDir` used to be a plain directory
// that scripts/stage-source-bundle.sh rewrote in place, and the script leads with
// `rm -rf "$DEST"`. A launch mounts `<bundle>/bin/linux-<arch>` at
// /opt/yolo-jail/bin (internal/cli/run/jailprefix.go), and a bind mount pins an
// INODE, not a path — so `just install` deleted the binaries out from under every
// jail that happened to be running, permanently. The container kept a mount on an
// emptied directory while a perfectly good bundle reappeared at the same path,
// and every later entry into that jail died at exec:
//
//	stat /opt/yolo-jail/bin/yolo-entrypoint: no such file or directory
//
// Measured on the maintainer's machine 2026-09-09: a jail launched at +1114 was
// bricked by an install that upgraded the host `yolo` to +1160.
//
// THE FIX IS THAT AN INSTALL NEVER WRITES OVER A DIRECTORY ANYTHING IS MOUNTING.
// Each install stages a fresh GENERATION under FlakeBundleDir's sibling
// `flake-bundles/`, then swaps the stable path to point at it:
//
//	~/.local/share/yolo-jail/flake-bundle          -> flake-bundles/<stamp>  (symlink)
//	~/.local/share/yolo-jail/flake-bundles/<stamp> the generation, immutable
//
// Nothing downstream changes: reporoot.Resolve still consults the one stable
// path, and it reads through the symlink. What changes is that a running jail's
// mount source is a generation nobody will ever write to again, so it survives
// every future install — and an old jail keeps working rather than needing a
// restart it was given no warning about.
//
// GENERATIONS ARE REAPED BY LIVENESS, NOT BY AGE, for the reason OQ-BF4 states
// about prefix roots: "old and unreferenced" and "old and still mounted by a jail
// that has been up for a week" look identical from the filesystem, and only the
// runtime can tell them apart. Reap declines entirely when it cannot ask.
package flakebundle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GenerationGrace mirrors prune.PrefixRootGrace, and for the same reason: a
// generation staged moments ago belongs to an install that has not swapped the
// symlink yet, or to a launch that has not started its container yet. Neither is
// visible to a liveness probe, and both would be reaped as unreferenced.
const GenerationGrace = time.Hour

// generationsLeaf is the directory holding every staged generation, beside the
// stable symlink rather than under it — under it would make the symlink's target
// contain the symlink's siblings, and a reap walking the tree would have to know
// which of those it was standing in.
const generationsLeaf = "flake-bundles"

// stampRe is what makes a reap scoped rather than a glob. Only names this
// package's own StageDir produces are candidates, so a human's
// `flake-bundles/keep-this` — or the `-backup` lookalike that OQ-BF3's tests
// exist for — is never a deletion candidate.
//
// Two spellings, one shape: `<RFC3339-ish UTC>-<pid>` for a normal install, and
// the same prefixed by `legacy-` for the one-time migration of a pre-generations
// directory (see Activate).
var stampRe = regexp.MustCompile(`^(legacy-)?[0-9]{8}T[0-9]{6}Z-[0-9]+$`)

// GenerationsDir is where generations live, given the stable bundle path.
//
// Derived from the stable path rather than from paths.GlobalStorage directly so
// that this package needs no dependency on internal/paths and every caller —
// including a test on a temp dir — names one root and gets a consistent pair.
func GenerationsDir(stableDir string) string {
	return filepath.Join(filepath.Dir(stableDir), generationsLeaf)
}

// StageDir returns the path of a fresh, EMPTY generation directory, creating its
// parent. The caller stages a bundle into it and then calls Activate.
//
// The stamp is UTC-sortable plus the pid: sortable so a human reading the
// directory sees them in install order, and pid-suffixed because two installs in
// the same second are rare rather than impossible, and the failure mode of a
// collision is one install staging into another's half-written tree.
func StageDir(stableDir string, now time.Time, pid int) (string, error) {
	gens := GenerationsDir(stableDir)
	if err := os.MkdirAll(gens, 0o755); err != nil {
		return "", fmt.Errorf("flake bundle generations dir: %w", err)
	}
	name := fmt.Sprintf("%s-%d", now.UTC().Format("20060102T150405Z"), pid)
	return filepath.Join(gens, name), nil
}

// Activate points the stable path at `generation`, atomically, and returns what
// it had to do to the previous occupant so a caller can report it.
//
// THE ATOMIC SWAP is symlink-to-a-temp-name then rename over the stable path.
// Rename onto an existing SYMLINK replaces it in one step, so no launch ever
// observes the stable path as absent — which matters because reporoot.Resolve
// treats an absent bundle as "no flake source at all" and refuses the launch.
//
// THE LEGACY MIGRATION is the interesting half. On a machine that installed
// before generations existed, the stable path is a real DIRECTORY, and rename
// cannot put a symlink over one. Deleting it is exactly the bug this package
// exists to fix — it is the directory running jails are mounting. So it is
// RENAMED into the generations dir instead: rename moves a directory entry and
// leaves the inode alone, so every bind mount on it stays valid and every jail
// that was up keeps running. It becomes an ordinary generation from then on, and
// the reaper takes it when the last jail using it is gone.
func Activate(stableDir, generation string) (migrated string, err error) {
	if fi, lerr := os.Lstat(stableDir); lerr == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
		gens := GenerationsDir(stableDir)
		if err := os.MkdirAll(gens, 0o755); err != nil {
			return "", fmt.Errorf("flake bundle generations dir: %w", err)
		}
		migrated = filepath.Join(gens, "legacy-"+time.Now().UTC().Format("20060102T150405Z")+
			fmt.Sprintf("-%d", os.Getpid()))
		if err := os.Rename(stableDir, migrated); err != nil {
			return "", fmt.Errorf("preserving the pre-generations bundle (a running jail may "+
				"be mounting it): %w", err)
		}
	}

	// RELATIVE, so the whole state dir stays movable — an absolute target would
	// break the moment $HOME differs from where the install ran (a copied home, a
	// container that mounts the state dir elsewhere).
	target, err := filepath.Rel(filepath.Dir(stableDir), generation)
	if err != nil {
		target = generation
	}
	tmp := stableDir + ".swap"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return migrated, fmt.Errorf("staging the bundle symlink: %w", err)
	}
	if err := os.Rename(tmp, stableDir); err != nil {
		_ = os.Remove(tmp)
		return migrated, fmt.Errorf("activating the bundle generation: %w", err)
	}
	return migrated, nil
}

// Generations lists the generation directories, oldest name first. Only names
// StageDir produces are returned (stampRe), so nothing a human parked here can
// be mistaken for ours.
func Generations(stableDir string) []string {
	entries, err := os.ReadDir(GenerationsDir(stableDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && stampRe.MatchString(e.Name()) {
			out = append(out, filepath.Join(GenerationsDir(stableDir), e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// Current is the generation the stable path resolves to, or "" when it resolves
// to nothing at all (no install has run). On a machine that has not been migrated
// yet the stable path is a real directory and resolves to itself — harmless,
// because Reap only ever considers what Generations lists.
func Current(stableDir string) string {
	resolved, err := filepath.EvalSymlinks(stableDir)
	if err != nil {
		return ""
	}
	return resolved
}

// Reap removes generations that no running jail is mounting, keeping the current
// one and anything inside GenerationGrace. Returns what it removed (or would, in
// dry-run).
//
// liveKnown==false reaps NOTHING, and that tri-state is the whole safety
// argument. "No jail is using this" and "I could not ask" produce the same empty
// set from the runtime, and only one of them makes deleting a jail's pid1 safe —
// the same rule LivePrefixSources and PruneOrphanPrefixRoots already follow.
//
// liveSources are host directories running jails mount at /opt/yolo-jail/bin; a
// generation is in use when one of them is inside it. The comparison is on
// symlink-resolved paths at both ends, because the launcher resolves the prefix
// before mounting it (jailPrefixSource) and a stale relative path would otherwise
// read as "not in use".
func Reap(stableDir string, liveSources map[string]bool, liveKnown, apply bool, now time.Time) []string {
	removed := []string{}
	if !liveKnown {
		return removed
	}
	current := Current(stableDir)
	inUse := map[string]bool{}
	for src := range liveSources {
		if r, err := filepath.EvalSymlinks(src); err == nil {
			src = r
		}
		inUse[src] = true
	}
	for _, gen := range Generations(stableDir) {
		resolved := gen
		if r, err := filepath.EvalSymlinks(gen); err == nil {
			resolved = r
		}
		if resolved == current {
			continue
		}
		if st, err := os.Stat(gen); err == nil && now.Sub(st.ModTime()) < GenerationGrace {
			continue
		}
		if generationInUse(resolved, inUse) {
			continue
		}
		if apply {
			if err := os.RemoveAll(gen); err != nil {
				continue
			}
		}
		removed = append(removed, gen)
	}
	return removed
}

// generationInUse reports whether any live mount source lies inside `gen`.
// Prefix comparison with an explicit separator, never a bare strings.HasPrefix:
// `<…>/flake-bundles/2026…-1` must not match `<…>/flake-bundles/2026…-12`.
func generationInUse(gen string, inUse map[string]bool) bool {
	for src := range inUse {
		if src == gen || strings.HasPrefix(src, gen+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
