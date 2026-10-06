package hostfloor

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stalecopystep_test.go pins StaleCopyStep, the next step for a copy the floor still holds of a
// program it no longer keeps. Only an owned host's `yolo host apply --assert` runs Reconcile:
// under "none", the unset default since the `assert` retirement (OQ-CO14), that apply refuses
// before every stage, so a line saying "`yolo host apply --assert` removes it" names a step that
// fails. There the step is the removal by hand, and it must remove exactly what Reconcile would.

// staleCopy lays down what a provisioned entry leaves in the prefix: its launcher, record and
// install dir.
func staleCopy(t *testing.T, f *Floor, bin string) {
	t.Helper()
	for _, p := range []string{f.Launcher(bin), f.recordPath(bin),
		filepath.Join(f.programsDir(bin), "v1", "bin", bin)} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheStaleCopyStepUnderOwnIsTheApply(t *testing.T) {
	f := &Floor{Dir: t.TempDir()}
	if got := f.StaleCopyStep(true, "floorcli"); got != "`yolo host apply --assert` removes it" {
		t.Errorf("owned: %q", got)
	}
	if got := f.StaleCopyStep(true, "a", "b"); got != "`yolo host apply --assert` removes them" {
		t.Errorf("owned, two bins: %q", got)
	}
}

// UNDER "none" THE STEP IS THE HAND REMOVAL, and it works: run as printed, it leaves nothing
// Reconcile would still remove. It never offers the apply as the step by itself.
func TestTheStaleCopyStepUnderNoneRemovesWhatReconcileWould(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "host floor") // a space: the command must quote
	f := &Floor{Dir: dir}
	staleCopy(t, f, "floorcli")
	staleCopy(t, f, "other")
	got := f.StaleCopyStep(false, "floorcli", "other")
	if strings.HasPrefix(got, "`yolo host apply --assert` removes") {
		t.Errorf("under none the step is the apply, which writes nothing there: %q", got)
	}
	if !strings.Contains(got, `"host_management": "own"`) {
		t.Errorf("the step does not say when the apply would do it: %q", got)
	}
	m := regexp.MustCompile("remove them by hand with `([^`]+)`").FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no hand-removal command in %q", got)
	}
	if out, err := exec.Command("sh", "-c", m[1]).CombinedOutput(); err != nil {
		t.Fatalf("the printed command failed: %v\n%s", err, out)
	}
	if left := f.Reconcile(nil, false); len(left) != 0 {
		t.Errorf("after the printed command Reconcile still finds %+v", left)
	}
	if f.StaleCopyStep(false, "floorcli") == got || !strings.Contains(f.StaleCopyStep(false, "floorcli"), "remove it by hand") {
		t.Errorf("one bin: %q", f.StaleCopyStep(false, "floorcli"))
	}
}
