package config

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// writableHomeDirsKey is the one config key this feature reads. Kept as a
// constant so the deriver, the validator and the unknown-key set can't drift.
const writableHomeDirsKey = "writable_home_dirs"

// WritableHomeBackingSubdir is the wsState subdir every writable-home backing
// dir lives under (<workspace>/.yolo/home/writable-home/<path>). Namespaced so
// it can never collide with the flat overlay dirs (npm-global, local, …) that
// share the wsState root.
const WritableHomeBackingSubdir = "writable-home"

// WritableHomeDirs returns the validated, deduplicated, sorted home-relative
// paths declared under writable_home_dirs in the MERGED config.
//
// Unlike [LoadCacheRelocations], this key is safe at ANY config scope, so it is
// read from the merged config exactly like `packages` — no user-config-only
// boundary. The reason is the whole security difference between the two knobs:
// cache_relocations mounts an arbitrary HOST path read-write into the jail (an
// escalation primitive if a jail-writable config could set it), whereas every
// writable_home_dirs entry is confined to a destination under /home/agent/ and
// backed by a directory under the workspace's own .yolo/home. A jail that could
// edit its workspace config to add an entry gains nothing it could not already
// do by writing to /workspace — the backing dir IS inside the workspace.
//
// Invalid entries are dropped here (ValidateConfig reports the same problems as
// errors, and preflight blocks the run before assembly), so a bad entry never
// silently mounts something unexpected; it is simply absent.
func WritableHomeDirs(cfg *jsonx.OrderedMap) []string {
	v, present := cfg.Get(writableHomeDirsKey)
	if !present || v == nil {
		return nil
	}
	entries, _ := checkWritableHomeDirs(v)
	return entries
}

// reservedHomeSegments is the set of first path segments yolo already manages
// under /home/agent — the base overlays and shared dirs, the single-file
// mounts, the :ro-base symlinks, and every selected-agent overlay dir. A
// writable_home_dirs entry whose first segment is one of these is rejected:
// either it would clobber a yolo mount (dir-over-file or file-over-dir), or the
// subtree is ALREADY writable (the overlay dirs are read-write binds), so the
// key is redundant there. The authority for the static half is
// podmanBaseMounts + storage.fileMountpoints in internal/cli/run + storage; the
// dynamic half is agents.AllOverlayDirs. Kept as a function (not a package var)
// so it always reflects the current agent set.
func reservedHomeSegments() map[string]struct{} {
	segs := map[string]struct{}{
		// Base overlays + shared dirs (podmanBaseMounts, assemble.go).
		".npm-global": {}, ".local": {}, "go": {}, ".yolo-shims": {},
		".config": {}, ".cache": {}, ".ssh": {}, ".claude-shared-credentials": {},
		// Single-file mounts (podmanBaseMounts + storage.fileMountpoints).
		".bash_history": {}, ".yolo-bootstrap.sh": {}, ".yolo-venv-precreate.sh": {},
		".yolo-perf.log": {}, ".yolo-socat.log": {}, ".yolo-entrypoint.lock": {},
		".yolo-ca-bundle.crt": {}, ".yolo-installed-lsps": {},
		// Symlinks materialized into the :ro GLOBAL_HOME base (storage.ensure).
		".gitconfig": {}, ".bashrc": {}, ".claude.json": {},
	}
	for _, d := range agents.AllOverlayDirs {
		segs[firstHomeSegment(d)] = struct{}{}
	}
	return segs
}

// firstHomeSegment returns the first slash-separated component of a
// container-home-relative path (always slash-separated, never OS-specific).
func firstHomeSegment(p string) string {
	return strings.SplitN(p, "/", 2)[0]
}

// checkWritableHomeDirs shape-validates a whole writable_home_dirs value. It
// returns the accepted paths (cleaned, deduplicated, sorted) and one message
// per rejected entry, already prefixed with its config path index.
//
// The deriver ([WritableHomeDirs]) and ValidateConfig both go through this, so
// what `yolo check` reports as an error is exactly what the deriver drops, in
// exactly the same words. There is no filesystem access: every destination is
// synthetic (/home/agent/<path>) and every backing dir is created by
// prepareWsState, so nothing here needs to stat the host.
func checkWritableHomeDirs(v any) (entries []string, problems []string) {
	prefix := "config." + writableHomeDirsKey
	list, ok := asList(v)
	if !ok {
		return nil, []string{prefix + ": expected a list of home-relative path strings"}
	}
	reserved := reservedHomeSegments()
	seen := make(map[string]struct{}, len(list))
	for idx, raw := range list {
		itemPath := fmt.Sprintf("%s[%d]", prefix, idx)
		s, ok := asStr(raw)
		if !ok {
			problems = append(problems, itemPath+": expected a home-relative path string")
			continue
		}
		if msg := checkWritableHomeDir(s, reserved); msg != "" {
			problems = append(problems, fmt.Sprintf("%s: %s: %s", itemPath, pytext.Repr(s), msg))
			continue
		}
		clean := path.Clean(s)
		if _, dup := seen[clean]; dup {
			continue // silently coalesce a duplicate; not an error
		}
		seen[clean] = struct{}{}
		entries = append(entries, clean)
	}
	sort.Strings(entries)
	return entries, problems
}

// checkWritableHomeDir returns "" for a usable entry, else the reason it is
// rejected. An entry must be a clean path RELATIVE to /home/agent that does not
// escape it, does not contain a ':' (which podman would parse as a mount
// option, not part of the path — the same footgun cache_relocations guards),
// and whose first segment is not one yolo already manages.
func checkWritableHomeDir(s string, reserved map[string]struct{}) string {
	if s == "" {
		return "must not be empty"
	}
	if strings.HasPrefix(s, "/") {
		return "must be a path relative to $HOME, not an absolute path"
	}
	if strings.ContainsRune(s, ':') {
		return "must not contain ':' — it would be parsed as a podman mount option, not part of the path"
	}
	clean := path.Clean(s)
	// path.Clean collapses "foo/../bar" to "bar" and "./foo" to "foo"; the only
	// ways a cleaned relative path can still escape or self-reference are a "."
	// (the home root itself) or a leading "..".
	if clean == "." {
		return "must name a path under $HOME, not '.' (the home directory itself)"
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "must not escape $HOME with '..'"
	}
	seg := firstHomeSegment(clean)
	if _, bad := reserved[seg]; bad {
		return fmt.Sprintf("first path segment %s is already managed read-write by yolo — "+
			"paths under it are writable without this key", pytext.Repr(seg))
	}
	return ""
}

// validateWritableHomeDirs surfaces every checkWritableHomeDirs problem as a
// `yolo check` error. Safe at any scope, so — unlike validateCacheRelocations —
// there is no workspace-scope rejection.
func validateWritableHomeDirs(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get(writableHomeDirsKey)
	if !present || v == nil {
		return
	}
	_, problems := checkWritableHomeDirs(v)
	for _, p := range problems {
		add(errs, p)
	}
}
