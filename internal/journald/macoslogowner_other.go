//go:build !darwin

package journald

import "time"

// processOwner has no answer off darwin, where no macos-log bridge runs (the loophole
// declares `platforms: ["darwin"]`). Every entry is then unattributable, and an
// unattributable entry is dropped: the narrow answer.
func processOwner(int) (uint32, time.Time, bool) { return 0, time.Time{}, false }
