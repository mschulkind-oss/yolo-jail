package run

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTheSetupGateRefusesAContainerThatIsNotApples: a `container` on PATH that is not Apple's is
// refused at the gate, before anything is built for it, by the probe `yolo check` runs
// (setup.CheckAppleContainer) and with its next step: the install, and podman-self only where
// Podman is installed to run it. Apple's own CLI passes the gate.
func TestTheSetupGateRefusesAContainerThatIsNotApples(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		podman, pass  bool
	}{
		{name: "Apple's CLI", version: "container CLI version 1.1.0 (build: release)", pass: true},
		{name: "another container, Podman installed", version: "container 2.3 (some other tool)", podman: true},
		{name: "another container, no Podman", version: "container 2.3 (some other tool)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupGateWorkspace(t, "{}\n")
			var stdout, stderr bytes.Buffer
			o := setupGateOptions(t, ws, nil, &stdout, &stderr)
			o.IsMacOS, o.IsLinux = true, false
			o.SetupName = "apple-container-self"
			o.LookPath = func(name string) (string, bool) {
				if name == "container" || (tc.podman && name == "podman") {
					return "/opt/bin/" + name, true
				}
				return "", false
			}
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if len(argv) == 2 && filepath.Base(argv[0]) == "container" && argv[1] == "--version" {
					return ExecResult{Ran: true, RC: 0, Stdout: tc.version}
				}
				return ExecResult{Ran: false}
			}
			rc := Run(*o)
			out := stderr.String()
			if tc.pass {
				if strings.Contains(out, "is not Apple's container CLI") {
					t.Errorf("Apple's CLI was refused:\n%s", out)
				}
				return
			}
			if rc != 1 || !strings.Contains(out, "Refusing to launch: the 'container' on PATH (/opt/bin/container) is not Apple's container CLI") ||
				!strings.Contains(out, "https://github.com/apple/container") || strings.Contains(out, pastTheGate) {
				t.Errorf("rc=%d, want 1 and the identity refusal at the gate:\n%s", rc, out)
			}
			if got := strings.Contains(out, "yolo --setup podman-self -- claude"); got != tc.podman {
				t.Errorf("podman installed=%v, yet the refusal names podman-self=%v:\n%s", tc.podman, got, out)
			}
		})
	}
}
