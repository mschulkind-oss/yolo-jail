package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloorapply_test.go pins `yolo host apply`'s floor stage at its call sites: the verb
// provisions the floor with the launch's own provisioning and removes what no selected pack
// delivers any more; the launch gate's apply touches the floor not at all; and the dependency
// pre-flight answers a floor program from the floor, never from PATH.

// TestHostApplyAssertProvisionsTheFloorAndRemovesADeselectedEntry: the dry run says what it
// would install and installs nothing; --assert installs it; dropping the pack from `packs` and
// asserting again removes the entry — launcher, record and install directory.
func TestHostApplyAssertProvisionsTheFloorAndRemovesADeselectedEntry(t *testing.T) {
	dist, _ := floorHostFixture(t, "")
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "floorcli")

	rc, report := applyWith(t, false, nil)
	if rc != 0 || !strings.Contains(report, "floorcli: would install") {
		t.Fatalf("dry run rc=%d, want it to say it would install floorcli:\n%s", rc, report)
	}
	if _, err := os.Stat(launcher); err == nil || len(dist.NpmCalls("install")) != 0 {
		t.Fatalf("the dry run installed something")
	}

	rc, report = applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("--assert rc=%d:\n%s", rc, report)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatalf("--assert did not provision floorcli (%v):\n%s", err, report)
	}
	if !strings.Contains(report, "floorcli 1.0.0, installed") {
		t.Errorf("the report does not say what it installed:\n%s", report)
	}

	writeFile(t, paths.UserConfigPath(), `{"packs":[]}`)
	rc, report = applyWith(t, true, nil)
	if rc != 0 || !strings.Contains(report, "floorcli: removed (no selected pack declares it any more)") {
		t.Fatalf("--assert after deselection rc=%d:\n%s", rc, report)
	}
	for _, gone := range []string{launcher, filepath.Join(paths.HostFloorDir(), "programs", "floorcli")} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s survived the deselection", gone)
		}
	}
}

// TestTheLaunchGatesApplyNeverTouchesTheFloor: the gate's writing apply is applyHostSurveyed
// without the verb's floor stage, so a launch neither installs every missing agent nor removes
// one — it installs only what it starts (resolveHostLaunchTarget).
func TestTheLaunchGatesApplyNeverTouchesTheFloor(t *testing.T) {
	dist, _ := floorHostFixture(t, "")
	var out, errw bytes.Buffer
	if rc := applyHostSurveyed(&out, &errw, false, true, nil, &hostApplySurvey{}); rc != 0 {
		t.Fatalf("the gate's apply rc=%d:\n%s%s", rc, out.String(), errw.String())
	}
	if _, err := os.Stat(paths.HostFloorDir()); err == nil || len(dist.NpmCalls("install")) != 0 {
		t.Errorf("the launch gate's apply provisioned the floor:\n%s", out.String())
	}
}

// TestHostApplyAnswersAFloorProgramFromTheFloorNotFromPATH: floorcli is on no PATH, and the floor
// has not installed it yet. Probed through PATH it would be a MISSING declared dependency — a
// blocker the --assert refuses over, at a prompt it cannot answer here. Answered by the floor, it
// is the floor's to install, and the --assert completes (and installs it).
func TestHostApplyAnswersAFloorProgramFromTheFloorNotFromPATH(t *testing.T) {
	floorHostFixture(t, "")
	t.Setenv(paths.VerboseEnv, "1")
	rc, report := applyWith(t, false, nil)
	if rc != 0 || strings.Contains(report, "MISSING") {
		t.Fatalf("dry run rc=%d, and floorcli must not read as a missing dependency:\n%s", rc, report)
	}
	if !strings.Contains(report, "yolo's floor installs it") {
		t.Errorf("the dependency line does not say the floor supplies floorcli:\n%s", report)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("--assert refused over a program the floor delivers (rc=%d):\n%s", rc, report)
	}
}

// TestCheckDepsAnswersAFloorProgramByItsFloorEntry: `yolo check-deps` asks the same question the
// apply's pre-flight does, and a program the floor delivers is not a missing host dependency
// whether or not any PATH has it.
func TestCheckDepsAnswersAFloorProgramByItsFloorEntry(t *testing.T) {
	floorHostFixture(t, "")
	var out, errw bytes.Buffer
	rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	if rc != 0 || !strings.Contains(out.String(), "floorcli") ||
		!strings.Contains(out.String(), "yolo's floor installs it") {
		t.Fatalf("check-deps rc=%d, want floorcli answered by the floor:\n%s%s", rc, out.String(), errw.String())
	}
}
