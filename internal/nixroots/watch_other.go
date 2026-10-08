//go:build !linux

package nixroots

import (
	"context"
	"errors"
)

// Run is Linux-only: the watcher exists for a container jail, whose mount namespace differs
// from the host's. macos-user shares the host's filesystem, so a root made there is already
// one the host honors and no watcher is needed (the design's §5.2).
func (w *Watcher) Run(ctx context.Context) error {
	return errors.New("the root watcher runs only in a Linux container jail; on macos-user, `nix-store --add-root` already makes a root the host honors")
}
