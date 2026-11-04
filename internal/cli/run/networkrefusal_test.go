package run

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// networkrefusal_test.go pins the network refusal (networkrefusal.go; named-setups.md SU-R5, plan
// slice 4b): a written `network.mode`, or a typed `--network`, the setup's technology cannot hold
// is refused before anything is built, naming a setup that can and the step that removes it.
//
// ⚠ THE CALL SITE IS WHAT THESE ASSERT: every case calls Run() and tells a refusal from a pass by
// whether the launch reached its runtime (OnRuntimeResolved, or the runtime's own refusal for
// setup <name>, which resolveRuntime prints after the network gate). Delete the call in Run and
// every refusal case reaches the runtime.

// networkRunResult is what one Run() showed: its exit code, what it printed, and whether it got
// past the network gate to the setup's runtime.
type networkRunResult struct {
	rc      int
	out     string
	reached bool
}

// runNetworkLaunch runs a launch of the setup flag names (--setup) in a workspace whose
// yolo-jail.jsonc is wsConfig, with network the typed --network ("" for none), on macOS for a
// Mac setup and on Linux for podman-self. The user config is userConfig when not "".
func runNetworkLaunch(t *testing.T, wsConfig, userConfig, flag, network string) networkRunResult {
	t.Helper()
	ws := setupGateWorkspace(t, wsConfig)
	if userConfig != "" {
		writeUserConfigJSON(t, os.Getenv("HOME"), userConfig)
	}
	var stdout, stderr bytes.Buffer
	o := setupGateOptions(t, ws, nil, &stdout, &stderr)
	if flag != "podman-self" {
		o.IsMacOS, o.IsLinux = true, false
		o.LookPath = func(name string) (string, bool) {
			if name == "container" {
				return "/usr/local/bin/container", true
			}
			return "", false
		}
		// The setup gate refuses a `container` that is not Apple's before the network gate runs
		// (setup.CheckAppleContainer), so the fake on PATH answers as Apple's CLI.
		inner := o.Exec
		o.Exec = func(argv []string, cwd string, env []string, timeout time.Duration) ExecResult {
			if len(argv) == 2 && filepath.Base(argv[0]) == "container" && argv[1] == "--version" {
				return ExecResult{Ran: true, RC: 0, Stdout: "container CLI version 1.1.0 (build: release)"}
			}
			if inner == nil {
				return ExecResult{Ran: false}
			}
			return inner(argv, cwd, env, timeout)
		}
	}
	o.SetupName, o.Network = flag, network
	var res networkRunResult
	o.OnRuntimeResolved = func(string) error {
		res.reached = true
		return errors.New("stop here")
	}
	res.rc = Run(*o)
	res.out = stderr.String()
	if strings.Contains(res.out, "(for setup "+flag+")") || strings.Contains(res.out, pastTheGate) {
		res.reached = true
	}
	return res
}

const unsetNetworkStep = "Or, if the project does not need it:  yolo config unset network.mode --project"

func TestANetworkModeTheSetupCannotHoldRefusesTheLaunch(t *testing.T) {
	for _, tc := range []struct {
		name, ws, flag, network string
		want                    []string
	}{
		{name: "host under apple-container-self", ws: `{"network": {"mode": "host"}}`, flag: "apple-container-self",
			want: []string{
				"Refusing to launch: ",
				`yolo-jail.jsonc:`,
				`network.mode = "host": setup apple-container-self always runs bridged, so the jail cannot share this machine's network. For this launch, a setup that can:`,
				"    yolo --setup podman-self -- claude", unsetNetworkStep}},
		{name: "bridge under seatbelt-self", ws: `{"network": {"mode": "bridge"}}`, flag: "seatbelt-self",
			want: []string{
				`network.mode = "bridge": setup seatbelt-self always shares the Mac's network, so the agent cannot have a loopback of its own.`,
				"    yolo --setup podman-self -- claude", unsetNetworkStep}},
		{name: "--network host under apple-container-self", ws: `{}`, flag: "apple-container-self", network: "host",
			want: []string{
				"--network host: setup apple-container-self always runs bridged",
				"    yolo --setup podman-self -- claude",
				"Or, if the project does not need it:  launch without --network host"}},
		{name: "--network host over a written bridge, under apple-container-self", ws: `{"network": {"mode": "bridge"}}`,
			flag: "apple-container-self", network: "host",
			want: []string{"--network host: setup apple-container-self always runs bridged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runNetworkLaunch(t, tc.ws, "", tc.flag, tc.network)
			if res.rc != 1 {
				t.Errorf("Run() = %d, want 1", res.rc)
			}
			if res.reached {
				t.Errorf("the launch got PAST the network refusal to its runtime — it is not wired into Run:\n%s", res.out)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.out, w) {
					t.Errorf("missing %q in\n%s", w, res.out)
				}
			}
			if tc.network != "" && strings.Contains(res.out, "config unset network.mode") {
				t.Errorf("a typed flag is refused naming the key's unset:\n%s", res.out)
			}
		})
	}
}

// TestANetworkModeWrittenInTheUserConfigNamesItsScope: the unset names the file that holds the key.
func TestANetworkModeWrittenInTheUserConfigNamesItsScope(t *testing.T) {
	res := runNetworkLaunch(t, `{}`, `{"network": {"mode": "host"}}`, "apple-container-self", "")
	if res.reached || !strings.Contains(res.out, "yolo config unset network.mode --user") {
		t.Fatalf("reached %v, want the --user unset:\n%s", res.reached, res.out)
	}
}

// TestANetworkModeTheSetupCanHoldLaunches is the control: podman-self holds either mode, and
// what each Mac technology always has passes, written or not.
func TestANetworkModeTheSetupCanHoldLaunches(t *testing.T) {
	for _, tc := range []struct{ name, ws, flag, network string }{
		{"podman-self host", `{"network": {"mode": "host"}}`, "podman-self", ""},
		{"podman-self bridge", `{"network": {"mode": "bridge"}}`, "podman-self", ""},
		{"podman-self --network host", `{}`, "podman-self", "host"},
		{"apple-container-self bridge", `{"network": {"mode": "bridge"}}`, "apple-container-self", ""},
		{"apple-container-self unwritten", `{}`, "apple-container-self", ""},
		{"seatbelt-self host", `{"network": {"mode": "host"}}`, "seatbelt-self", ""},
		{"seatbelt-self unwritten", `{}`, "seatbelt-self", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runNetworkLaunch(t, tc.ws, "", tc.flag, tc.network)
			if !res.reached {
				t.Errorf("a mode the setup can hold did not reach the runtime (rc %d):\n%s", res.rc, res.out)
			}
			if strings.Contains(res.out, "network.mode = ") || strings.Contains(res.out, "--network host:") {
				t.Errorf("refused a mode the setup holds:\n%s", res.out)
			}
		})
	}
}
