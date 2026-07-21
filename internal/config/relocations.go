package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// cacheRelocationsKey is the one config key this feature reads. Kept as a
// constant so the loader, the validator and the unknown-key set can't drift.
const cacheRelocationsKey = "cache_relocations"

// CacheRelocation maps a cache subdir name to an absolute host directory.
// The container destination is always /home/agent/.cache/<Subdir> and is never
// caller-supplied, so a relocation can never mount over /etc, /opt/yolo-jail or
// the workspace.
type CacheRelocation struct {
	Subdir string // single path segment, e.g. "huggingface"
	Target string // absolute host path, ~ already expanded
}

// LoadCacheRelocations reads cache_relocations from the USER config ONLY —
// $HOME/.config/yolo-jail/config.jsonc plus its include_if_found files, which
// are host-side too.
//
// This is the security boundary of the whole feature, not a convenience. The
// cache directory is bind-mounted READ-WRITE into the jail, so anything derived
// from a jail-writable file is agent-controlled, and this key hands out
// read-write host mounts. Of the three places a config key can come from, two
// are jail-writable: the workspace yolo-jail{,.local}.jsonc (/workspace is
// bind-mounted rw) and <workspace>/.yolo/config-snapshot.json (same mount, and
// read verbatim in-jail by LoadConfig). Only the host user config is not. So
// this loader must never consult the merged config, the workspace config, or
// the snapshot — reading paths.UserConfigPath() directly makes workspace scope
// inexpressible by construction. validateCacheRelocations' workspace-scope
// error is defense-in-depth against a silent no-op, not the boundary itself.
//
// Entries are returned sorted by Subdir so the emitted -v argv is
// deterministic. Malformed entries are SKIPPED with a message through warn (nil
// warn discards) rather than failing the run: ValidateConfig makes the same
// problems loud, and preflight blocks the run before assembly is ever reached.
// The error return is reserved for a user config that cannot be read or parsed
// at all.
//
// A target that does not exist YET is kept, not dropped. Podman does fail hard
// ("statfs …: no such file or directory") on a missing bind source, but the
// guard against that is ordering, not filtering: every caller that goes on to
// mount the entry runs storage.EnsureCacheRelocations first, which creates the
// target's last path component (its parent must already exist — see
// checkCacheRelocations) and errors loudly if it cannot. Dropping the entry
// here instead would make a fresh host with the config set silently fall back
// to the un-relocated cache, and `yolo check` would still call the config fine
// — the exact silent no-op this feature exists to prevent.
//
// Relocation is a HOST-side feature and is inert inside a jail. The user config
// is visible in here — it is the host's file, bind-mounted read-only
// (userConfigMountArgs in internal/cli/run) — but its targets are host paths
// that are deliberately not in the jail's mount namespace, and a nested jail's
// GlobalCache() is its own per-workspace dir anyway. Emitting a relocation here
// would hand podman a bind source that does not exist and kill the container.
func LoadCacheRelocations(warn Warn) ([]CacheRelocation, error) {
	if inJail() {
		return nil, nil
	}
	if warn == nil {
		warn = func(string) {}
	}
	path := paths.UserConfigPath()
	// strict=true: a malformed user config is an error, never a silent empty
	// map — silently losing a relocation sends 185 GiB back onto the root
	// filesystem, which is the exact failure this feature exists to prevent.
	userCfg, err := LoadJSONCWithIncludes(path, path, true, warn, nil)
	if err != nil {
		return nil, err
	}
	v, present := userCfg.Get(cacheRelocationsKey)
	if !present || v == nil {
		return nil, nil
	}

	entries, problems := checkCacheRelocations(v, true)
	for _, p := range problems {
		warn(p + " — relocation skipped")
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries, nil
}

// checkCacheRelocations shape-validates a whole cache_relocations value. It
// returns the accepted entries (sorted by Subdir, "~" expanded, path cleaned)
// and one message per rejected entry, already prefixed with its config path.
//
// The loader and ValidateConfig both go through this, so what `yolo check`
// reports as an error is exactly what the loader drops, in exactly the same
// words. Filesystem access is limited to the target's PARENT, and only when
// checkTargetParent is set; the target itself is always allowed to be missing
// because storage.EnsureCacheRelocations creates it.
//
// checkTargetParent is false for callers validating a config that may have been
// written on a different filesystem than the one they are running on — see
// validateCacheRelocations, which must not stat host paths from inside a jail.
func checkCacheRelocations(v any, checkTargetParent bool) (entries []CacheRelocation, problems []string) {
	prefix := "config." + cacheRelocationsKey
	m, ok := asMap(v)
	if !ok {
		return nil, []string{prefix + ": expected an object mapping a cache subdir " +
			"name to an absolute host path"}
	}
	byTarget := make(map[string]string, m.Len()) // target -> first subdir claiming it
	for _, subdir := range m.Keys() {
		if msg := checkCacheRelocationSubdir(subdir); msg != "" {
			// The key is unusable as a path component, so it can't be part of a
			// dotted config path — name it with a repr instead.
			problems = append(problems, fmt.Sprintf("%s: invalid subdir %s: %s",
				prefix, pytext.Repr(subdir), msg))
			continue
		}
		path := prefix + "." + subdir
		raw, _ := m.Get(subdir)
		rawTarget, ok := asStr(raw)
		if !ok {
			problems = append(problems, path+": expected an absolute host path string")
			continue
		}
		// Clean normalizes a trailing slash away so two spellings of the same
		// directory collide in the duplicate check below.
		target := filepath.Clean(expandUser(rawTarget))
		if !filepath.IsAbs(target) {
			problems = append(problems, fmt.Sprintf(
				"%s: target must be an absolute path (got %s); '~' is expanded",
				path, pytext.Repr(rawTarget)))
			continue
		}
		if prev, dup := byTarget[target]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s: target %s is already relocated from subdir %s",
				path, pytext.Repr(target), pytext.Repr(prev)))
			continue
		}
		// A ':' would be swallowed by podman's src:dst:options parsing rather
		// than being part of the path — proven: a subdir of "hf:ro" mounts at
		// ~/.cache/hf with the READ-ONLY option (every cache write then fails),
		// and "hf:U" is accepted too, which recursively chowns the target. The
		// user config is host-owned, so this is a footgun and not an escalation,
		// but the failure is silent and lands on the biggest directory the user
		// owns. Rejected on both sides of the mount spec.
		if strings.ContainsRune(target, ':') {
			problems = append(problems, fmt.Sprintf(
				"%s: target must not contain ':' (%s) — it would be parsed as a "+
					"podman mount option, not part of the path", path, pytext.Repr(target)))
			continue
		}
		if !checkTargetParent {
			byTarget[target] = subdir
			entries = append(entries, CacheRelocation{Subdir: subdir, Target: target})
			continue
		}
		// The PARENT must exist; the final segment may be missing because
		// storage.EnsureCacheRelocations creates it. The asymmetry is
		// deliberate: MkdirAll-ing the whole path would turn a typo like
		// /data/relcoated/… into a silently-wrong empty directory on the root
		// filesystem — i.e. the 185 GiB stays exactly where the user was trying
		// to move it from, which is the failure this feature exists to prevent.
		// One missing component is a not-yet-created cache dir; two is a typo.
		parent := filepath.Dir(target)
		if st, err := os.Stat(parent); err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: parent directory of the target does not exist: %s "+
					"(only the last path component is created for you)", path, parent))
			continue
		} else if !st.IsDir() {
			problems = append(problems, fmt.Sprintf(
				"%s: parent of the target is not a directory: %s", path, parent))
			continue
		}
		byTarget[target] = subdir
		entries = append(entries, CacheRelocation{Subdir: subdir, Target: target})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Subdir < entries[j].Subdir })
	return entries, problems
}

// checkCacheRelocationSubdir returns "" for a usable subdir key, else the
// reason it is rejected. A key must name a single path segment: it becomes the
// last component of the container destination /home/agent/.cache/<subdir>, so
// anything with a separator, "." or ".." could point the mount somewhere the
// user did not write down.
func checkCacheRelocationSubdir(subdir string) string {
	const rule = "must be a single path segment (no '/' or ':', not '.' or '..')"
	if subdir == "" {
		return "must not be empty"
	}
	if strings.ContainsRune(subdir, '/') || strings.ContainsRune(subdir, os.PathSeparator) {
		return rule
	}
	// ':' is not merely an odd directory name here — it terminates the
	// destination in podman's src:dst:options spec. Proven: "hf:ro" mounts at
	// ~/.cache/hf read-only (silently breaking every cache write) and "hf:U" is
	// accepted, recursively chowning the target.
	if strings.ContainsRune(subdir, ':') {
		return rule
	}
	if subdir == "." || subdir == ".." {
		return rule
	}
	// Belt and braces: anything Base() rewrites (a trailing slash, a lone
	// separator run) is not the literal segment it looks like.
	if filepath.Base(subdir) != subdir {
		return rule
	}
	return ""
}
