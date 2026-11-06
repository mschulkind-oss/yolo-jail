package cli

// stopleftover_test.go pins `yolo stop`'s handling of a container of the jail's name that EXISTS but
// is not running. probeJailRunning is RUNNING-ONLY, so a stopped/exited/created leftover read as
// "no jail": the stop did nothing and the next launch was refused by name. The stop must say the
// state, end the leftover, and — when the runtime will not remove it — report the failure with the
// next step, never report success.

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// leftoverRuntime is a fake podman whose container cname exists in state, not running. rmArgv
// records the removal that was run; removed flips once it succeeds; rmFails refuses it (the wedged
// case). The state probe answers "no such object" once removed, so the post-removal existence check
// settles on the removal's effect, never its exit status.
func leftoverRuntime(cname, state string, rmArgv *[]string, removed *bool, rmFails bool) stopCommandRunner {
	return func(argv []string, _ time.Duration) (string, bool, int) {
		switch {
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			if *removed {
				return "", true, 1
			}
			return "", true, 0
		case argv[1] == "inspect" && slices.Contains(argv, "{{.State.Running}}"):
			return "false\n", true, 0
		case argv[1] == "inspect" && slices.Contains(argv, "{{.State.Status}}"):
			if *removed {
				return "", true, 125 // no such object: the removal took
			}
			return state + "\n", true, 0
		case argv[1] == "rm":
			*rmArgv = argv
			if rmFails {
				return "", true, 1
			}
			*removed = true
			return "", true, 0
		}
		return "", false, 1
	}
}

// TestAStopEndsALeftoverContainerThatWouldBlockTheNextLaunch: an existing, not-running container is
// not "no jail". The stop names its state, removes it, and says so. Deleting the existence gate
// fails it — pre-change the stop printed "No jail running" and removed nothing.
func TestAStopEndsALeftoverContainerThatWouldBlockTheNextLaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	var rmArgv []string
	removed := false
	run := leftoverRuntime(cname, "exited", &rmArgv, &removed, false)
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", run, nil, false); rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "No jail running") {
		t.Errorf("an existing container was read as no jail:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"exited"`) {
		t.Errorf("the stop must name the container's state:\n%s", out.String())
	}
	if want := []string{"podman", "rm", cname}; !slices.Equal(rmArgv, want) {
		t.Errorf("the removal argv = %v, want %v", rmArgv, want)
	}
	if !strings.Contains(out.String(), "Ended the leftover container") {
		t.Errorf("the stop must say it ended it:\n%s", out.String())
	}
}

// TestAForcedStopForceRemovesALeftoverContainer: --force escalates the removal to `rm --force`,
// which is what a container the plain rm refuses needs.
func TestAForcedStopForceRemovesALeftoverContainer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	var rmArgv []string
	removed := false
	run := leftoverRuntime(cname, "created", &rmArgv, &removed, false)
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", run, nil, true); rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if want := []string{"podman", "rm", "--force", cname}; !slices.Equal(rmArgv, want) {
		t.Errorf("the forced removal argv = %v, want %v", rmArgv, want)
	}
}

// TestAStopThatCannotRemoveALeftoverNamesTheNextStep: the removal fails and the container is still
// there. The stop must not report success or "no jail": it names the state, the removal that failed,
// and the runtime-owned recovery step.
func TestAStopThatCannotRemoveALeftoverNamesTheNextStep(t *testing.T) {
	shrinkStopGone(t)
	t.Setenv("HOME", t.TempDir())
	cname := runtime.FromWorkspace("/ws")
	var rmArgv []string
	removed := false
	run := leftoverRuntime(cname, "exited", &rmArgv, &removed, true)
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", run, nil, false); rc != 1 {
		t.Fatalf("rc=%d, want 1\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "No jail running") {
		t.Errorf("an existing container was read as no jail:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Ended the leftover container") {
		t.Errorf("a removal that did not take was reported as success:\n%s", out.String())
	}
	for _, want := range []string{
		"still there in state \"present\"",
		"`podman rm -f " + cname + "`",
		"runtime administrator",
	} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the failure must name %q:\n%s", want, errb.String())
		}
	}
}
