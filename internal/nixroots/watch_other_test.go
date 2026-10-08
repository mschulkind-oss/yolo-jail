//go:build !linux

package nixroots

import (
	"context"
	"strings"
	"testing"
)

// Off Linux there is no watcher (the design's §5.2: macos-user shares host paths, so a root
// nix makes there is already one the host honors). Run says so at once, admitting nothing,
// rather than appearing to watch.
func TestOffLinuxTheWatcherRefusesAndKeepsNothing(t *testing.T) {
	f, w := watchFixture(t)
	src := f.userLink(t, "result", f.paths[0])
	autoEntry(t, w, "aaaa", src)
	err := w.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("Run = %v, want the Linux-only refusal", err)
	}
	if roots, _ := f.reg.List(); len(roots) != 0 {
		t.Errorf("kept %+v off Linux", roots)
	}
}
