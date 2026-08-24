package run

// hostfiles.go is the HOST half of docs/plans/host-file-staging.md: the run
// pipeline's job is to resolve the user's `host_files` entries once, carry each
// source-bearing entry's bytes across the boundary as a `:ro` mount, make every
// destination writable, and hand the resolved list to the entrypoint through
// YOLO_HOST_FILES.
//
// The split matters: the host CLI is the ONLY side that can read the user config
// (a source-bearing entry is user-scope by construction — see
// config.LoadHostFiles) and the only side that can stat a host path. The
// entrypoint therefore never re-reads config; it decodes YOLO_HOST_FILES and
// trusts the slugs, which is what guarantees the slug it derives for a surface
// matches the /ctx/host-user/<slug> mount emitted here.

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// hostUserCtxDir is the read-only mount root under which each source-bearing
// entry's host file (or directory) is bound, keyed by the entry's slug. The
// entrypoint reads the same path (internal/entrypoint.hostUserDir).
const hostUserCtxDir = "/ctx/host-user"

// hostFilesEnv returns the `-e YOLO_HOST_FILES=<json>` pair, or nil when there
// are no entries. Emitted OUTSIDE commonEnvBlock so the frozen golden argv of a
// jail with no host_files is unchanged — this feature adds argv only when used.
//
// A marshal failure drops the whole set rather than emitting a partial one: the
// entrypoint would reject a truncated value anyway, and a jail missing a declared
// file is easier to diagnose than one holding half of them.
func (o *Options) hostFilesEnv(in *assembleInput) []string {
	if len(in.hostFiles) == 0 {
		return nil
	}
	wire, err := config.MarshalHostFiles(in.hostFiles)
	if err != nil || wire == "" {
		if err != nil {
			o.pr(o.Stdout).print("[yellow]Warning: host_files: " + err.Error() + " — skipping[/yellow]")
		}
		return nil
	}
	return []string{"-e", "YOLO_HOST_FILES=" + wire}
}

// hostUserFileArgs mounts each SOURCE-BEARING entry's host path read-only at
// /ctx/host-user/<slug>. This is the sibling of hostFileArgs (the yolo-declared
// per-agent set) for the USER-declared set, and the only channel by which a host
// file's bytes cross into the jail for this feature.
//
// Skips a source that does not exist: podman kills the whole container with a bare
// "statfs <path>: no such file or directory" when a bind source is missing, and a
// host file the user has not created yet is a normal state — the surface simply
// falls back to its defaults layer (config.probeHostFileSource deliberately does
// not reject it either).
//
// A FILE source goes through ROFileMountArg so a nested jail (where the source
// may itself be a bind mountpoint, which rootless podman cannot use as a bind
// source) gets the copy-to-wsState dereference. A DIRECTORY source is bound
// directly: the deref exists for single-file binds, and a directory mountpoint is
// usable as a bind source.
func (o *Options) hostUserFileArgs(in *assembleInput) []string {
	var args []string
	materialized := false
	for _, entry := range sortedHostFiles(in.hostFiles) {
		if !entry.SourceBearing() {
			continue
		}
		target := hostUserCtxDir + "/" + entry.Slug()
		if entry.IsDir {
			if !isDir(entry.Source) {
				continue
			}
			args = append(args, "-v", entry.Source+":"+target+":ro")
			continue
		}
		if !isFile(entry.Source) {
			continue
		}
		// APPLE CONTAINER CANNOT BIND A SINGLE FILE, and unlike the pack `reads-host`
		// case this one does not merely omit — it MASKS. The entrypoint swallows the
		// read error and prism.go writes the destination anyway at the `readonly`
		// default mode, so the user ends up with an EMPTY 0o444 file where their
		// .npmrc should be, which they then cannot fix from inside the jail.
		//
		// The dir branch above is deliberately untouched: AC nests directory mounts
		// fine (paths.GlobalCache proves it), so a dir entry is already honored.
		if in.rt == "container" {
			acMaterialize(entry.Source,
				filepath.Join(acCtxDirRel, "host-user", entry.Slug()), in.wsState)
			materialized = true
			continue
		}
		args = append(args, ROFileMountArg(
			entry.Source, target, in.wsState,
			"ctx-host-user/"+entry.Slug(), in.mountTargets, nil)...)
	}
	if materialized {
		args = append(args, "-e", "YOLO_CTX_ROOT=/home/agent/"+acCtxDirRel)
	}
	return args
}

// hostFileWritableDirArgs mounts a writable subtree for every destination that
// needs one (config.HostFileStagingWritableDir — a new top-level dir), reusing the
// writable_home_dirs shape: <wsState>/writable-home/<rel> bound rw over
// /home/agent/<rel>, nested inside the :ro base. prepareHostFiles created both
// ends before assembly.
//
// Deduped and sorted: two entries can share a parent (~/foo/a.json and
// ~/foo/b.json), and a duplicate -v for one destination is a hard podman error.
func (o *Options) hostFileWritableDirArgs(in *assembleInput) []string {
	var args []string
	for _, rel := range hostFileWritableDirs(in.hostFiles) {
		args = append(args, "-v",
			filepath.Join(in.wsState, config.WritableHomeBackingSubdir, rel)+":/home/agent/"+rel)
	}
	return args
}

// hostFileWritableDirs returns the deduped, sorted home-relative subtrees the
// host_files destinations need staged read-write. Shared by the provisioning step
// (which creates both ends) and the argv emitter, so the two cannot disagree
// about which dirs exist.
//
// An entry whose subtree is already covered by ANOTHER entry's writable dir is
// dropped — nesting a second bind inside the first is redundant, and for a
// directory entry it would shadow the tree the copy is about to write.
func hostFileWritableDirs(entries []config.HostFileEntry) []string {
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.StagingFor() != config.HostFileStagingWritableDir {
			continue
		}
		seen[entry.WritableParent()] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return dropNestedPaths(out)
}

// dropNestedPaths removes any path that lies under another path in the (sorted)
// list, so only the outermost subtrees are mounted. Sorted input means a parent
// always precedes its children.
func dropNestedPaths(sorted []string) []string {
	var out []string
	for _, p := range sorted {
		nested := false
		for _, kept := range out {
			if p == kept || hasPathPrefix(p, kept) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

// hasPathPrefix reports whether p lies under dir (slash-separated, both cleaned).
func hasPathPrefix(p, dir string) bool {
	return len(p) > len(dir) && p[:len(dir)] == dir && p[len(dir)] == '/'
}

// prepareHostFiles provisions everything a host_files destination needs to be
// writable, BEFORE the container starts. Three cases, matching
// config.HostFileEntry.StagingFor (docs/design/composed-file-permissions.md §7.5):
//
//   - None: nothing to do — the destination is already under a rw bind.
//   - Symlink: materialize a RELATIVE symlink in GlobalHome pointing into the
//     writable ~/.config overlay, plus the overlay dir that holds the targets.
//     The symlink is left DANGLING on purpose: `once` seeds a file it cannot
//     stat, so a pre-created target would permanently suppress the seed.
//   - WritableDir: the writable_home_dirs recipe — backing dir under wsState AND
//     the mountpoint inside GlobalHome. The backing dir is load-bearing (podman
//     fails the whole container on a missing bind source); the GlobalHome
//     mountpoint is belt-and-braces (podman auto-creates it on the runtimes
//     tested, but pre-creating it makes the mode/ownership deterministic instead
//     of podman's drwxr-xr-t).
//
// Best-effort throughout: a provisioning failure degrades that one entry to the
// entrypoint's fail-open warning rather than blocking the launch.
func prepareHostFiles(wsState string, entries []config.HostFileEntry) {
	for _, rel := range hostFileWritableDirs(entries) {
		_ = os.MkdirAll(filepath.Join(wsState, config.WritableHomeBackingSubdir, rel), 0o755)
		_ = os.MkdirAll(filepath.Join(paths.GlobalHome(), rel), 0o755)
	}

	var needConfigDir bool
	for _, entry := range entries {
		if entry.StagingFor() != config.HostFileStagingSymlink {
			continue
		}
		needConfigDir = true
		// The symlink lives in the :ro base and resolves THROUGH the container's
		// mount table into the rw .config overlay, so the link text must be
		// relative (storage.EnsureSymlink enforces that) and is never resolved
		// host-side.
		link := filepath.Join(paths.GlobalHome(), filepath.FromSlash(entry.Path))
		_ = os.MkdirAll(filepath.Dir(link), 0o755)
		_ = storage.EnsureSymlink(link, entry.SymlinkTarget())
	}
	if needConfigDir {
		// The link targets live under the per-workspace .config overlay, whose
		// backing dir is <wsState>/config. Create the yolo-home subdir there so the
		// entrypoint's write lands in an existing directory.
		_ = os.MkdirAll(filepath.Join(wsState, "config", "yolo-home"), 0o755)
	}
}

// sortedHostFiles returns the entries ordered by destination Path without
// mutating the caller's slice (assembleRunCmd is a pure function of its input).
// config.LoadHostFiles already sorts; this keeps the guarantee local to the
// emitter, matching sortedCacheRelocations / sortedWritableHomeDirs.
func sortedHostFiles(entries []config.HostFileEntry) []config.HostFileEntry {
	out := append([]config.HostFileEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
