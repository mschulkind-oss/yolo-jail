package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// placedDoorwayModule writes a loophole whose host daemon is program and whose jail daemon is a
// doorway (it declares its host argv), the shape run's doorwayplacement_test.go launches.
func placedDoorwayModule(t *testing.T, root, program string) {
	t.Helper()
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLoopholeManifest(t, root, "acme-proxy", `"name": "acme-proxy",
		"description": "acme proxy", "transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "cmd": ["`+program+`", "--socket", "{socket}"]},
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1999", "caller_token": true,
		"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}`)
}

// `yolo check` PREDICTS HS-D34'S REFUSAL. A doorway whose host service the placement rule refuses
// refuses the launch on every backend (docs/design/host-notch-services.md#HS-D34), so the check's
// row for it is a [FAIL] that says the launch will refuse and names the fix, not a list of
// placement problems under a row that reads as a skipped validator.
func TestCheckSaysTheLaunchRefusesADoorwayWhoseServiceThePlacementRuleRefuses(t *testing.T) {
	for _, setup := range []string{"podman-self", "seatbelt-self"} {
		t.Run(setup, func(t *testing.T) {
			moduleRoot := isolatedModuleDir(t)
			workspace := t.TempDir()
			program := filepath.Join(workspace, "acme-daemon")
			placedDoorwayModule(t, moduleRoot, program)
			r, out := runCheckLoopholesAt(t, workspace, setup == "seatbelt-self", setup)
			if r.failed == 0 {
				t.Fatalf("the placement refusal of a doorway's service is not a [FAIL]:\n%s", out)
			}
			for _, want := range []string{
				"loophole acme-proxy: the launch will refuse its doorway",
				program,
				`The next launch refuses: the "acme-proxy" doorway forwards to this host service, ` +
					"which the placement rule refuses to start",
				"HS-D34",
				"then run `yolo check --no-build` again",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the check must say %q:\n%s", want, out)
				}
			}
		})
	}
}

// The same doorway with its service outside the workspace draws no such row: the refusal is the
// placement rule's, not every doorway's.
func TestCheckDoesNotPredictARefusalForADoorwayWhoseServiceThePlacementRuleAdmits(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	placedDoorwayModule(t, moduleRoot, filepath.Join(t.TempDir(), "acme-daemon"))
	_, out := runCheckLoopholesAt(t, t.TempDir(), false, "podman-self")
	if strings.Contains(out, "the launch will refuse its doorway") {
		t.Fatalf("a doorway whose service the placement rule admits was predicted refused:\n%s", out)
	}
}
