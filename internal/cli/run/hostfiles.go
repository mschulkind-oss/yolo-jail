package run

// hostfiles.go is the HOST half of host_files
// (docs/reference/composed-file-permissions.md): the run
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
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostUserCtxDir is the read-only mount root under which each source-bearing
// entry's host file (or directory) is bound, keyed by the entry's slug. The
// entrypoint reads the same path (internal/entrypoint.hostUserPath, which resolves it
// through that package's ctxRoot so a relocated /ctx moves both readers together).
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
//
// A DIRECTORY source asks roBindsUnsupported like the other `/ctx` read-only binds that
// consult it (config `mounts`, pack `mount` grants, the host nvim config, captures) — not
// every `:ro` bind in this package does — and below acROBindsFloor it is
// REFUSED with the reason rather than bound: that Apple Container accepts `:ro` and
// ignores it, so the bind would hand the jail write access to a host tree the user
// declared read-only. Declining leaves the entrypoint nothing at /ctx/host-user/<slug>,
// which its stageHostFile treats as fail-open (the same as a source not created yet), so
// no masking destination is written. The FILE branch needs no such gate on that backend:
// there it copies rather than binds.
func (o *Options) hostUserFileArgs(in *assembleInput) []string {
	var args []string
	for _, entry := range sortedHostFiles(in.hostFiles) {
		if !entry.SourceBearing() {
			continue
		}
		target := hostUserCtxDir + "/" + entry.Slug()
		if entry.IsDir {
			if !isDir(entry.Source) {
				continue
			}
			if reason := o.roBindsUnsupported(in.rt); reason != "" {
				o.pr(o.Stdout).print("[yellow]Skipping host_files directory ~/" + entry.Path +
					" (source " + entry.Source + " → " + target + "): " + reason + "[/yellow]")
				continue
			}
			args = append(args, "-v", entry.Source+":"+target+":ro")
			continue
		}
		if !isFile(entry.Source) {
			continue
		}
		// APPLE CONTAINER GETS A COPY, BY CHOICE. A single regular-file bind works on
		// `container` 1.1.0 (TestAppleContainerBindsASingleFile); the copy is kept
		// because it needs no version gate (acMaterialize says why). What it guards
		// against is why guessing wrong is costly here: a file bind that does not
		// arrive does not error, and unlike the pack `reads-host` case this one would
		// not merely omit — it would MASK. The entrypoint renders the destination
		// anyway at the `readonly` default mode, so the user would get their other
		// layers only (an EMPTY 0o444 file for a plain copy) where their .npmrc
		// should be, and could not fix it from inside the jail.
		//
		// The dir branch above is not converted to a copy: AC nests directory mounts
		// fine (paths.GlobalCache proves it), so from acROBindsFloor a dir entry binds,
		// and below it roBindsUnsupported refuses the entry instead.
		if in.rt == "container" {
			acMaterialize(entry.Source,
				filepath.Join(acCtxDirRel, "host-user", entry.Slug()), in.wsState)
			in.acCtxMaterialized = true
			continue
		}
		args = append(args, ROFileMountArg(
			entry.Source, target, in.wsState,
			"ctx-host-user/"+entry.Slug(), in.mountTargets, nil)...)
	}
	return args
}

// hostFileWritableDirArgs mounts a writable subtree for every destination that
// needs one (config.HostFileStagingWritableDir — a new top-level dir), reusing the
// writable_home_dirs shape: <wsState>/writable-home/<rel> bound rw over
// /home/agent/<rel>, nested inside the :ro home skeleton. prepareHostFiles created the
// backing end and buildHomeSkeleton the mountpoint, both before assembly.
//
// Deduped and sorted: two entries can share a parent (~/foo/a.json and
// ~/foo/b.json), and a duplicate -v for one destination is a hard podman error.
//
// PODMAN ONLY. Apple Container binds wsState read-write, whole, at /home/agent, so a new
// top-level destination is writable there as it stands, and nothing on that backend's launch
// path creates a writable-home backing dir (prepareHostFiles and the skeleton are podman-only):
// a bind emitted there named a missing source.
func (o *Options) hostFileWritableDirArgs(in *assembleInput) []string {
	if in.rt == "container" { // parity: HonoredBy — Apple Container binds this workspace's wsState read-write at /home/agent, so the entrypoint's write lands in wsState with no staging
		return nil
	}
	var args []string
	for _, rel := range hostFileWritableDirs(in.hostFiles, in.packs, in.writableHomeDirs) {
		args = append(args, "-v",
			filepath.Join(in.wsState, config.WritableHomeBackingSubdir, rel)+":/home/agent/"+rel)
	}
	return args
}

// hostFileWritableDirs returns the deduped, sorted home-relative subtrees the
// host_files destinations need staged read-write. Shared by the two provisioning steps
// (prepareHostFiles creates the backing end, buildHomeSkeleton the mountpoint) and the
// argv emitter, so the three cannot disagree about which dirs exist.
//
// An entry whose subtree is already covered by ANOTHER entry's writable dir is
// dropped — nesting a second bind inside the first is redundant, and for a
// directory entry it would shadow the tree the copy is about to write.
//
// packs are the launch's SELECTED packs: an entry under one of their writable dirs needs
// nothing, and an entry under a dir only an unselected pack declares is staged like any
// other new top-level dir (config.HostFileEntry.StagingFor, the OQ-BH14 ruling).
//
// writableHomeDirs are the launch's validated writable_home_dirs entries, and a subtree at or
// under one is dropped too: that key stages the SAME backing layout
// (<wsState>/writable-home/<path>), so its bind already covers the destination, and staging it
// again was a second, identical -v at one destination, which podman refuses. OQ-BH14 made that
// easy to reach: `writable_home_dirs: [".codex"]` is legal in a workspace that does not select
// codex, beside a host_files entry under ~/.codex/.
func hostFileWritableDirs(entries []config.HostFileEntry, packs []*packload.Pack, writableHomeDirs []string) []string {
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.StagingFor(packs) != config.HostFileStagingWritableDir {
			continue
		}
		seen[entry.WritableParent()] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		if !underAny(rel, writableHomeDirs) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return dropNestedPaths(out)
}

// underAny reports whether p is one of dirs or lies under one (slash-separated, cleaned).
func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || hasPathPrefix(p, d) {
			return true
		}
	}
	return false
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

// prepareHostFiles provisions the WORKSPACE-OVERLAY half of everything a host_files
// destination needs to be writable, BEFORE the container starts. Three cases, matching
// config.HostFileEntry.StagingFor (docs/reference/composed-file-permissions.md §7.5):
//
//   - None: nothing to do — the destination is already under a rw bind.
//   - Symlink: the overlay dir that holds the link targets. The RELATIVE link itself,
//     pointing into the writable ~/.config overlay and left DANGLING on purpose (`once`
//     seeds a file it cannot stat, so a pre-created target would permanently suppress
//     the seed), is the podman home skeleton's (buildHomeSkeleton).
//   - WritableDir: the writable_home_dirs recipe's backing dir under wsState, which is
//     load-bearing (podman fails the whole container on a missing bind source). The
//     mountpoint inside the :ro home root is the skeleton's too — belt-and-braces (podman
//     auto-creates it on the runtimes tested, but pre-creating it makes the mode/ownership
//     deterministic instead of podman's drwxr-xr-t).
//
// Both skeleton halves used to be created here, in the shared base home every podman jail
// mounted, so one workspace's host_files links showed up in every jail on the machine.
//
// Best-effort throughout: a provisioning failure degrades that one entry to the
// entrypoint's fail-open warning rather than blocking the launch.
//
// packs and writableHomeDirs are the launch's selected packs and validated writable_home_dirs
// entries, the same values the skeleton and the argv read, so the three cannot disagree about
// which entries are staged.
//
// replaced is every link it replaced at a backing dir, relative to wsState, for the caller to
// name (printReplacedLinks): the launch says so at every bind source it replaces a link at.
func prepareHostFiles(wsState string, entries []config.HostFileEntry, packs []*packload.Pack, writableHomeDirs []string) (replaced []string) {
	// Beneath wsState, which the jail can write (wsstatebeneath.go): each backing dir is a podman
	// bind source, so a link the jail left at one, or above one, is replaced by a real directory
	// rather than followed, and the yolo-home dir is never created through a link.
	for _, rel := range hostFileWritableDirs(entries, packs, writableHomeDirs) {
		replaced = append(replaced, ensureBindSourceDir(wsState, filepath.Join(config.WritableHomeBackingSubdir, filepath.FromSlash(rel)))...)
	}

	var needConfigDir bool
	for _, entry := range entries {
		if entry.StagingFor(packs) == config.HostFileStagingSymlink {
			needConfigDir = true
		}
	}
	if needConfigDir {
		// The link targets live under the per-workspace .config overlay, whose
		// backing dir is <wsState>/config. Create the yolo-home subdir there so the
		// entrypoint's write lands in an existing directory.
		_ = mkdirAllBeneath(wsState, filepath.Join("config", "yolo-home"))
	}
	return replaced
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
