//go:build !darwin

package journald

// processOwner has no answer off darwin, where no macos-log bridge runs (the loophole
// declares `platforms: ["darwin"]`). Every entry is then unattributable, and an
// unattributable entry is dropped: the narrow answer.
func processOwner(int) (uint32, bool) { return 0, false }
