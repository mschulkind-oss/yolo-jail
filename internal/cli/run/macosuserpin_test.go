package run

// macosuserpin_test.go pins docs/design/pinned-host-helpers.md section 3.3 and 3.4 for the macos-user
// arm: every launch-owned service and doorway it starts carries the launch's pin, the jail label
// its helpers log, and the stop step a give-up names; and the backend stages the pin's yolo.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func pinnedMacosUserOptions(t *testing.T) (*Options, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	o := &Options{Stdout: &buf, Stderr: &buf, Workspace: t.TempDir()}
	fillDefaults(o)
	o.runtime = "macos-user"
	pin := shellPin(t)
	o.helperPin = &pin
	return o, &buf
}

func recordedMacosUserPlans(t *testing.T) *[]*launchservice.Plan {
	t.Helper()
	var plans []*launchservice.Plan
	origS, origD := startMacosUserService, startMacosUserDoorway
	start := func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		plans = append(plans, p)
		return &supervisedFake{}, "/log", nil
	}
	startMacosUserService, startMacosUserDoorway = start, start
	t.Cleanup(func() { startMacosUserService, startMacosUserDoorway = origS, origD })
	return &plans
}

func assertPinned(t *testing.T, o *Options, plans []*launchservice.Plan, want int) {
	t.Helper()
	if len(plans) != want {
		t.Fatalf("started %d plans, want %d", len(plans), want)
	}
	for _, p := range plans {
		if p.Pin == nil || *p.Pin != *o.helperPin {
			t.Errorf("the %q plan carries pin %v, want the launch's %v", p.Service, p.Pin, *o.helperPin)
		}
		if p.Jail != o.jailName() || p.Jail == "" {
			t.Errorf("the %q plan's jail = %q, want %q", p.Service, p.Jail, o.jailName())
		}
		if !strings.Contains(p.StopRemedy, "yolo stop") {
			t.Errorf("the %q plan's stop step = %q, want macos-user's (`yolo stop` from this workspace)", p.Service, p.StopRemedy)
		}
	}
}

// Deleting the stamp from startMacosUserServices fails this.
func TestMacosUserServicesAreStartedFromThePin(t *testing.T) {
	o, _ := pinnedMacosUserOptions(t)
	plans := recordedMacosUserPlans(t)
	stop, _, err := o.startMacosUserServices([]serviceStart{
		{plan: &launchservice.Plan{Declared: launchservice.Declared{Service: "a", Pack: "p"}}},
		{plan: &launchservice.Plan{Declared: launchservice.Declared{Service: "b", Pack: "p"}}, worker: true},
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	assertPinned(t, o, *plans, 2)
}

// Deleting the stamp from startMacosUserDoorways fails this.
func TestMacosUserDoorwaysAreStartedFromThePin(t *testing.T) {
	o, _ := pinnedMacosUserOptions(t)
	plans := recordedMacosUserPlans(t)
	stop, _, err := o.startMacosUserDoorways([]*launchservice.Plan{
		{Declared: launchservice.Declared{Service: "door", Pack: "p"}}}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	assertPinned(t, o, *plans, 1)
}

// The pin Run resolves reaches the arm, whose pin the front door's backend deps stage and start from.
// Deleting the SetHelperPin call from resolveHelperPin fails this.
func TestResolvingTheLaunchPinHandsItToTheMacosUserArm(t *testing.T) {
	arm := NewMacosUserArm()
	o := &Options{MacosUserArm: arm}
	if arm.HelperPin() != nil {
		t.Fatal("a fresh arm carries a pin")
	}
	if err := o.resolveHelperPin(pinFixtureBundle(t, []byte("yolo"))); err != nil {
		t.Fatal(err)
	}
	if got := arm.HelperPin(); got == nil || o.helperPin == nil || *got != *o.helperPin {
		t.Errorf("the arm's pin = %v, want the launch's %v", got, o.helperPin)
	}
}

// PH-D1 at the launch's pre-flight: a bundle whose stamp names another commit than this launcher
// is refused, with both builds and the reinstall step, and no pin is stored. Deleting the build
// check from helperpin fails this.
func TestResolvingTheLaunchPinRefusesABundleOfAnotherBuild(t *testing.T) {
	root := pinFixtureBundle(t, []byte("yolo"))
	if err := os.WriteFile(filepath.Join(root, "yolo-build-stamp.json"), []byte(`{"commit":"bbbbbbbbbbbb"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := version.GitCommit
	version.GitCommit = "aaaaaaaaaaaa"
	t.Cleanup(func() { version.GitCommit = orig })
	o := &Options{}
	err := o.resolveHelperPin(root)
	if err == nil || o.helperPin != nil {
		t.Fatalf("err = %v, pin = %v; want a refusal and no pin", err, o.helperPin)
	}
	for _, want := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", reporoot.ReinstallSteps} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	// The matching stamp is accepted.
	if err := os.WriteFile(filepath.Join(root, "yolo-build-stamp.json"), []byte(`{"commit":"aaaaaaaaaaaa"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.resolveHelperPin(root); err != nil {
		t.Fatalf("a bundle of this build was refused: %v", err)
	}
}
