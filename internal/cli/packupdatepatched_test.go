package cli

// packupdatepatched_test.go pins that the host apply `yolo pack update` runs on the host
// (packupdate.go, OQ-3) builds no patched fork and no patched extension: the update checks and
// replays them and builds nothing (docs/design/patched-forks.md §8.3, PF-D12; patched-extensions.md
// PPX-D28), so its own line, "the next launch builds it", stays true. `yolo host apply --assert`
// typed by itself still runs the advance (PF-D50, PPX-D25), and so does `yolo host -- <bin>`.

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// productionHostApplyFromPackUpdate is the seam's production value, read at package init, before
// any fixture stubs it (forkPinHomeSeams).
var productionHostApplyFromPackUpdate = hostApplyFromPackUpdate

// usePackUpdatesHostApply puts the production host apply back behind `yolo pack update`.
func usePackUpdatesHostApply(t *testing.T) {
	t.Helper()
	prev := hostApplyFromPackUpdate
	hostApplyFromPackUpdate = productionHostApplyFromPackUpdate
	t.Cleanup(func() { hostApplyFromPackUpdate = prev })
}

// A PATCHED FORK at the host floor: the update's host apply builds nothing and says what does; a
// `yolo host apply --assert` typed by itself then builds and installs it.
func TestPackUpdatesHostApplyBuildsNoPatchedFork(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	usePackUpdatesHostApply(t)
	rc, out, errw := packVerb(t, "update")
	if len(fx.builds) != 0 {
		t.Fatalf("`yolo pack update` built the patched fork %d times (rc=%d)\n%s\n%s", len(fx.builds), rc, out, errw)
	}
	for _, w := range []string{"applies — " + patchedNotBuilt, "tool: not installed yet — there is no build of tool " +
		"from fork pack forkpack's patch series on this machine, and " + packUpdateBuildsNoPatched + "\n"} {
		if !strings.Contains(out+errw, w) {
			t.Errorf("`yolo pack update` lacks %q:\n%s\n%s", w, out, errw)
		}
	}
	if rc, report := applyWith(t, true, nil); rc != 0 || len(fx.builds) != 1 {
		t.Errorf("`yolo host apply --assert` after the update: rc=%d builds=%d\n%s", rc, len(fx.builds), report)
	}
}

// A PATCHED EXTENSION at the host's render: the update's host apply builds no tree; a `yolo host
// apply --assert` typed by itself builds it.
func TestPackUpdatesHostApplyBuildsNoPatchedExtension(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	usePackUpdatesHostApply(t)
	rc, out, errw := packVerb(t, "update")
	if len(fx.builds) != 0 {
		t.Fatalf("`yolo pack update` built the patched extension %d times (rc=%d)\n%s\n%s", len(fx.builds), rc, out, errw)
	}
	var w bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &w, false, strings.NewReader(""))
	if len(fx.builds) != 1 {
		t.Errorf("`yolo host apply --assert` after the update built %d trees:\n%s", len(fx.builds), w.String())
	}
}
