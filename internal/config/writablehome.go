package config

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"path"
	"sort"
	"strings"

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
//
// packs are the launch's SELECTED packs (the staged set), whose directories an entry may
// not claim (see reservedHomeDirs). Validation resolves the same selection itself
// (resolveSelectedPacks), so what `yolo check` refuses is what this drops.
func WritableHomeDirs(cfg *jsonx.OrderedMap, packs []*packload.Pack) []string {
	if cfg == nil {
		return nil
	}
	v, present := cfg.Get(writableHomeDirsKey)
	if !present || v == nil {
		return nil
	}
	entries, _ := checkWritableHomeDirs(v, packs)
	return entries
}

// reservedHomeDirRoots are the home-relative DIRECTORY roots yolo mounts
// read-write into the jail whatever the selection: the base overlays. The
// selected packs' writable and shared dirs join them (see reservedHomeDirs).
// Authority: podmanBaseMounts in internal/cli/run.
var reservedHomeDirRoots = []string{
	".npm-global", ".local", "go", ".yolo",
	// The retired names of the two generated-script dirs. Kept reserved for one
	// release while RemoveStaleGeneratedClients sweeps them, so a user config cannot
	// claim a path yolo is still cleaning up.
	".yolo-shims", ".yolo-launchers",
	".config", ".cache", ".ssh",
}

// reservedHomeFiles are the home-relative paths yolo owns as SINGLE FILES: the
// per-file bind mounts, plus the redirect links every podman jail's :ro home
// skeleton carries. Writing any of them from config would clobber a bind mount or
// replace a symlink yolo depends on. Authority: podmanBaseMounts +
// paths.HomeFileMountpoints + paths.HomeFileRedirects (buildHomeSkeleton, in
// internal/cli/run, creates both in the skeleton).
var reservedHomeFiles = []string{
	".bash_history", ".yolo-bootstrap.sh", ".yolo-venv-precreate.sh",
	".yolo-perf.log", ".yolo-socat.log", ".yolo-entrypoint.lock",
	".yolo-ca-bundle.crt",
	".gitconfig", ".bashrc", ".claude.json",
	// A2: the TARGETS of the three redirect links in the :ro home skeleton
	// (paths.HomeFileRedirects). A symlink and its target
	// are the SAME FILE, so reserving only the link name left the other name
	// claimable — a config could take ~/.config/git/config and silently clobber
	// what ~/.gitconfig resolves to, while the reservation looked complete.
	//   .claude.json -> .claude/claude.json
	//   .gitconfig   -> .config/git/config
	//   .bashrc      -> .config/bashrc
	".claude/claude.json", ".config/git/config", ".config/bashrc",
}

// reservedHomeSubtrees are home-relative DIRECTORIES yolo owns the inside of, so a
// destination anywhere beneath one is refused. Separate from reservedHomeFiles because
// the members cannot be enumerated as paths: what they hold is generated.
//
// V1. There is exactly one, and it is the other half of the A2 alias problem. A home-root
// destination (`~/.npmrc`) cannot be written into the `:ro` home skeleton, so the CLI stages a
// symlink there pointing at HostFileEntry.SymlinkTarget() — `.config/yolo-home/<slug>` in
// the writable `.config` overlay. That makes the alias and the target two spellings of ONE
// file, exactly as `~/.gitconfig` and `~/.config/git/config` are; but where A2 could list
// its three targets literally, these are keyed by a slug derived from whatever the user
// declared, so no literal list can cover them.
//
// A SUBTREE rather than the computed targets, and that is the point rather than
// convenience: reserving only the slugs a config happens to name today would still let a
// user plant a file among yolo's staged ones — and the whole subtree is yolo
// infrastructure the user never asked for, so there is no legitimate destination inside
// it. SymlinkTarget's doc said the slug keying meant "two entries can never collide",
// which was true of the case it was reasoning about (a real `~/.config` entry a tool owns)
// and false of the one that matters here (a second host_files entry).
//
// Directory-root reservation is deliberately NOT extended past this: the overlay dirs
// (`.config`, `.cache`, …) stay claimable because composing a NEW file inside one is
// host_files' central use case (see hostFileReservedDests). This subtree is different
// because yolo, not the user, decides what lives in it.
var reservedHomeSubtrees = []string{
	hostFileStagingRoot,
}

// reservedHomeDirs maps each reserved home-relative directory to the pack that
// declares it: reservedHomeDirRoots (core's, owner "") plus the writable and shared
// dirs of the SELECTED packs.
//
// THE SELECTED PACKS, NOT EVERY PACK YOLO SHIPS — the maintainer's OQ-BH14 ruling
// (docs/design/base-home-legacy-state.md#28-reservation-is-a-rule-about-config-names-not-about-directories):
// an unselected pack is treated as if it does not exist, so `writable_home_dirs:
// [".codex"]` is legal in a workspace that does not select codex and refused once it
// does. The shipped set never was the right universe anyway: packs can come from
// anywhere, and a configured pack's directory was never in it. Both consequences were
// accepted with the ruling: one user-scope entry can be valid in one workspace and
// refused in another, and a pack selected later can refuse an entry that passed before
// — which is why the refusal names the pack.
//
// Reserving a name is not creating a directory: nothing here touches a filesystem. After
// this ruling the two sets agree, though — the home skeleton a podman jail gets carries
// exactly the selected packs' dirs (buildHomeSkeleton, internal/cli/run).
func reservedHomeDirs(packs []*packload.Pack) map[string]string {
	dirs := make(map[string]string, len(reservedHomeDirRoots))
	for _, d := range reservedHomeDirRoots {
		dirs[d] = ""
	}
	// Selection order, so the first selected pack to declare a dir is the one a refusal
	// names.
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, d := range append(p.Decl.WritableDirContributions(), p.Decl.SharedDirContributions()...) {
			if _, have := dirs[d]; !have {
				dirs[d] = p.Name
			}
		}
	}
	return dirs
}

// reservedHomeSegments is the set of first path segments yolo already manages
// under /home/agent — the union of reservedHomeDirs(packs) and reservedHomeFiles,
// reduced to first segments, each mapped to the selected pack that declares it ("" for
// core's own). A writable_home_dirs entry whose first segment is one of these is
// rejected: either it would clobber a yolo mount (dir-over-file or file-over-dir), or
// the subtree is ALREADY writable (the overlay dirs are read-write binds), so the key is
// redundant there.
//
// Note this is deliberately COARSER than what host_files needs. Rejecting a
// whole first segment is right for writable_home_dirs, whose entries request a
// new writable subtree — under `.config` that request is redundant. But a
// host_files destination is a single composed FILE, and `~/.config/mytool/x.json`
// is its central use case, so that guard matches exact paths instead; see
// checkHostFileDest.
//
// Core's claims are made first and win a shared segment. One of them outlives every
// selection on purpose: reservedHomeFiles holds `.claude/claude.json`, the target of core's
// `~/.claude.json` redirect, so the `.claude` segment stays reserved in a workspace that
// selects no claude pack. That is core reserving the target of a link every skeleton
// carries, not a pack's directory, so OQ-BH14 does not reach it.
func reservedHomeSegments(packs []*packload.Pack) map[string]string {
	segs := make(map[string]string, len(reservedHomeDirRoots)+len(reservedHomeFiles))
	for _, f := range reservedHomeFiles {
		segs[firstHomeSegment(f)] = ""
	}
	dirs := reservedHomeDirs(packs)
	// Sorted, so which pack a shared segment names does not depend on map order.
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		seg, owner := firstHomeSegment(d), dirs[d]
		if cur, have := segs[seg]; !have || (cur != "" && owner == "") {
			segs[seg] = owner
		}
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
//
// packs are the selected packs whose directories an entry may not claim
// (reservedHomeSegments). Each caller brings its own copy of the one selection: the
// launch its staged set, validation resolveSelectedPacks.
func checkWritableHomeDirs(v any, packs []*packload.Pack) (entries []string, problems []string) {
	prefix := "config." + writableHomeDirsKey
	list, ok := asList(v)
	if !ok {
		return nil, []string{prefix + ": expected a list of home-relative path strings"}
	}
	reserved := reservedHomeSegments(packs)
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
// and whose first segment is not one yolo already manages. reserved maps each
// managed segment to the selected pack that declares it ("" for core's own).
func checkWritableHomeDir(s string, reserved map[string]string) string {
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
	if owner, bad := reserved[seg]; bad {
		// Name the pack: under OQ-BH14 this refusal can arrive the day a pack is
		// selected, in a workspace whose config never changed, and the pack is the
		// only thing that did.
		by := ""
		if owner != "" {
			by = fmt.Sprintf(" (the selected pack %s declares it)", owner)
		}
		return fmt.Sprintf("first path segment %s is already managed read-write by yolo%s — "+
			"paths under it are writable without this key", pytext.Repr(seg), by)
	}
	return ""
}

// validateWritableHomeDirs surfaces every checkWritableHomeDirs problem as a
// `yolo check` error. Safe at any scope, so — unlike validateCacheRelocations —
// there is no workspace-scope rejection.
//
// The selection is resolved only when the key is present, so a config without it costs no
// pack resolution. A selection that resolves only partly still reserves what it could
// read; resolveSelectedPacks says why that is the right answer.
func validateWritableHomeDirs(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get(writableHomeDirsKey)
	if !present || v == nil {
		return
	}
	selected, _ := resolveSelectedPacks()
	_, problems := checkWritableHomeDirs(v, selected)
	for _, p := range problems {
		add(errs, p)
	}
}
