package cli

// hostpatchprewrite_test.go pins PF-D81's CHECK BEFORE A HOST WRITE (hostpatchpreflight.go,
// advanceHostTrees' refusal): a host verb that renders into the real home stops on a patch series
// that does not apply before its first write, so a refused verb leaves nothing half-written, and the
// patch failure's own bypass goes on, running the intact build.

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// countGateSurveys counts the host-render gate's observe passes, the first thing the gate's render
// does, so a test can say no render began.
func countGateSurveys(t *testing.T) *int {
	t.Helper()
	n := 0
	prev := hostApplyGateSurvey
	hostApplyGateSurvey = func(out, errw io.Writer, color, write bool, stdin io.Reader, s *hostApplySurvey) int {
		n++
		return prev(out, errw, color, write, stdin, s)
	}
	t.Cleanup(func() { hostApplyGateSurvey = prev })
	return &n
}

// A PATCHED EXTENSION'S SERIES THAT DOES NOT APPLY STOPS `yolo host -- <bin>` BEFORE THE GATE, even
// with its good build linked: the gate's render never runs, the program does not start, and the
// error block offers the bypass that runs the good build; with it, the launch goes on. Red with
// hostLaunch ignoring advanceHostTrees' answer.
func TestAHostLaunchStopsOnATreesPatchFailureBeforeTheGate(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn+`,"host_apply_on_launch":true`)
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("the first launch: rc=%d execed %v\n%s", rc, got.execed, errw.String())
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	surveys := countGateSurveys(t)
	got.execed = false
	errw.Reset()
	rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	if rc == 0 || got.execed || *surveys != 0 {
		t.Fatalf("a series that does not apply: rc=%d execed %v, the gate surveyed %d times, want a refusal before it\n%s",
			rc, got.execed, *surveys, errw.String())
	}
	for _, w := range []string{"ERROR: treepack/tool-ext: patch application failed at upstream v1.2.0",
		"  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo host -- tool\n",
		"yolo host: refusing: the patch series of extension treepack/tool-ext does not apply (the ERROR above); nothing was written."} {
		if !strings.Contains(errw.String(), w) {
			t.Errorf("the refusal lacks %q:\n%s", w, errw.String())
		}
	}
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	errw.Reset()
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed ||
		!strings.Contains(errw.String(), "CONTINUING: using intact admitted build") {
		t.Errorf("the bypass: rc=%d execed %v\n%s", rc, got.execed, errw.String())
	}
}

// `yolo host apply --assert` STOPS THE SAME WAY BEFORE ITS FIRST WRITE: the extension's link is left
// as it was and no render ran. Red with hostApplyRefreshAndRender ignoring advanceHostTrees' answer.
func TestAHostApplyStopsOnATreesPatchFailureBeforeItWrites(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn+`,"host_apply_on_launch":false`)
	stubBins(t, "tool")
	var out, errw bytes.Buffer
	if rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", ""); rc != 0 {
		t.Fatalf("the first apply: rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	out.Reset()
	errw.Reset()
	if rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", ""); rc == 0 ||
		strings.Contains(out.String(), "host apply — applying into") ||
		!strings.Contains(errw.String(), "  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo host apply --assert\n") ||
		!strings.Contains(errw.String(), "nothing was written") {
		t.Errorf("an apply over a series that does not apply: rc=%d, want a refusal before its render\n%s\n%s",
			rc, out.String(), errw.String())
	}
}

// A PATCHED FORK'S RECORDED PATCH FAILURE STOPS `yolo host apply --assert` BEFORE ITS FIRST WRITE,
// read from the record with no advance (hostPatchPreflight). Red with its call in
// hostApplyRefreshAndRender deleted, which renders the home and fails only at the floor stage.
func TestAHostApplyStopsOnAForksRecordedPatchFailureBeforeItWrites(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.writeUserConfig(t, `,"host_management":"own"`)
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	if rc, _, out := fx.hostLaunch(t); rc == 0 || !strings.Contains(out, "0001-ten.patch") {
		t.Fatalf("the launch that records the failure: rc=%d\n%s", rc, out)
	}
	var out, errw bytes.Buffer
	rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", "")
	if rc == 0 || strings.Contains(out.String(), "host apply — applying into") ||
		!strings.Contains(errw.String(), "ERROR: fork forkpack/tool: patch application failed at upstream v1.2.0") ||
		!strings.Contains(errw.String(), "  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo host apply --assert\n") ||
		!strings.Contains(errw.String(), "yolo host: refusing the host apply: the patch series of fork forkpack/tool does not apply") {
		t.Errorf("an apply over a recorded patch failure: rc=%d, want a refusal before its render\n%s\n%s",
			rc, out.String(), errw.String())
	}
}

// UNDER host_apply_on_launch, `yolo host -- <bin>` STOPS ON A PATCHED FORK'S RECORDED PATCH FAILURE
// BEFORE THE GATE MAY APPLY: no observe pass, no render, no exec. Red with hostLaunch's
// hostPatchPreflight call deleted, which surveys (and may apply) first.
func TestAHostLaunchStopsOnAForksRecordedPatchFailureBeforeTheGate(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.writeUserConfig(t, `,"host_management":"own","host_apply_on_launch":true`)
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	if rc, _, out := fx.hostLaunch(t); rc == 0 || !strings.Contains(out, "0001-ten.patch") {
		t.Fatalf("the launch that records the failure: rc=%d\n%s", rc, out)
	}
	surveys := countGateSurveys(t)
	rc, target, out := fx.hostLaunch(t)
	if rc == 0 || target != "" || *surveys != 0 {
		t.Fatalf("a recorded patch failure: rc=%d exec %q, the gate surveyed %d times, want a refusal before it\n%s",
			rc, target, *surveys, out)
	}
	if n := strings.Count(out, "ERROR: fork forkpack/tool: patch application failed"); n != 1 ||
		!strings.Contains(out, "yolo host: refusing to launch tool: the patch series of fork forkpack/tool does not apply") {
		t.Errorf("the refusal says the error %d times, want once, and names the stop:\n%s", n, out)
	}
}

// WITHOUT host_apply_on_launch, `yolo host -- <bin>` still stops on a patched extension's RECORDED
// patch failure, though its linked good build is there to load: the advance before the gate does
// not run, so the stop (hostTreeGate) reads the record. The bypass loads the linked build, said.
// Red with hostTreeGate's patch-failure read deleted, which started tool on the older build.
func TestAHostLaunchWithoutApplyOnLaunchStopsOnATreesRecordedPatchFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, treeHostOwn+`,"host_apply_on_launch":false`)
	stubBins(t, "tool")
	var out, errw bytes.Buffer
	if rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", ""); rc != 0 || !isDir(fx.link()) {
		t.Fatalf("the first apply: rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	if advanceHostTrees(io.Discard, false, "", nil) {
		t.Fatal("the advance that records the failure did not stop")
	}
	got := captureHostExec(t)
	errw.Reset()
	rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	if rc == 0 || got.execed {
		t.Fatalf("a recorded patch failure: rc=%d execed %v, want a refusal\n%s", rc, got.execed, errw.String())
	}
	if n := strings.Count(errw.String(), "ERROR: treepack/tool-ext: patch application failed at upstream v1.2.0"); n != 1 ||
		!strings.Contains(errw.String(), "  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo host -- tool\n") {
		t.Errorf("the refusal says the error %d times, want once, with the bypass:\n%s", n, errw.String())
	}
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	errw.Reset()
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed ||
		!strings.Contains(errw.String(), "CONTINUING: YOLO_ALLOW_PATCH_FAILURES=1 is set") {
		t.Errorf("the bypass: rc=%d execed %v\n%s", rc, got.execed, errw.String())
	}
}

// THE APPLY `yolo pack update` RUNS (deferred: it advances nothing) stops before its first write on a
// patched fork's and a patched extension's recorded patch failure alike, and says no second error
// block: the update's own check printed each one just before it. Red with hostPatchPreflight
// checking forks alone, or printing the blocks again for the pack-update apply.
func TestThePackUpdateApplyStopsOnRecordedPatchFailuresBeforeItWritesSayingThemOnce(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	t.Run("fork", func(t *testing.T) {
		fx := patchedFloorFixture(t)
		fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
		fx.writeUserConfig(t, `,"host_management":"own"`)
		if rc, _, out := fx.hostLaunch(t); rc != 0 {
			t.Fatalf("the first launch: rc=%d\n%s", rc, out)
		}
		fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
		fx.later(2 * time.Hour)
		if rc, _, out := fx.hostLaunch(t); rc == 0 {
			t.Fatalf("the launch that records the failure: rc=%d\n%s", rc, out)
		}
		var out, errw bytes.Buffer
		rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", packUpdateBuildsNoPatched)
		if rc == 0 || strings.Contains(out.String(), "host apply — applying into") || strings.Contains(errw.String(), "ERROR: ") ||
			!strings.Contains(errw.String(), "yolo host: refusing the host apply: the patch series of fork forkpack/tool does not apply") {
			t.Errorf("the pack-update apply over a recorded patch failure: rc=%d\n%s\n%s", rc, out.String(), errw.String())
		}
	})
	t.Run("extension", func(t *testing.T) {
		fx := newTreeFixture(t, `"f.txt"`)
		fx.listTreeForAgent(t)
		fx.writeHostConfig(t, treeHostOwn+`,"host_apply_on_launch":false`)
		stubBins(t, "tool")
		var out, errw bytes.Buffer
		if rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", ""); rc != 0 {
			t.Fatalf("the first apply: rc=%d\n%s\n%s", rc, out.String(), errw.String())
		}
		fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
		fx.now = fx.now.Add(2 * time.Hour)
		if advanceHostTrees(io.Discard, false, "", nil) {
			t.Fatal("the advance that records the failure did not stop")
		}
		out.Reset()
		errw.Reset()
		rc := hostApplyRefreshAndRender(&out, &errw, false, true, nil, "", packUpdateBuildsNoPatched)
		if rc == 0 || strings.Contains(out.String(), "host apply — applying into") || strings.Contains(errw.String(), "ERROR: ") ||
			!strings.Contains(errw.String(), "yolo host: refusing the host apply: the patch series of extension treepack/tool-ext does not apply") {
			t.Errorf("the pack-update apply over a recorded tree failure: rc=%d\n%s\n%s", rc, out.String(), errw.String())
		}
	})
}
