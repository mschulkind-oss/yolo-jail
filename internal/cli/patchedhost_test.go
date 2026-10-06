package cli

// patchedhost_test.go pins what a PATCHED fork's step 2 leaves at the host's edge of the jail
// launch (docs/design/patched-forks.md PF-D38): the child build jail is this very binary and never a
// test binary. What the host floor does with a patched fork (step 4) is hostfloorpatched_test.go's.

import (
	"bytes"
	"errors"
	"io"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

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
		Bin: "b"}}, captureStreams{out: io.Discard, errw: &errw, jailOut: io.Discard, jailErr: io.Discard}, false)
	if rc == 0 || bound || !strings.Contains(errw.String(), "could not start the build jail") {
		t.Errorf("the runner under a test binary = %d (bound %v): %s", rc, bound, errw.String())
	}
}
