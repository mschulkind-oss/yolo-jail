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
	if !strings.Contains(errw.String(), "0001-ten.patch") || !strings.Contains(errw.String(), "YOLO_ALLOW_MISSING_PROGRAMS") ||
		!strings.Contains(errw.String(), "not waived") {
		t.Fatalf("readiness did not retain the concrete patch failure and refusal:\n%s", errw.String())
	}
}
