package cli

// packtreefresh_test.go pins `--update` for a PACK-TREE PROGRAM (docs/design/launch-update-preparation.md,
// "Explicit update and launch materialization": a failed check, build or unsupported required
// freshness route fails the explicit request). A pack-tree program has no upstream to re-resolve, so
// --update builds or fetches it as any launch does, but a failure must still reach the launch as a
// fresh-preparation failure, which run.refuseFreshPreparation refuses before the ordinary
// readiness and YOLO_ALLOW_MISSING_PROGRAMS route.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE CALL SITE: runBuildSlot → addForkKeys → buildPlainForkForLaunch, under FreshUpdate. Red if
// addForkKeys answers a failed pack-tree build to an --update launch without a FreshFailure.
func TestAFreshUpdateFailsOnAFailedPackTreeBuild(t *testing.T) {
	f, digest := packTreeBuildFixture(t, "exit 3")
	runs := 0
	withFakeCaptureJail(t, failingPackTreeJail(t, &runs))
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	var stream syncBuffer
	req := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: digest}}, Platform: "linux/amd64",
		Runtime: "podman", Workspace: floortest.ResolvedTemp(t), Interrupt: &run.ActInterrupt{}, FreshUpdate: true}
	forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
	d := forks["x"]
	if d.Key != "" || runs != 1 {
		t.Fatalf("delivery %+v after %d build runs, want no key after one failed build\n%s", d, runs, stream.String())
	}
	if d.FreshFailure == "" || !strings.Contains(d.FreshFailure, "`yolo pack build fx`") {
		t.Errorf("an --update launch's failed pack-tree build carries no fresh failure naming its retry: %+v", d)
	}
}

// THE CALL SITE: runBuildSlot → addForkKeys → fetchPrebuiltForLaunch, under FreshUpdate. Red if a
// prebuilt that fails verification answers an --update launch without a FreshFailure.
func TestAFreshUpdateFailsOnAPrebuiltThatFailsVerification(t *testing.T) {
	served := []byte("#!/bin/sh\necho tampered\n")
	pinned := sha256Hex([]byte("#!/bin/sh\necho the real one\n"))
	url, _, _ := servePrebuilt(t, served)
	f := prebuiltFixture(t, url, pinned, false)
	pin := packload.PackTreeResolve(f, prebuiltPlatform, false)
	var stream bytes.Buffer
	req := run.ForkBuildRequest{Pins: []packload.ForkPin{pin}, Platform: prebuiltPlatform, Runtime: "podman",
		Workspace: floortest.ResolvedTemp(t), Interrupt: &run.ActInterrupt{}, FreshUpdate: true}
	forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
	d := forks["x"]
	if d.Key != "" {
		t.Fatalf("a mismatched artifact was delivered: %+v\n%s", d, stream.String())
	}
	if d.FreshFailure == "" || !strings.Contains(d.FreshFailure, pinned) {
		t.Errorf("an --update launch's failed prebuilt carries no fresh failure naming the pinned digest: %+v", d)
	}
}
