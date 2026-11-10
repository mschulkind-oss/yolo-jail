package cli

// hostpin_test.go pins docs/design/pinned-host-helpers.md for `yolo host`: a launch pins its helpers'
// yolo only when it will start one, refuses without it before spawning anything, and hands every
// plan it starts the pin, its workspace as the jail and the host's stop step; the private broker
// and the security adapter run from the pin.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// writeYoloBundle makes <root>/bin/<os>-<arch>/yolo hold body and returns the root and its pin.
func writeYoloBundle(t *testing.T, body string) (string, *helperpin.Pin) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := helperpin.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, &pin
}

// testYoloStandInRoot is the bundle installYoloStandIn made for the whole package.
var testYoloStandInRoot string

// installYoloStandIn points every host launch of this package at a bundle whose yolo runs this test
// binary as `yolo` (testAsYoloArg), and returns the removal of it.
func installYoloStandIn() func() {
	exe, err := os.Executable()
	if err != nil {
		return func() {}
	}
	root, err := os.MkdirTemp("", "yolo-standin-")
	if err != nil {
		return func() {}
	}
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = resolved
	}
	dir := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "yolo"), []byte("#!/bin/sh\nexec '"+exe+"' "+testAsYoloArg+" \"$@\"\n"), 0o755)
	testYoloStandInRoot = root
	hostBundleRoot = func() (string, error) { return root, nil }
	return func() { _ = os.RemoveAll(root) }
}

// bundleSeam points hostBundleRoot at root (or at the error) for one test.
func bundleSeam(t *testing.T, root string, err error) (resolved *int) {
	t.Helper()
	n := 0
	orig := hostBundleRoot
	hostBundleRoot = func() (string, error) { n++; return root, err }
	t.Cleanup(func() { hostBundleRoot = orig })
	return &n
}

// The private broker's argv[0] is the pin. Fails if the broker goes back to execx.SelfExecArgv.
func TestTheHostViewBrokerArgvStartsWithThePin(t *testing.T) {
	_, pin := writeYoloBundle(t, "#!/bin/sh\n")
	argv, err := hostViewBrokerArgv(pin, "/tmp/s.sock", 7)
	if err != nil || argv[0] != pin.Path {
		t.Fatalf("argv = %v, %v; want argv[0] = the pin %s", argv, err, pin.Path)
	}
	if err := os.WriteFile(pin.Path, []byte("#!/bin/sh\n# replaced\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := hostViewBrokerArgv(pin, "/tmp/s.sock", 7); err == nil || !strings.Contains(err.Error(), pin.Path) {
		t.Errorf("a replaced pin must refuse naming %s; got %v", pin.Path, err)
	}
	if _, err := hostViewBrokerArgv(nil, "/tmp/s.sock", 7); err == nil {
		t.Error("no pin must refuse, not fall back to the running binary")
	}
}

// The broker the launch spawns is the pinned file and is told the launch it serves.
func TestTheHostViewBrokerIsSpawnedFromThePinWithItsJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	record := filepath.Join(t.TempDir(), "record")
	_, pin := writeYoloBundle(t, "#!/bin/sh\n{ echo \"argv0=$0\"; echo \"jail=$YOLO_HELPER_JAIL\"; } > '"+record+"'\nexit 1\n")
	if _, err := startHostViewBroker(pin, "/work/ws", os.Stderr); err == nil {
		t.Fatal("the recording yolo exits at once, so the start must report it")
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the pinned yolo never ran: %v", err)
	}
	for _, want := range []string{"argv0=" + pin.Path, "jail=/work/ws"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("record lacks %q:\n%s", want, data)
		}
	}
}

// The Darwin security adapter shim runs the pinned yolo, not whatever os.Executable() names.
func TestTheHostClaudeSecurityAdapterRunsThePinnedYolo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, pin := writeYoloBundle(t, "#!/bin/sh\n")
	dir, err := writeHostClaudeSecurityAdapter(pin, filepath.Join(home, "managed"), "/usr/bin")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "security"))
	if err != nil || !strings.Contains(string(body), pin.Path) {
		t.Fatalf("the adapter shim does not run the pin %s: %q, %v", pin.Path, body, err)
	}
	if _, err := writeHostClaudeSecurityAdapter(nil, filepath.Join(home, "managed"), "/usr/bin"); err == nil {
		t.Error("no pin must refuse the adapter")
	}
}

// The composition resolves its pin from the bundle root once, and not until asked.
func TestAHostCompositionResolvesItsPinOnceAndOnlyWhenAsked(t *testing.T) {
	root, want := writeYoloBundle(t, "#!/bin/sh\n")
	n := bundleSeam(t, root, nil)
	c := &hostComposition{workspace: "/work/ws"}
	if *n != 0 {
		t.Fatalf("a composition resolved a pin before anyone asked (%d)", *n)
	}
	for i := 0; i < 2; i++ {
		got, err := c.helperPin()
		if err != nil || got.Path != want.Path {
			t.Fatalf("pin = %v, %v; want %s", got, err, want.Path)
		}
	}
	if *n != 1 {
		t.Errorf("the bundle root was resolved %d times, want once", *n)
	}
	if c.helperJail() != "/work/ws" {
		t.Errorf("jail = %q, want the workspace", c.helperJail())
	}
}

// A composition that cannot resolve a pin says so with the reinstall step and keeps saying it.
func TestAHostCompositionWithoutABundleHasNoPin(t *testing.T) {
	bundleSeam(t, "", errors.New("no bundle. "+reporoot.ReinstallSteps))
	c := &hostComposition{}
	for i := 0; i < 2; i++ {
		if pin, err := c.helperPin(); pin != nil || err == nil || !strings.Contains(err.Error(), "Reinstall yolo") {
			t.Fatalf("pin = %v, err = %v; want a refusal naming the reinstall step", pin, err)
		}
	}
}

// The OpenAI prelaunch carries the launch's pin and jail, so the credential daemon it starts runs from
// them. Fails if prelaunch() stops handing them over.
func TestThePrelaunchCarriesThePinAndJail(t *testing.T) {
	root, want := writeYoloBundle(t, "#!/bin/sh\n")
	bundleSeam(t, root, nil)
	c := &hostComposition{agent: "codex", workspace: "/work/ws"}
	p := c.prelaunch(false)
	if p.Pin == nil || p.Jail != "/work/ws" {
		t.Fatalf("prelaunch = %+v, want the pin source and the jail", p)
	}
	if got, err := p.Pin(); err != nil || got.Path != want.Path {
		t.Errorf("the prelaunch's pin = %v, %v; want %s", got, err, want.Path)
	}
}

// testHostPin is a pin at a fixture bundle's yolo, for a test that needs some pin and not a real one.
func testHostPin(t *testing.T) *helperpin.Pin {
	t.Helper()
	_, pin := writeYoloBundle(t, "#!/bin/sh\n")
	return pin
}

// A macos-user launch's backend stages and starts from the pin its arm carries, and from nothing
// else: with no pin SelfExe answers "", never os.Executable(). Fails if macosUserSessionDeps stops
// wiring SelfExe from the arm (it would answer the test binary).
func TestTheMacosUserBackendStagesTheArmsPin(t *testing.T) {
	arm := run.NewMacosUserArm()
	deps := macosUserSessionDeps(arm, nil, nil)
	if got := deps.SelfExe(); got != "" {
		t.Fatalf("a launch with no pin staged %q, want nothing (no os.Executable fallback)", got)
	}
	_, pin := writeYoloBundle(t, "pinned")
	arm.SetHelperPin(pin)
	if got := deps.SelfExe(); got != pin.Path {
		t.Errorf("SelfExe = %q, want the arm's pin %q", got, pin.Path)
	}
	if exe, err := os.Executable(); err == nil && deps.SelfExe() == exe {
		t.Error("SelfExe is the running binary")
	}
}

// A host launch that starts a launch-owned service hands its plan the pin, its workspace as the jail
// and the host's stop step. Deleting the stamp (pinPlan) from the start path fails this.
func TestHostStartsItsServicesFromThePin(t *testing.T) {
	officialWorkerHome(t)
	dir := workerStandIn(t)
	l := runWorkerLaunch(t, dir, "worker", "exit 0", nil)
	if l.rc != 0 || len(l.started) != 1 {
		t.Fatalf("rc = %d, started %d\n%s", l.rc, len(l.started), l.errs)
	}
	p := l.started[0].Plan
	if p.Pin == nil || !strings.Contains(p.Pin.Path, filepath.Join("bin", runtime.GOOS+"-"+runtime.GOARCH, "yolo")) {
		t.Errorf("the plan's pin = %v, want the bundle's yolo", p.Pin)
	}
	if p.Jail == "" || !filepath.IsAbs(p.Jail) {
		t.Errorf("the plan's jail = %q, want the launch's workspace", p.Jail)
	}
	if p.StopRemedy != hostStopRemedy {
		t.Errorf("the plan's stop step = %q, want %q", p.StopRemedy, hostStopRemedy)
	}
}

// A host launch that would start a service but cannot pin refuses before spawning anything, naming
// the reinstall step. Deleting the helperPin call before the services block fails this.
func TestHostRefusesToStartAServiceWithoutAPin(t *testing.T) {
	officialWorkerHome(t)
	dir := workerStandIn(t)
	bundleSeam(t, "", errors.New("no bundle. "+reporoot.ReinstallSteps))
	l := runWorkerLaunch(t, dir, "worker", "exit 0", nil)
	if l.rc == 0 || !strings.Contains(l.errs, "Reinstall yolo") || !strings.Contains(l.errs, "refusing to launch") {
		t.Fatalf("rc = %d; want a refusal naming the reinstall step\n%s", l.rc, l.errs)
	}
	if len(l.started) != 0 || l.execed {
		t.Errorf("started %d services and exec'd %v after refusing", len(l.started), l.execed)
	}
	if _, err := os.Stat(filepath.Join(dir, "pid.worker")); err == nil {
		t.Error("the worker ran")
	}
}

// A binary-only host launch, which starts no helper, resolves no pin and works with no bundle.
func TestHostLaunchThatStartsNoHelperResolvesNoPin(t *testing.T) {
	n := bundleSeam(t, "", errors.New("no bundle"))
	rc, _, errs := hostGateRunIn(t, `{"packs": []}`, nil, nil, "tool", nil)
	if rc != 0 {
		t.Fatalf("a launch with no helper refused without a bundle (rc=%d):\n%s", rc, errs)
	}
	if *n != 0 {
		t.Errorf("a launch that starts no helper resolved its pin %d times", *n)
	}
}
