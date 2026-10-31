package paths

import (
	"path/filepath"
	"sync"
)

// userConfigDirOverride, when set, is the directory every user-config reader reads instead of
// ~/.config/yolo-jail. Only `yolo config migrate` sets it, for the moment it validates the
// converted files the way a launch would read them, from a staging tree, before it touches any
// original (docs/design/config-migration.md §4, RC-D44). Nothing else under the home moves.
var (
	userConfigDirMu       sync.RWMutex
	userConfigDirOverride string
)

// userConfigDir is ~/.config/yolo-jail, or the override while one is set.
func userConfigDir() string {
	userConfigDirMu.RLock()
	o := userConfigDirOverride
	userConfigDirMu.RUnlock()
	if o != "" {
		return o
	}
	return filepath.Join(home(), ".config", "yolo-jail")
}

// ReadUserConfigFrom makes every user-config reader read dir until the returned restore runs.
// It is process-wide: a caller holds no other config read in flight while it is set.
func ReadUserConfigFrom(dir string) (restore func()) {
	userConfigDirMu.Lock()
	prev := userConfigDirOverride
	userConfigDirOverride = dir
	userConfigDirMu.Unlock()
	return func() {
		userConfigDirMu.Lock()
		userConfigDirOverride = prev
		userConfigDirMu.Unlock()
	}
}
