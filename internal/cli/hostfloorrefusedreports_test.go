package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// hostfloorrefusedreports_test.go pins what the reporting verbs say about a declared program the
// floor cannot hold on this machine (an installer agent on a Mac whose sandbox account is not set
// up). `yolo host` refuses to launch it rather than run a copy on the PATH
// (host-notch-readiness.md HNR-D2), so `yolo host apply` and `yolo check-deps` must say that, and
// must not answer for it with whatever copy a PATH holds.

// refusedOnThisMac is nativeFloorFixture on a Mac before `yolo macos-setup`, with a hand-installed
// nativecli first on PATH: the copy the old fallback ran, and the one a PATH probe would find.
func refusedOnThisMac(t *testing.T) (hand string) {
	t.Helper()
	nativeFloorFixture(t)
	withMac(t, macSetup{terminal: true})
	withFloorPlatform(t, "darwin")
	return filepath.Join(stubBins(t, "nativecli"), "nativecli")
}

// `yolo host apply`'s floor stage names the refusal and the way out, and never says the PATH copy
// runs. Drop the OutsideTheFloor branch in applyHostFloor and the PATH line comes back.
func TestHostApplySaysARefusedProgramIsRefusedNotRunFromThePATH(t *testing.T) {
	refusedOnThisMac(t)
	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, false, nil)
	got := out.String() + errw.String()
	if want := "nativecli: no floor entry — "; !strings.Contains(got, want) {
		t.Fatalf("the floor stage lacks %q:\n%s", want, got)
	}
	if want := "`yolo host` refuses to launch until the floor holds it, or until `host_floor` leaves pack " +
		"nativepack out"; !strings.Contains(got, want) {
		t.Errorf("the floor stage lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "`yolo host -- nativecli` runs the one on your PATH") {
		t.Errorf("the floor stage says a refused program runs from the PATH:\n%s", got)
	}
}

// `yolo check-deps` answers for a refused program from the floor, never from the PATH copy, marks it
// as the problem it is and exits 1. Keep it out of floorDeliveredBins and the PATH copy passes.
func TestCheckDepsFailsOnAProgramTheLaunchRefusesWhateverThePATHHolds(t *testing.T) {
	hand := refusedOnThisMac(t)
	rc, got := runCheckDepsT(t)
	if rc != 1 {
		t.Errorf("rc = %d, want 1 for a program every `yolo host` launch refuses:\n%s", rc, got)
	}
	for _, want := range []string{
		"✗ nativecli",
		"so `yolo host` refuses to launch; to run your own copy, leave pack nativepack out with " +
			"`\"host_floor\": {\"nativepack\": false}` in the user config",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check-deps lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, hand) {
		t.Errorf("check-deps answered for nativecli with the PATH copy %s, which `yolo host` never runs:\n%s",
			hand, got)
	}
}
