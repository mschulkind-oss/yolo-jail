package entrypoint

import (
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
	"github.com/mschulkind-oss/yolo-jail/internal/testutil"
)

// TestThisPackageLinksYoloOnce fails if TestMain stops opting in (the call site of
// testutil.ShareYoloBinary): every launcher test would then relink yolo again.
func TestThisPackageLinksYoloOnce(t *testing.T) {
	if !testutil.YoloBinarySharing() {
		t.Fatal("TestMain must call testutil.ShareYoloBinary, or each launcher test links yolo itself")
	}
}

// TestMain lets the test binary play the entrypoint for
// TestIOPriorityReachesEveryThreadAfterOneReexec: the apply re-executes the process, which
// only a subprocess can survive. It also plays the jail's yolo for the update-bound cells
// (asYoloEnv). Without the mode variable it runs the tests, searching an
// empty stand-in for the image's bin dirs rather than the machine's (imagebins_test.go).
func TestMain(m *testing.M) {
	// The stand-in for the jail's yolo (updatebound_test.go): the variable is the launchers'
	// children's business no further than this process.
	if os.Getenv(asYoloEnv) == "1" {
		os.Unsetenv(asYoloEnv)
		os.Exit(runAsYolo(os.Args[1:]))
	}
	switch os.Getenv(ioTestModeEnv) {
	case "child":
		runIOPriorityChild()
		os.Exit(0)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	// THE CALLER'S YOLO_* DIALS ARE NOT THIS SUITE'S (testsupport.ScrubYoloEnv): a shell under
	// `yolo host` or in a jail exports hatches such as YOLO_ALLOW_MISSING_PROGRAMS that turn an
	// expected refusal into a launch. Past every child dispatch above, which reads its mode variables.
	testsupport.ScrubYoloEnv()
	// A CHILD OF THIS BINARY LEAVES NOTHING BEHIND (testtmp.Isolate): a helper re-exec runs this
	// TestMain again and exits, execs away or is killed before its own removals run, so its
	// directories and t.TempDirs go in a private TMPDIR this process removes.
	releaseTmp := testtmp.Isolate()
	// ONE LINK OF yolo FOR THE PACKAGE (testutil.ShareYoloBinary): hundreds of launcher tests
	// and subtests ask for it, and a link is not cached.
	releaseBin := testutil.ShareYoloBinary()
	code := runWithStandInImageBins(m)
	releaseBin()
	os.Exit(releaseTmp(code))
}

// TestThisPackageIsolatesTMPDIR fails if TestMain stops calling testtmp.Isolate, which would
// put every helper child's leftovers back in the machine's TMPDIR.
func TestThisPackageIsolatesTMPDIR(t *testing.T) { testtmp.RequireIsolated(t) }
