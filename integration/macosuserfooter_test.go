package integration

import (
	"strings"
	"testing"
)

// THE AGENT FOOTER'S NOTCH ON macos-user — docs/design/agent-footer.md OQ-FT13, build item 7.
//
// WHAT IT SETTLES. A macos-user session is at the jail notch, so its launch sets the marker
// every container launch sets, YOLO_VERSION, in the session env file
// (internal/macosuser/orchestrator.go, buildPlan). The Linux half is
// internal/macosuser/jailmarker_test.go, which runs the rendered env file through the launch's
// own reader. Nothing on Linux can say that a process inside sandbox-exec sees the marker, or
// that the staged `yolo` every agent footer runs is on the sandbox PATH and allowed to execute
// there. So both are asked from inside a real launch: the marker, where `yolo` resolves, and
// the renderer's own answer for the notch, which is what a macos-user Claude's footer shows.
//
// ONE LAUNCH, for macosusertools_test.go's reason: every launch pays for a native floor build.
func TestMacosUserFooterSaysJail(t *testing.T) {
	requireMacosUser(t)

	probe := strings.Join([]string{
		`echo "=== MARKER ==="`,
		`printf '%s\n' "${YOLO_VERSION-UNSET}"`,
		`echo "=== YOLO ==="`,
		`command -v yolo || echo "YOLO-NOT-FOUND"`,
		`echo "=== NOTCH ==="`,
		`yolo internal footer --template '{yolo.notch}' 2>&1; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n")
	r := runMacosUser(t, macosUserWorkspace(t, `{}`), probe)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before the probe could answer.\n"+
			"stdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	marker := strings.TrimSpace(section(r.stdout, "=== MARKER ===", "=== YOLO ==="))
	yolo := strings.TrimSpace(section(r.stdout, "=== YOLO ===", "=== NOTCH ==="))
	notch := strings.TrimSpace(section(r.stdout, "=== NOTCH ===", "=== END ==="))
	if marker == "" && yolo == "" && notch == "" {
		t.Fatalf("the sandbox produced no probe output.\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}

	t.Run("marker_set", func(t *testing.T) {
		if marker == "" || marker == "UNSET" {
			t.Errorf("YOLO_VERSION in the sandbox = %q, want the launcher's version: without it "+
				"config.InJail() answers host inside the sandbox (OQ-FT13)\nlaunch output:\n%s%s",
				marker, r.stdout, r.stderr)
		}
	})
	t.Run("renderer_on_path", func(t *testing.T) {
		if yolo == "" || yolo == "YOLO-NOT-FOUND" {
			t.Errorf("`command -v yolo` in the sandbox = %q: every agent footer runs `yolo internal "+
				"footer`, so none would render here\nlaunch output:\n%s%s", yolo, r.stdout, r.stderr)
		}
	})
	t.Run("notch_is_jail", func(t *testing.T) {
		if notch != "jail\nRC=0" {
			t.Errorf("`yolo internal footer --template '{yolo.notch}'` in the sandbox printed %q, "+
				"want `jail` and exit 0\nlaunch output:\n%s%s", notch, r.stdout, r.stderr)
		}
	})
}
