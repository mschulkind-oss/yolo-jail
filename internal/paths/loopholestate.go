package paths

import "path/filepath"

// LoopholeStateDir is the host-owned state directory for one loophole. Its name-keyed
// location is shared by discovery, configuration validation and runtime state consumers.
func LoopholeStateDir(name string) string {
	return filepath.Join(GlobalStorage(), "state", name)
}
