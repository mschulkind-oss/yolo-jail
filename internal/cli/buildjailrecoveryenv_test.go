package cli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
)

// THE OUTER LAUNCH'S RECOVERY HATCHES DO NOT REACH A BUILD JAIL. A launch run with
// YOLO_USE_CACHED_GOOD that also built a patched extension handed the hatch to the build jail's own
// launch, which refused ("no host capture store to deliver from"). Red if runCaptureChild stops
// stripping patchrecovery.Scoped from the child's environment.
func TestTheBuildJailChildIsNotHandedTheOuterLaunchsRecoveryHatches(t *testing.T) {
	for _, env := range patchrecovery.Scoped {
		t.Setenv(env, "1")
	}
	t.Setenv("YOLO_KEEP_ME", "kept")
	prev := forkBuildChildCommand
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", "env >&3"), nil
	}
	t.Cleanup(func() { forkBuildChildCommand = prev })
	var jailOut, errw bytes.Buffer
	rc, _ := runForkBuildChild(context.Background(), time.Hour, "/s", forkBuild{},
		captureStreams{out: &bytes.Buffer{}, errw: &errw, jailOut: &jailOut, jailErr: &bytes.Buffer{}}, false)
	if rc != 0 {
		t.Fatalf("the child = %d: %s", rc, errw.String())
	}
	got := jailOut.String()
	for _, env := range patchrecovery.Scoped {
		if strings.Contains(got, env+"=") {
			t.Errorf("the build jail child inherited %s:\n%s", env, got)
		}
	}
	if !strings.Contains(got, "YOLO_KEEP_ME=kept") {
		t.Errorf("the child lost its other environment:\n%s", got)
	}
}

// The in-process arm (a direct `yolo capture`'s installer jail) reads its hatches through the
// options' Getenv: red if runCaptureJail stops hiding them there. The outer process's own reads
// (capturehost's skipsPatches) are untouched.
func TestACaptureJailsLaunchReadsNoRecoveryHatch(t *testing.T) {
	for _, env := range patchrecovery.Scoped {
		t.Setenv(env, "1")
	}
	t.Setenv("YOLO_KEEP_ME", "kept")
	var seen func(string) string
	withFakeCaptureJail(t, func(o run.Options) int { seen = o.Getenv; return 0 })
	var out, errw bytes.Buffer
	runCaptureJail(t.TempDir(), "probetool", []string{"true"}, nil, captureStreams{out: &out, errw: &errw}, false)
	if seen == nil {
		t.Fatal("the capture jail's launch was not reached with a Getenv")
	}
	for _, env := range patchrecovery.Scoped {
		if v := seen(env); v != "" {
			t.Errorf("the capture jail's launch reads %s=%q", env, v)
		}
	}
	if seen("YOLO_KEEP_ME") != "kept" {
		t.Error("the capture jail's launch lost its other environment")
	}
}
