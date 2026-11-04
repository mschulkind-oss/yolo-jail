package check

import (
	"strings"
	"testing"
)

// network_test.go pins `yolo check` predicting the launch's network refusal (section_network.go;
// named-setups.md SU-R5, plan slice 4b): a written `network.mode` the selected setup's technology
// cannot hold is one [FAIL] in Merged Configuration, in the launch's words. Delete the call in
// sectionMergedConfig and the refusal cases fail.

func TestCheckRefusesANetworkModeTheSetupCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name, ws, setup string
		macOS           bool
		want, step      string
	}{
		// The fixture is a session (YOLO_VERSION set), so the step is YOLO_SETUP, naming a setup
		// that runs on a Mac and holds the mode.
		{"host under apple-container-self", `{"network": {"mode": "host"}}`, "apple-container-self", true,
			`network.mode = "host": setup apple-container-self always runs bridged, so the jail cannot share this machine's network.`,
			"a setup that can: `YOLO_SETUP=podman-self`."},
		{"bridge under seatbelt-self", `{"network": {"mode": "bridge"}}`, "seatbelt-self", true,
			`network.mode = "bridge": setup seatbelt-self always shares the Mac's network, so the agent cannot have a loopback of its own.`,
			"a setup that can: `YOLO_SETUP=apple-container-self`."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			got := runCheckOverConfigWith(t, tc.ws, tc.macOS, nil, withSessionSetup(tc.setup))
			if n := strings.Count(got, tc.want); n != 1 {
				t.Errorf("check printed the refusal %d times, want once:\n%s", n, got)
			}
			for _, w := range []string{"[FAIL] ", "yolo-jail.jsonc:", tc.step,
				"yolo config unset network.mode --project"} {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in\n%s", w, got)
				}
			}
		})
	}
}

// TestCheckPassesANetworkModeTheSetupCanHold is the control: podman-self holds either mode, and
// each Mac technology's own network passes.
func TestCheckPassesANetworkModeTheSetupCanHold(t *testing.T) {
	for _, tc := range []struct {
		name, ws, setup string
		macOS           bool
	}{
		{"podman-self host", `{"network": {"mode": "host"}}`, "podman-self", false},
		{"podman-self bridge", `{"network": {"mode": "bridge"}}`, "podman-self", false},
		{"apple-container-self bridge", `{"network": {"mode": "bridge"}}`, "apple-container-self", true},
		{"seatbelt-self host", `{"network": {"mode": "host"}}`, "seatbelt-self", true},
		// A host setup only `yolo host` runs, and it never reads network.mode.
		{"host-self bridge", `{"network": {"mode": "bridge"}}`, "host-self", false},
		{"host-self bridge on a Mac", `{"network": {"mode": "bridge"}}`, "host-self", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			got := runCheckOverConfigWith(t, tc.ws, tc.macOS, nil, withSessionSetup(tc.setup))
			if strings.Contains(got, "network.mode = ") {
				t.Errorf("check refused a mode the setup holds:\n%s", got)
			}
		})
	}
}

// withSessionSetup selects setup the way a session does (YOLO_SETUP, with the fixture's
// YOLO_VERSION kept).
func withSessionSetup(name string) func(*Options) {
	return func(o *Options) {
		inner := o.Getenv
		o.Getenv = func(k string) string {
			if k == "YOLO_SETUP" {
				return name
			}
			return inner(k)
		}
	}
}
