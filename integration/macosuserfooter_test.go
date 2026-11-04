package integration

import (
	"strings"
	"testing"
)

// THE AGENT FOOTER'S SETUP ON macos-user — docs/reference/agent-footer.md OQ-FT13, "What has and has not been observed".
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
func TestMacosUserFooterSaysItsSetup(t *testing.T) {
	requireMacosUser(t)

	probe := strings.Join([]string{
		`echo "=== MARKER ==="`,
		`printf '%s\n' "${YOLO_VERSION-UNSET}"`,
		`echo "=== YOLO ==="`,
		`command -v yolo || echo "YOLO-NOT-FOUND"`,
		`echo "=== SETUP ==="`,
		`yolo internal footer --template '{yolo.setup}' 2>&1; echo "RC=$?"`,
		// The same marker makes a host apply refuse inside the sandbox (hostapplyinjail.go):
		// rendering the account's own home at the host notch would rewrite what its launch
		// rendered for the jail.
		`echo "=== HOSTAPPLY ==="`,
		`yolo host apply 2>&1; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n")
	r := runMacosUser(t, macosUserWorkspace(t, `{}`), probe)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before the probe could answer.\n"+
			"stdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	marker := strings.TrimSpace(section(r.stdout, "=== MARKER ===", "=== YOLO ==="))
	yolo := strings.TrimSpace(section(r.stdout, "=== YOLO ===", "=== SETUP ==="))
	setupName := strings.TrimSpace(section(r.stdout, "=== SETUP ===", "=== HOSTAPPLY ==="))
	hostApply := strings.TrimSpace(section(r.stdout, "=== HOSTAPPLY ===", "=== END ==="))
	if marker == "" && yolo == "" && setupName == "" {
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
	t.Run("host_apply_refuses", func(t *testing.T) {
		if strings.HasSuffix(hostApply, "RC=0") || !strings.Contains(hostApply, "inside a jail") {
			t.Errorf("`yolo host apply` in the sandbox printed %q, want a refusal naming the jail "+
				"and a non-zero exit\nlaunch output:\n%s%s", hostApply, r.stdout, r.stderr)
		}
	})
	t.Run("setup_is_named", func(t *testing.T) {
		if setupName != "seatbelt-yolo-account\nRC=0" {
			t.Errorf("`yolo internal footer --template '{yolo.setup}'` in the sandbox printed %q, "+
				"want `seatbelt-yolo-account` and exit 0\nlaunch output:\n%s%s", setupName, r.stdout, r.stderr)
		}
	})
}
