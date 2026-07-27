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
	"sync"
)

var (
	embeddedOnce  sync.Once
	embeddedPacks []*Pack
	embeddedFS    fs.FS
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
func SetEmbeddedFS(f fs.FS) { embeddedFS = f }

// Embedded returns the embedded packs, materialized once per process.
func Embedded() []*Pack {
	embeddedOnce.Do(func() {
		if embeddedFS == nil {
			return
		}
		dir, err := os.MkdirTemp("", "yolo-embedded-")
		if err != nil {
			return
		}
		packs, problems := MaterializeEmbedded(embeddedFS, dir)
		if len(problems) > 0 {
			return
		}
		embeddedPacks = packs
	})
	return embeddedPacks
}

// EmbeddedWritableDirs is the union of every embedded pack's writableDirs.
func EmbeddedWritableDirs() []string { return WritableDirs(Embedded()) }

// EmbeddedSharedDirs is the union of every embedded pack's sharedDirs.
func EmbeddedSharedDirs() []string { return SharedDirs(Embedded()) }

// EmbeddedRetireMiseTools is the union of every embedded pack's retireMiseTools.
func EmbeddedRetireMiseTools() []string { return RetireMiseTools(Embedded()) }
