package cli

import (
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue"
	"strings"
	"testing"
)

// TestApproveWebIsWiredIntoYoloApprove fails if the `--web` dispatch in runApprove is deleted:
// it goes through runApprove itself, not through approveWeb.
func TestApproveWebIsWiredIntoYoloApprove(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	var got []string
	called := 0
	old := approveWebStart
	approveWebStart = func(args []string) int { called++; got = args; return 0 }
	t.Cleanup(func() { approveWebStart = old })

	rc, _, errs := captureStd(t, func() int {
		return runApprove([]string{"approve", "--web", "--listen", "127.0.0.1:0", "--no-open"})
	})
	if rc != 0 || called != 1 || strings.Join(got, " ") != "--listen 127.0.0.1:0 --no-open" {
		t.Fatalf("rc %d, start called %d times with %q; stderr %q", rc, called, got, errs)
	}
}

func TestApproveWebRefusesInAJailAndNamesTheHost(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	called := 0
	old := approveWebStart
	approveWebStart = func([]string) int { called++; return 0 }
	t.Cleanup(func() { approveWebStart = old })

	rc, _, errs := captureStd(t, func() int { return runApprove([]string{"approve", "--web"}) })
	if rc != approveExitUsage || called != 0 {
		t.Fatalf("rc %d, start called %d times", rc, called)
	}
	if !strings.Contains(errs, "host-only") || !strings.Contains(errs, "on the host") {
		t.Fatalf("the refusal must name the next step: %q", errs)
	}
}

func TestApproveHelpDocumentsTheExperimentalWebQueue(t *testing.T) {
	for _, want := range []string{"yolo approve --web", "experimental", "read-only", "--no-open", "--listen", "--reconcile", "--assets"} {
		if !strings.Contains(approveUsage, want) {
			t.Errorf("approve help does not mention %q", want)
		}
	}
}

// TestApproveHelpNamesEveryFlagTheWebQueueParses pins the --web synopsis to the queue's own flag
// set: `-h` makes that parser print every flag it defines, and each must appear in the help's
// --web line. MUTATION: add a flag to approvalqueue's FlagSet, or delete `[--ended]` from the
// synopsis, and this fails.
func TestApproveHelpNamesEveryFlagTheWebQueueParses(t *testing.T) {
	_, _, errs := captureStd(t, func() int { return approvalqueue.Main([]string{"-h"}) })
	var synopsis string
	for _, line := range strings.Split(approveUsage, "\n") {
		if strings.Contains(line, "yolo approve --web") {
			synopsis = line
			break
		}
	}
	if synopsis == "" {
		t.Fatal("approve help has no --web synopsis line")
	}
	var flags []string
	for _, line := range strings.Split(errs, "\n") {
		if strings.HasPrefix(line, "  -") {
			flags = append(flags, strings.Fields(line)[0])
		}
	}
	if len(flags) < 5 {
		t.Fatalf("read %d flags from the queue's -h output, want at least 5:\n%s", len(flags), errs)
	}
	for _, f := range flags {
		if !strings.Contains(synopsis, "-"+f) {
			t.Errorf("approve help's --web line %q does not name the parser's %s flag", synopsis, "-"+f)
		}
	}
}
