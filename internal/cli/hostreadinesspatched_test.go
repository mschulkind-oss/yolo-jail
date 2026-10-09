package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

func TestHostPatchFatalReadinessMissingBypassDoesNotWaiveFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "1")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	selection := selectConfiguredHostPacks()
	if selection.loadErr != nil {
		t.Fatal(selection.loadErr)
	}
	programs := floorPrograms(selection.packs)
	floor := newHostFloor(io.Discard, programs)
	programIndex := -1
	for i := range programs {
		if programs[i].Bin() == "tool" {
			programIndex = i
			break
		}
	}
	if programIndex < 0 {
		t.Fatal("fixture selection has no patched tool program")
	}
	if _, _, err := floor.Ensure(context.Background(), programs[programIndex]); err != nil {
		t.Fatalf("install compatible floor copy: %v", err)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	if _, _, err := floor.Ensure(context.Background(), programs[programIndex]); err == nil {
		t.Fatal("the actual foreground floor advance did not retain its patch failure")
	}
	if record := floor.Status(programs[programIndex]).Record; record == nil {
		t.Fatal("fixture did not retain the usable old floor program")
	}
	var errw strings.Builder
	_, rc := hostReadinessAct(selection.packs, []string{"other-command"}, &errw, &run.ActInterrupt{})
	if rc == 0 {
		t.Fatalf("missing-program bypass waived the typed patch-application refusal:\n%s", errw.String())
	}
	// PF-D83: an intact build is here, so the refusal names the patch failure's own bypass, the one
	// that runs it, and says the missing-program hatch does not leave the program out.
	if !strings.Contains(errw.String(), "0001-ten.patch") ||
		!strings.Contains(errw.String(), "YOLO_ALLOW_MISSING_PROGRAMS does not leave it out") ||
		!strings.Contains(errw.String(), "YOLO_ALLOW_PATCH_FAILURES=1 yolo host -- other-command") {
		t.Fatalf("readiness did not retain the concrete patch failure and refusal:\n%s", errw.String())
	}
}

// PF-D83's OTHER HALF, AT THE HOST: a patch failure that leaves nothing of the program to run — no
// intact build of the series, no installed copy — is a missing program like any other, which the
// missing-program hatch waives by launching without it, as a jail launch does; without the hatch the
// launch refuses, its error block offering no bypass that cannot run. Red with the readiness act
// refusing every patch failure whatever ran, or with the block offering YOLO_ALLOW_PATCH_FAILURES.
func TestHostPatchFailureWithNothingToRunIsAMissingProgram(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen", 11: "eleven"})
	selection := selectConfiguredHostPacks()
	if selection.loadErr != nil {
		t.Fatal(selection.loadErr)
	}
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
	var refused strings.Builder
	if _, rc := hostReadinessAct(selection.packs, []string{"other-command"}, &refused, &run.ActInterrupt{}); rc == 0 {
		t.Fatalf("a patch failure with nothing to run started the launch without the hatch:\n%s", refused.String())
	}
	if !strings.Contains(refused.String(), "0001-ten.patch") ||
		!strings.Contains(refused.String(), "Bypass: none on this machine") ||
		strings.Contains(refused.String(), "Bypass: YOLO_ALLOW_PATCH_FAILURES") ||
		!strings.Contains(refused.String(), "YOLO_ALLOW_MISSING_PROGRAMS=1 yolo host -- other-command") {
		t.Fatalf("the refusal of a patch failure with nothing to run:\n%s", refused.String())
	}
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "1")
	var waived strings.Builder
	if _, rc := hostReadinessAct(selection.packs, []string{"other-command"}, &waived, &run.ActInterrupt{}); rc != 0 ||
		!strings.Contains(waived.String(), "starts WITHOUT what a selected pack declares") {
		t.Fatalf("the missing-program hatch did not waive a patch failure with nothing to run (rc %d):\n%s", rc, waived.String())
	}
}
