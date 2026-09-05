package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// installonly_test.go pins InstallOnlyEnv, the one thing `yolo capture` needs from the
// native launcher: install, and then STOP.
//
// It runs the real generated launcher against a real (localhost) installer URL, like every
// other test in nativelauncher_test.go, because the property under test is a branch in
// generated shell and reading the template proves nothing about what bash does with it.

// runNativeLauncherWithEnv is runNativeLauncherWithReceipts' shape with an extra environment
// entry, returning the temp HOME as well so a caller can inspect what the install left.
func runNativeLauncherWithEnv(t *testing.T, url string, extraEnv ...string) (rc int, out, home string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home = t.TempDir()
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		filepath.Join(home, "stamps"),
		filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
		"", // no capture store: this cell is about the DOWNLOAD path
		true,
	)
	script := filepath.Join(home, "probetool")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, extraEnv...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running launcher: %v (output %s)", err, b)
		}
		rc = ee.ExitCode()
	}
	return rc, string(b), home
}

// installOnlyURL serves an installer that plants a probetool which SHOUTS when it runs, so
// "the launcher did not exec it" is an assertion about observed behaviour rather than about
// an absence.
func installOnlyURL(t *testing.T) string {
	t.Helper()
	return serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		"set -eu",
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
		`echo INSTALLER_RAN`,
	}, "\n")+"\n")
}

// With InstallOnlyEnv set, the launcher installs and exits 0 WITHOUT running the program.
//
// This is the property `yolo capture` depends on. The capture surfaces are the three home
// dirs an installer writes into, and a tool run once writes its own first-run state into
// them — config, machine identifiers, telemetry opt-ins — which would then be
// content-addressed into an entry and hardlinked into every workspace on the machine.
// program-delivery.md §6.3 names install-time personalization as the thing that defeats
// capture's sharing; exec'ing the tool would create it on purpose.
func TestInstallOnlyInstallsWithoutRunningTheProgram(t *testing.T) {
	rc, out, home := runNativeLauncherWithEnv(t, installOnlyURL(t), InstallOnlyEnv+"=1")

	if rc != 0 {
		t.Errorf("install-only must succeed when the install did, rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("the installer did not run:\n%s", out)
	}
	if strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("the launcher EXEC'd the program under %s — a capture would then record "+
			"the tool's first-run state as part of the vendor's package:\n%s", InstallOnlyEnv, out)
	}
	// The install itself is untouched: install-only suppresses the exec, nothing else.
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "probetool")); err != nil {
		t.Errorf("install-only skipped the INSTALL: %v", err)
	}
}

// The NEGATIVE CONTROL: without the variable the same launcher execs the program.
//
// Without it, "PROBETOOL_RAN is absent" would also pass for a launcher that installed
// nothing, or for a fixture whose tool never printed.
func TestWithoutInstallOnlyTheLauncherStillExecsTheProgram(t *testing.T) {
	rc, out, _ := runNativeLauncherWithEnv(t, installOnlyURL(t))

	if rc != 0 {
		t.Errorf("rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("the ordinary launch path must still exec the tool:\n%s", out)
	}
}

// A FAILED install still fails under install-only. The capture driver reads the exit code
// and refuses to store anything on a non-zero one, so a launcher that reported success for
// an install that produced no binary would hand the store an empty tree with a key.
func TestInstallOnlyFailsWhenNothingWasInstalled(t *testing.T) {
	url := serveBody(t, 200, "text/html; charset=utf-8", "<!doctype html><html><body>moved</body></html>")

	rc, out, _ := runNativeLauncherWithEnv(t, url, InstallOnlyEnv+"=1")

	if rc == 0 {
		t.Errorf("install-only must fail when the installer left no binary:\n%s", out)
	}
	if !strings.Contains(out, "not available") {
		t.Errorf("the refusal should name the missing program:\n%s", out)
	}
}

// The launcher reads the env var the Go constant names. They are one string by
// construction (the template splices InstallOnlyEnv in), and this is the assertion that
// keeps a future edit from re-typing it.
func TestInstallOnlyEnvIsSplicedIntoTheTemplate(t *testing.T) {
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/i.sh"},
		"/stamps", "/ws/.yolo/receipts.jsonl", "", true,
	)
	if !strings.Contains(body, "${"+InstallOnlyEnv+":-}") {
		t.Errorf("the native launcher does not read %s:\n%s", InstallOnlyEnv, body)
	}
}
