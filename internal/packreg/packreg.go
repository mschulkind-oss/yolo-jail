// Package packreg wires the embedded packs into packload.
//
// It exists ONLY to break an import cycle, and the cycle is a real one rather than a
// tooling quirk: packload must be able to read the embedded packs, and the natural way to
// arrange that is an init() in the `packs` package calling packload.SetEmbeddedFS. But
// packload's own test imports `packs` (to pin the go:embed list against the tree), so
// `packs` importing packload makes that a cycle-in-test.
//
// So the registration lives in a third package that imports both and is imported for its
// side effect:
//
//	import _ "github.com/mschulkind-oss/yolo-jail/internal/packreg"
//
// An init() rather than a call from main, because the reservation lists that consume this
// (internal/config's hostFileWritableRoots, internal/storage's overlay subdirs) are
// package-level values evaluated at init time. A main-time registration would arrive too
// late and they would silently see no packs — reserving nothing, with no error.
package packreg

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func init() { packload.SetEmbeddedFS(packs.FS) }
