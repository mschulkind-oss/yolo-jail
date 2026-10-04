package cli

// patchedhost_test.go pins what a PATCHED fork's step 2 leaves at the host's edges of the jail
// launch (docs/design/patched-forks.md §9, PF-D35, PF-D38): the host floor's line names what this
// yolo does at the host, never a jail launch's build it cannot use; and the child build jail is this
// very binary and never a test binary.

import (
	"bytes"
	"errors"
	"io"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE HOST FLOOR'S LINE FOR A PATCHED FORK (PF-D35): `yolo host` does not deliver one yet, and the
// line says so and where the program does run — never the jail reason's "a fresh jail launch builds
// it", whose build is for the jail's platform and which, followed, brings the user back here.
func TestTheHostFloorSaysItDoesNotDeliverAPatchedForkYet(t *testing.T) {
	newPatchedFixture(t, "")
	sel := selectConfiguredHostPacks()
	progs := floorPrograms(sel.packs)
	f := productionHostFloor(io.Discard, progs)
	found := false
	for _, p := range progs {
		if p.Bin() != "tool" || p.Install.ForkedBy == "" {
			continue
		}
		found = true
		commit, why := f.ForkPin(p)
		if commit != "" || !strings.Contains(why, "`yolo host` does not deliver a patched fork yet") ||
			!strings.Contains(why, "run tool in a jail") || strings.Contains(why, packload.PatchedForkPinReason) {
			t.Errorf("the host floor's reason for a patched fork is %q (commit %q)", why, commit)
		}
	}
	if !found {
		t.Fatalf("the selection's floor carries no forked tool: %+v", progs)
	}
}

// THE CHILD BUILD JAIL IS THIS VERY BINARY (PF-D38), exec'd as the keeper is — /proc/self/exe on
// Linux, which `just install` cannot swap mid-launch — and a test binary never self-execs as it: the
// production command refuses here, and the advance's runner says it could not start.
func TestTheChildBuildJailIsThisBinaryAndNeverATestBinary(t *testing.T) {
	if cmd := forkBuildChildExec([]string{"internal"}); goruntime.GOOS == "linux" && cmd.Path != "/proc/self/exe" {
		t.Errorf("the child is exec'd from %q, want /proc/self/exe", cmd.Path)
	}
	if _, err := forkBuildChildCommand([]string{"internal", forkBuildJailVerb}); !errors.Is(err, errForkBuildChildFromTest) {
		t.Fatalf("the production child command under a test binary = %v, want the refusal", err)
	}
	var errw bytes.Buffer
	rc, bound := runForkBuildChild(t.Context(), forkBuildWaitBound, "/staging", forkBuild{Fork: packload.Fork{Pack: "p",
		Bin: "b"}}, io.Discard, &errw, false)
	if rc == 0 || bound || !strings.Contains(errw.String(), "could not start the build jail") {
		t.Errorf("the runner under a test binary = %d (bound %v): %s", rc, bound, errw.String())
	}
}
