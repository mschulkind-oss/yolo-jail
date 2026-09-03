package packload

// embedded.go answers "what do the EMBEDDED packs declare" without a caller having to
// materialize a tree.
//
// It exists for the RESERVATION and PROVISIONING lists — the places that must account for
// every pack's writable dirs regardless of which packs a particular jail loads:
//
//	internal/config      which home roots a host_files entry may write into,
//	                     and which path segments writable_home_dirs may not claim
//	internal/storage     which GlobalHome subdirs to create
//	internal/entrypoint  which mise tool tokens to retire
//
// These are NOT selection-gated, and that distinction has been got wrong before: a
// reservation list gated on the loaded packs would let a user's host_files entry claim a
// path that a pack they add tomorrow needs, and the collision would surface as a mount
// conflict with no obvious cause. They are the union over everything yolo SHIPS.
//
// Embedded only, which bounds the guarantee honestly: a configured pack's writable dir is
// not reserved, so a user who declares a host_files entry at that path gets a conflict
// rather than a clear error. Reading the pack store from inside config validation would
// mean a filesystem dependency (and a failure mode) in a function that only inspects
// config values.

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
	embeddedRoot     string
	embeddedFS       fs.FS
)

// SetEmbeddedFS registers the embedded pack filesystem.
//
// Injected rather than imported, and the direction is forced: packload's own test imports
// the `packs` package (to pin the embed list against the tree), so `packs` cannot import
// packload back. internal/packreg holds the wiring and exists for exactly that reason.
//
// Callers that need the packs before registration get an empty set, which for a
// reservation list means "reserve only core's paths" — the same conservative result as a
// failed materialization.
func SetEmbeddedFS(f fs.FS) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	embeddedFS = f
}

// Embedded returns the embedded packs, materialized once per process.
//
// ONE temp dir, for the whole process, shared by every caller — and that is a lifetime
// decision rather than a cache, because the returned Packs are HANDLES INTO IT: Pack.Root
// names a directory the caller goes on to read (a skills tree, a briefing source, a
// contribution's `from` path). Nothing here can know when the last such read happens, and
// there is no owner to hand a cleanup func to: the first call is `internal/config`'s
// hostFileWritableRoots, a package-level var evaluated at INIT, so the tree is already
// materialized before any command has started. Process lifetime is therefore the shortest
// honest answer, and the process's exit path is where it is given back (ReleaseEmbedded).
//
// Callers that want their OWN tree with their OWN lifetime call MaterializeEmbedded
// directly and delete it themselves (internal/cli/run/packs.go stages out of one). What
// they must not do is make a second process-lifetime copy: three call sites did, and each
// leaked its own never-removed directory on every invocation of every command.
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

// ReleaseEmbedded removes the process's materialized tree and forgets it.
//
// Called on the way out of each binary that can reach Embedded (cli.Main,
// entrypoint.Main): the tree lives as long as the process needs it and no longer, so
// /tmp stops growing by one ~200 KB directory per invocation of every command.
//
// RELEASED, NOT POISONED — a later Embedded() materializes a fresh tree. That is what
// makes calling this at an exit path safe rather than a trap: a process that runs Main
// twice (the unit tests do) gets a live tree the second time instead of Packs whose Root
// has been deleted under them. Idempotent, so the exec-shaped exit paths may both defer it
// and call it explicitly.
func ReleaseEmbedded() {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	if embeddedRoot != "" {
		_ = os.RemoveAll(embeddedRoot)
	}
	embeddedRoot = ""
	embeddedPacks = nil
	embeddedProblems = nil
	embeddedLoaded = false
}

// loadEmbeddedLocked materializes the tree on first use. Caller holds embeddedMu.
func loadEmbeddedLocked() {
	if embeddedLoaded {
		return
	}
	embeddedLoaded = true
	if embeddedFS == nil {
		return
	}
	dir, err := os.MkdirTemp("", "yolo-embedded-")
	if err != nil {
		embeddedProblems = []string{"embedded packs: " + err.Error()}
		return
	}
	packs, problems := MaterializeEmbedded(embeddedFS, dir)
	if len(problems) > 0 {
		// The tree is unusable, so it is removed HERE rather than waiting for the exit
		// path: nothing is going to read a Root out of a set no caller receives.
		_ = os.RemoveAll(dir)
		embeddedProblems = problems
		return
	}
	embeddedRoot = dir
	embeddedPacks = packs
}

// EmbeddedWritableDirs is the union of every embedded pack's writableDirs.
func EmbeddedWritableDirs() []string { return WritableDirs(Embedded()) }

// EmbeddedSharedDirs is the union of every embedded pack's sharedDirs.
func EmbeddedSharedDirs() []string { return SharedDirs(Embedded()) }

// EmbeddedRetireMiseTools is the union of every embedded pack's retireMiseTools.
func EmbeddedRetireMiseTools() []string { return RetireMiseTools(Embedded()) }

// EmbeddedNames lists the embedded pack directory names, sorted.
//
// Read from the embed.FS directly rather than via Embedded(), which materializes the trees
// to disk: config validation calls this to check a bare `packs: ["claude"]` entry, and a
// name list does not justify a temp-dir copy on every config read.
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
