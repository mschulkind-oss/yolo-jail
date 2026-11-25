package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// useImmediateHostProgress shows every host phase's line at its start, so a test sees a phase's
// lines without one running a second.
func useImmediateHostProgress(t *testing.T) {
	t.Helper()
	prev := hostProgressConfig
	hostProgressConfig = func(spans ...string) progress.Config {
		return run.ProgressCovering(progress.Config{Immediate: true}, spans...)
	}
	t.Cleanup(func() { hostProgressConfig = prev })
}

// A host phase prints `<label>…` and `<label>: done (N.Ns)`, and whatever the step prints goes
// through the line, above its result.
func TestAHostPhasePrintsItsProgressLine(t *testing.T) {
	useImmediateHostProgress(t)
	trace := &hostLaunchTrace{timing: run.HostNotchTimingLog(false, false, func(string) string { return "" }, io.Discard)}
	var errw bytes.Buffer
	ok := trace.phase(&errw, "host.test_phase", "Doing the host thing", func(w io.Writer) bool {
		io.WriteString(w, "a disclosure\n")
		return true
	})
	if !ok {
		t.Fatal("phase reported failure for a step that succeeded")
	}
	got := errw.String()
	start := strings.Index(got, "Doing the host thing…")
	mid := strings.Index(got, "a disclosure")
	done := strings.Index(got, "Doing the host thing: done (")
	if start < 0 || mid < start || done < mid {
		t.Errorf("want the start line, the step's own line, then the result, got:\n%s", got)
	}
}

// A failing step prints no result line: its own message is the stop and stays the last line.
func TestAFailingHostPhaseLeavesItsOwnMessageLast(t *testing.T) {
	useImmediateHostProgress(t)
	trace := &hostLaunchTrace{timing: run.HostNotchTimingLog(false, false, func(string) string { return "" }, io.Discard)}
	var errw bytes.Buffer
	if trace.phase(&errw, "host.test_phase", "Doing the host thing", func(w io.Writer) bool {
		io.WriteString(w, "refused: run the fix\n")
		return false
	}) {
		t.Fatal("phase reported success for a failing step")
	}
	if got := errw.String(); !strings.HasSuffix(got, "refused: run the fix\n") {
		t.Errorf("the stop is not the last line:\n%s", got)
	}
}

// THE CALL SITES of the host launch's phases: delete one and that phase is silent again.
func TestEveryHostLaunchPhaseHasAProgressLine(t *testing.T) {
	src, err := os.ReadFile("host.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"host.pack_refresh", "host.compose", "host.readiness", "host.resolve_target",
		"host.prelaunch_refresh"} {
		if !strings.Contains(string(src), `trace.phase(errw, "`+name+`"`) {
			t.Errorf("host.go no longer runs %s under a progress line", name)
		}
	}
}
