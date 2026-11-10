package macosuser

// pinnedselfexe_test.go pins docs/design/pinned-host-helpers.md section 3.3 for macos-user: a
// launch's guest `yolo` is copied from the file its launch pinned, never from os.Executable(), and
// a pin that is gone stages nothing, so the plan invariant refuses the launch.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
)

// pinFixture is a bundle whose host yolo holds content, and its pin.
func pinFixture(t *testing.T, content string) *helperpin.Pin {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := helperpin.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	return &pin
}

// guestYoloSource is the file the plan copies into the session's guest yolo.
func guestYoloSource(t *testing.T, plan RunPlan) string {
	t.Helper()
	want := filepath.Join(plan.SessionGuestBinDir+".new", "yolo")
	for _, cmd := range plan.StageCommands {
		if len(cmd) == 4 && cmd[0] == cpBin && cmd[3] == want {
			return cmd[2]
		}
	}
	t.Fatalf("the plan copies no guest yolo to %s: %v", want, plan.StageCommands)
	return ""
}

// The guest yolo is the pin's file: its CONTENTS are the fixture's, not this test binary's.
func TestTheStagedGuestYoloIsThePinsFile(t *testing.T) {
	pin := pinFixture(t, "pinned-build-bytes")
	deps := mockDeps(nil)
	deps.SelfExe = PinnedSelfExe(func() *helperpin.Pin { return pin })
	plan := buildPlan(deps, newOpts("/Users/Shared/yolo/proj"), mockDarwin())
	src := guestYoloSource(t, plan)
	got, err := os.ReadFile(src)
	if err != nil || string(got) != "pinned-build-bytes" {
		t.Fatalf("the guest yolo is staged from %s holding %q (%v), want the pin's file", src, got, err)
	}
	if src != pin.Path {
		t.Errorf("staged from %s, want the pin %s", src, pin.Path)
	}
}

// A pin that is gone or replaced answers "": nothing is staged from another build, and the plan
// invariant refuses.
func TestAPinnedSelfExeIsEmptyWithoutAUsablePin(t *testing.T) {
	pin := pinFixture(t, "one")
	if got := PinnedSelfExe(func() *helperpin.Pin { return pin })(); got != pin.Path {
		t.Fatalf("an unchanged pin answered %q, want %q", got, pin.Path)
	}
	if err := os.WriteFile(pin.Path, []byte("a different build"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() string{
		"a replaced pin": PinnedSelfExe(func() *helperpin.Pin { return pin }),
		"no pin":         PinnedSelfExe(func() *helperpin.Pin { return nil }),
		"no source":      PinnedSelfExe(nil),
	} {
		if got := fn(); got != "" {
			t.Errorf("%s answered %q, want \"\" (never the running binary)", name, got)
		}
	}
	deps := mockDeps(nil)
	deps.SelfExe = PinnedSelfExe(func() *helperpin.Pin { return pin })
	plan := buildPlan(deps, newOpts("/Users/Shared/yolo/proj"), mockDarwin())
	var found bool
	for _, p := range PlanInvariants(plan) {
		found = found || strings.Contains(p, "no source yolo binary")
	}
	if !found {
		t.Error("a launch whose pin was replaced passed the plan invariants")
	}
}
