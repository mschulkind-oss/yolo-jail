package cli

import (
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
