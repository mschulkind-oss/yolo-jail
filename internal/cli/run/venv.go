package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/perside"
)

// The per-side path helpers live in internal/perside, so the macos-user backend reads the
// SAME set the container mounts shadow (its disclosure and its uv redirect). These forwarders
// keep this package's callers and tests unchanged.

// ValidPerSideRel reports whether rel is a shadowable workspace sub-path (perside.ValidRel).
func ValidPerSideRel(rel string) bool { return perside.ValidRel(rel) }

// MiseConfigVenvPath resolves env._.python.venv from a workspace's mise configs
// (perside.MiseConfigVenvPath, which states its last-hit-wins rule).
func MiseConfigVenvPath(resolveFile func(fname string) (map[string]any, bool)) (string, bool) {
	return perside.MiseConfigVenvPath(resolveFile)
}

// MiseConfigVenvPathFromDir is MiseConfigVenvPath backed by the real filesystem
// (perside.MiseConfigVenvPathFromDir).
func MiseConfigVenvPathFromDir(dir string) (string, bool) {
	return perside.MiseConfigVenvPathFromDir(dir)
}
