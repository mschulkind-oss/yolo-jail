package launchservice

// pin_test.go pins docs/design/pinned-host-helpers.md §3.3-3.4: a launch-owned service of the
// bundled `yolo` runs from the pin its launch resolved, never from os.Executable(), and a
// supervisor that finds the pin replaced gives up rather than start another build.

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
)

// pinnedCopy copies this test binary to <bundle>/bin/<os>-<arch>/yolo and pins it, so a service
// execd from the pin is a test binary that acts as a host half (TestMain), whatever its argv.
func pinnedCopy(t *testing.T) *helperpin.Pin {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(dir, "yolo"), os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	pin, err := helperpin.Resolve(filepath.Dir(filepath.Dir(dir)))
	if err != nil {
		t.Fatal(err)
	}
	return &pin
}

func TestAServiceIsExecdFromThePin(t *testing.T) {
	pin := pinnedCopy(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	plan.Pin = pin
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	if r.Argv[0] != pin.Path {
		t.Errorf("argv[0] = %q, want the pin %q", r.Argv[0], pin.Path)
	}
	if got, err := os.Readlink("/proc/" + strconv.Itoa(r.PID()) + "/exe"); err == nil && got != pin.Path {
		t.Errorf("the service runs %q, want the pin %q", got, pin.Path)
	}
	if !dials(addr) {
		t.Error("the service started from the pin is not serving")
	}
}

func TestStartRefusesWithoutAPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plan, addr := testPlan(t)
	_, err := Start(plan, map[string]string{"ADDR": addr})
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a yolo service with no pin started, or refused without naming the pin: %v", err)
	}
	// A pin whose file is gone refuses too, naming the path and the way out.
	pin := pinnedCopy(t)
	plan, addr = testPlan(t)
	plan.Pin = pin
	if err := os.Remove(pin.Path); err != nil {
		t.Fatal(err)
	}
	_, err = Start(plan, map[string]string{"ADDR": addr})
	if err == nil || !strings.Contains(err.Error(), pin.Path) || !strings.Contains(err.Error(), "launch") {
		t.Fatalf("a missing pin must refuse naming %s and the relaunch: %v", pin.Path, err)
	}
}

func TestAPlanThatNamesItsOwnProgramNeedsNoPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	exe, _ := os.Executable()
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	plan.Cmd = []string{exe, "x"}
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	if r.Argv[0] != exe {
		t.Errorf("argv[0] = %q, want %q", r.Argv[0], exe)
	}
}

func TestSuperviseGivesUpWhenThePinChanged(t *testing.T) {
	stubRestartSleep(t, nil)
	pin := pinnedCopy(t)
	t.Setenv(helperEnv, "doorway:ok")
	plan, addr := doorwayPlan(t)
	plan.Restart = "always"
	plan.Pin = pin
	plan.StopRemedy = "`yolo stop` once its sessions are done, then launch"
	r, err := Start(plan, map[string]string{"UPSTREAM": "x"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	out := &lockedBuilder{}
	r.Supervise("claude", out, "yolo host: ")
	// An upgrade replaces the file: its mtime no longer matches the pin.
	later := time.Unix(0, pin.MtimeNanos).Add(time.Hour)
	if err := os.Chtimes(pin.Path, later, later); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(r.PID(), syscall.SIGKILL)
	waitGone(t, r, 10*time.Second)
	for _, want := range []string{`"door"`, "killed by SIGKILL", "not restarting it",
		"the yolo this jail was launched with is no longer at " + pin.Path,
		"It stays down until the jail is launched again: `yolo stop` once its sessions are done, then launch"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the give-up line must say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "is back") || dials(addr) {
		t.Errorf("a service whose pin changed was restarted:\n%s", out.String())
	}
}

func TestAServiceIsHandedItsJailLabel(t *testing.T) {
	pin := pinnedCopy(t)
	t.Setenv(helperEnv, "serve-jail")
	plan, addr := testPlan(t)
	plan.Pin, plan.Jail = pin, "yolo-jail-abc"
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	b, _ := os.ReadFile(r.Log)
	if !strings.Contains(string(b), `helper-jail="yolo-jail-abc"`) {
		t.Errorf("the service was not handed %s:\n%s", helperpin.EnvJail, b)
	}
}
