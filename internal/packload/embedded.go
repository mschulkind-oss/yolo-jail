package packload

// embedded.go answers "what do the EMBEDDED packs declare" without a caller having to
// materialize a tree.
//
// It exists for the lists that must account for every pack yolo SHIPS, whether or not a
// particular jail loads it:
//
//	internal/storage     which machine-store (GlobalHome) shared dirs to create — before
//	                     any config is loaded, so the selection is not known yet
//	internal/config      only to look the SELECTED embedded packs up by name (resolveSelectedPacks);
//	                     no reservation reads the whole set since OQ-BH15
//	internal/entrypoint  which mise tool tokens to retire (core's list today; reads no pack)
//
// NAME RESERVATION IS NOT ONE OF THEM ANY MORE. Which home roots a host_files entry needs no
// staging under, and which path segments writable_home_dirs may not claim, were read from
// here on the argument that a list gated on the loaded packs would let an entry claim a path
// a pack added tomorrow needs. The maintainer ruled the other way (OQ-BH14,
// docs/design/base-home-legacy-state.md#28-reservation-is-a-rule-about-config-names-not-about-directories):
// an unselected pack is treated as if it does not exist, and the shipped set never covered a
// configured pack anyway. Those lists take the SELECTED packs now (config.WritableHomeDirs,
// config.HostFileEntry.StagingFor), and config validation resolves the selection itself,
// configured packs included, from the pack store (internal/config/selectedpacks.go).

import (
	"io/fs"
	"os"
	"sort"
	"sync"
)

var (
	embeddedMu       sync.Mutex
	embeddedLoaded   bool
	embeddedPacks    []*Pack
	embeddedProblems []string
	embeddedFS       fs.FS

	// embeddedRoot is the tree the packs were loaded from: <base>/<hash> in the cache, or a
	// per-process TMPDIR dir when embeddedFallback. Only a fallback tree is ever deleted by
	// this process.
	embeddedRoot     string
	embeddedFallback bool
	// embeddedLease is the SHARED flock on embeddedRoot/.lease, held for as long as the
	// packs are — the liveness evidence a reaper asks for. Nil when the filesystem cannot
	// lock, which a reaper reads as "could not ask" and keeps.
	embeddedLease *os.File
)

// SetEmbeddedFS registers the embedded pack filesystem.
//
// Injected rather than imported, and the direction is forced: packload's own test imports
// the `packs` package (to pin the embed list against the tree), so `packs` cannot import
// packload back. internal/packreg holds the wiring and exists for exactly that reason.
//
// Registration creates nothing: no tree is written and no hash is computed until the first
// caller asks for the packs (Embedded), so a process that never reads a pack pays nothing.
// Callers that ask before registration get an empty set, which for a reservation list means
// "reserve only core's paths" — the same conservative result as a failed materialization.
func SetEmbeddedFS(f fs.FS) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	embeddedFS = f
	embeddedHashMemo = nil
}

// Embedded returns the embedded packs, materialized on the FIRST CALL and shared by every
// later one.
//
// The returned Packs are HANDLES into a tree on disk: Pack.Root names a directory the caller
// goes on to read (a skills tree, a briefing source, a contribution's `from` path), long after
// this returns and with no owner to hand a cleanup func to. So the tree is not this call's to
// delete, and embeddedcache.go decides where it lives:
//
//   - Normally it is ONE IMMUTABLE, CONTENT-ADDRESSED tree per build under
//     paths.EmbeddedPacksDir() (~/.local/share/yolo-jail/embedded-packs/<hash>), populated
//     atomically by whichever process gets there first and REUSED by every other process of
//     the same build. This process holds a shared lease on it (embeddedlease.go) and never
//     deletes it — `yolo prune` reaps other builds' trees once no process holds them.
//   - When that location is unavailable, it is a per-process FALLBACK tree in TMPDIR, which
//     ReleaseEmbedded deletes and whose lease lets a later process reap it after a crash.
//
// Nothing is materialized at package init any more: until 2026-09-23 internal/config's
// hostFileWritableRoots called this from a package-level initializer, so every process that
// linked internal/config — every test binary, `yolo --version`, every in-jail daemon — wrote
// a ~250 KB temp tree, and each exit path that skipped a defer (exec, a signal arm, a daemon
// with no release at all) leaked one.
//
// Callers that want their OWN tree with their OWN lifetime call MaterializeEmbedded directly
// and delete it themselves (internal/cli/run/packs.go stages out of one). What they must not
// do is make a second process-lifetime copy: three call sites did, and each leaked its own
// never-removed directory on every invocation of every command.
func Embedded() []*Pack {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	loadEmbeddedLocked()
	return embeddedPacks
}

// EmbeddedProblems reports what went wrong materializing the embedded packs, or nothing.
//
// Separate from Embedded, which answers with an empty set on any problem: a reservation
// list reserving only core's paths is the conservative reading of "no packs" and needs no
// error, but a caller asking for a pack BY NAME (entrypoint.ConfigurePackByName, the CLI's
// surface merge) can say which pack broke instead of "no embedded pack named x". A broken
// embedded pack is a yolo bug either way.
func EmbeddedProblems() []string {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	loadEmbeddedLocked()
	return embeddedProblems
}

// EmbeddedLoaded reports whether this process has materialized (or tried to materialize)
// the embedded packs since start or the last ReleaseEmbedded. It exists for the pins that a
// process which never reads a pack creates nothing — `yolo --version`, package init.
func EmbeddedLoaded() bool {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	return embeddedLoaded
}

// EmbeddedLocation reports the tree this process's packs were loaded from, and whether it
// is a per-process fallback (deleted by ReleaseEmbedded) rather than the shared cache tree.
// "" before the first Embedded() call, after a release, or when nothing could be loaded.
func EmbeddedLocation() (root string, fallback bool) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	return embeddedRoot, embeddedFallback
}

// ReleaseEmbedded gives back the process's hold on the embedded tree and forgets the packs.
//
// Called on the way out of each binary that can reach Embedded (cli.Main, entrypoint.Main,
// yolo-jaild) and before every exec-shaped or signal-shaped exit that would skip a defer
// (`yolo host`'s exec, the launcher's terminate arm). What it gives back depends on where the
// tree lives:
//
//   - the shared CACHE tree is NOT deleted — other processes of this build use it — only this
//     process's lease on it is closed;
//   - a per-process FALLBACK tree is deleted.
//
// RELEASED, NOT POISONED — a later Embedded() loads again (re-adopting the same cache tree).
// That is what makes calling this at an exit path safe rather than a trap: a process that
// runs Main twice (the unit tests do) gets a live tree the second time instead of Packs whose
// Root has been deleted under them. Idempotent, so the exec-shaped exit paths may both defer
// it and call it explicitly.
func ReleaseEmbedded() {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	releaseEmbeddedLocked()
}

func releaseEmbeddedLocked() {
	if embeddedFallback && embeddedRoot != "" {
		_ = os.RemoveAll(embeddedRoot)
	}
	if embeddedLease != nil {
		_ = embeddedLease.Close()
	}
	embeddedRoot = ""
	embeddedFallback = false
	embeddedLease = nil
	embeddedPacks = nil
	embeddedProblems = nil
	embeddedLoaded = false
}

// loadEmbeddedLocked loads the packs on first use: from the content-addressed cache when its
// location is usable, else from a per-process fallback tree. Caller holds embeddedMu.
func loadEmbeddedLocked() {
	if embeddedLoaded {
		return
	}
	embeddedLoaded = true
	if embeddedFS == nil {
		return
	}
	if base, strict := embeddedCacheBaseLocked(); base != "" && loadFromCacheLocked(base, strict) {
		return
	}
	loadFallbackLocked()
}

// EmbeddedSharedDirs is the union of every embedded pack's sharedDirs.
func EmbeddedSharedDirs() []string { return SharedDirs(Embedded()) }

// EmbeddedRetireMiseTools is the retired mise-tool list the boot path strips.
//
// It reads NO pack, and must not: the list is core's own (RetiredMiseTools — RetireMiseTools
// ignores its packs argument), while the jail boot calls this on every start. Routed through
// Embedded() it materialized (or adopted and byte-verified) the whole tree on every boot to
// read a hard-coded list, and wrote a persistent copy into every workspace's `.local`
// overlay for each new build.
func EmbeddedRetireMiseTools() []string { return RetireMiseTools(nil) }

// EmbeddedNames lists the embedded pack directory names, sorted.
//
// Read from the embed.FS directly rather than via Embedded(), which materializes the trees
// to disk: config validation calls this to check a bare `packs: ["claude"]` entry, and a
// name list does not justify hashing and adopting a tree on every config read.
func EmbeddedNames() []string {
	if embeddedFS == nil {
		return nil
	}
	entries, err := fs.ReadDir(embeddedFS, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
