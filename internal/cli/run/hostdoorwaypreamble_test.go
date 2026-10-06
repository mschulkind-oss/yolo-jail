package run

// hostdoorwaypreamble_test.go pins the `yolo host` half of HD-D5 (3) (docs/design/
// host-daemon-ownership.md): a launch that opens a doorway to a host-wide daemon which predates
// the connection preamble refuses before the doorway opens, naming the command that restarts THAT
// daemon, as a jail launch refuses (launchcheckrefusal_test.go). HostDoorways.Start is the call
// site; deleting its check fails the first test, and the second is its control.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// errDoorwayStub is what the control's doorway start answers, ending the launch where the doorway
// would open.
var errDoorwayStub = errors.New("the doorway was started")

// hostDoorwayStartAgainst serves the fixture daemon, runs prep, and starts a `yolo host` doorway to
// it, reporting Start's error and whether the doorway itself was started.
func hostDoorwayStartAgainst(t *testing.T, prep ...func(*testing.T)) (err error, doorwayStarted bool) {
	t.Helper()
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, answersTheCheck)
	for _, p := range prep {
		p(t)
	}
	pack := writeRealLoopholePack(t, "yjtest-doorway-pack", launchCheckFixtureName, askedEveryLaunchManifest)
	packs := []*packload.Pack{pack}
	d := &HostDoorways{
		plans: []*launchservice.Plan{launchservice.PlanAt(launchservice.Declared{
			Service: launchCheckFixtureName, Pack: "yjtest-doorway-pack", Cmd: []string{"yolo", "doorway"},
		}, "127.0.0.1:1", "YOLO_TEST_DOOR_TOKEN", "token", nil)},
		set:   loopholes.NewSet(loopholes.DiscoverOptions{PackModules: packLoopholeModules(packs)}),
		packs: packs,
	}
	var buf bytes.Buffer
	_, stop, _, err := d.Start(newConfig(), t.TempDir(), "pi", &buf,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			doorwayStarted = true
			return nil, errDoorwayStub
		})
	if stop != nil {
		stop()
	}
	return err, doorwayStarted
}

func TestAHostDoorwayToADaemonThatPredatesThePreambleRefusesTheLaunch(t *testing.T) {
	err, started := hostDoorwayStartAgainst(t, predatesThePreamble)
	if started {
		t.Error("the doorway opened to a daemon that predates the preamble")
	}
	if err == nil || errors.Is(err, errDoorwayStub) {
		t.Fatalf("Start returned %v, want the refusal", err)
	}
	for _, want := range []string{"'" + launchCheckFixtureName + "' predates this yolo",
		"does not speak the connection preamble", broker.CycleCommand(launchCheckFixtureName)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	assertTheDaemonWasNotRestarted(t)
}

// TestAHostDoorwayToADaemonThatSpeaksThePreambleOpens is its control: the same start reaches the
// doorway.
func TestAHostDoorwayToADaemonThatSpeaksThePreambleOpens(t *testing.T) {
	err, started := hostDoorwayStartAgainst(t)
	if !started || !errors.Is(err, errDoorwayStub) {
		t.Errorf("the doorway did not open (started %v, err %v), so its refusal twin proves nothing",
			started, err)
	}
}
