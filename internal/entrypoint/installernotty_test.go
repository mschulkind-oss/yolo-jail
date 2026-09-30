package entrypoint

// installernotty_test.go pins PS-D1 (docs/design/provisioner-sets.md) at the jail's launcher: the
// native launcher runs the vendor's installer, on first use and on update alike, through
// `yolo internal no-terminal`, with a /dev/null stdin. That the verb really leaves the child no
// controlling terminal is internal/notty's test, over a pty; here a stub yolo stands in for it,
// records what it was asked, and runs the command with a marker, so the launcher's ROUTING is
// what is under test. No agent runs and nothing leaves the loopback.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// installerProbeBody is an installer that says whether it ran through the stub and whether its
// stdin gave it anything, then installs probetool.
const installerProbeBody = `#!/bin/bash
echo "VIA=${NOTTY_STUB:-direct}"
if read -r _answer; then echo "STDIN=GOT:$_answer"; else echo "STDIN=EOF"; fi
mkdir -p "$HOME/.local/bin"
printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"
chmod +x "$HOME/.local/bin/probetool"
echo INSTALLER_RAN
`

// writeNoTerminalStub writes a yolo into dir that implements only `internal no-terminal -- …`:
// it logs its argv and runs the command with NOTTY_STUB=1 and a /dev/null stdin.
func writeNoTerminalStub(t *testing.T, dir, log string) {
	t.Helper()
	body := `#!/bin/bash
printf '%s\n' "$*" >> ` + shellQuoteForTest(log) + `
[ "$1 $2 $3" = "internal ` + NoTerminalVerb + ` --" ] || exit 2
shift 3
NOTTY_STUB=1 exec "$@" </dev/null
`
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runInstallerLauncher renders the native launcher for url into a temp home, runs it with PATH
// and "y" on its stdin (an answer a prompting installer would take), and returns its output.
func runInstallerLauncher(t *testing.T, url, path string, env ...string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home := t.TempDir()
	launcher := nativeAgentLauncher(&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		filepath.Join(home, "stamps"), filepath.Join(home, "ws", ".yolo", "receipts.jsonl"), "",
		true, launcherServers{}, nil)
	script := filepath.Join(home, "probetool-launcher")
	if err := os.WriteFile(script, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + path}, env...)
	cmd.Stdin = strings.NewReader("y\n")
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("running launcher: %v\n%s", err, out)
	}
	return string(out), home
}

// On first use the installer runs through the verb: the probe the launcher asks first, then the
// installer itself, and the installer sees no stdin although the launcher was handed a "y".
func TestTheNativeLauncherRunsItsInstallerWithNoTerminal(t *testing.T) {
	stub := t.TempDir()
	log := filepath.Join(stub, "argv.log")
	writeNoTerminalStub(t, stub, log)
	url := serveBody(t, 200, "application/x-sh", installerProbeBody)
	out, _ := runInstallerLauncher(t, url, stub+":"+os.Getenv("PATH"))
	for _, want := range []string{"VIA=1", "STDIN=EOF", "INSTALLER_RAN", "PROBETOOL_RAN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	calls := readFileString(t, log)
	if !strings.Contains(calls, "internal "+NoTerminalVerb+" -- true\n") {
		t.Errorf("the launcher did not ask whether the verb exists first:\n%s", calls)
	}
	if !strings.Contains(calls, "internal "+NoTerminalVerb+" -- bash ") {
		t.Errorf("the installer did not run through the verb:\n%s", calls)
	}
}

// The update arm (no declared verb, so the installer is re-run) takes the same route: PS-D1
// names the launcher's install on first use AND on update.
func TestTheNativeLauncherRunsItsUpdateInstallerWithNoTerminal(t *testing.T) {
	stub := t.TempDir()
	log := filepath.Join(stub, "argv.log")
	writeNoTerminalStub(t, stub, log)
	url := serveBody(t, 200, "application/x-sh", installerProbeBody)
	// A first run installs; the pack update then re-runs the installer.
	_, home := runInstallerLauncher(t, url, stub+":"+os.Getenv("PATH"))
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "probetool-launcher")
	cmd := exec.Command(launcher)
	cmd.Env = []string{"HOME=" + home, "PATH=" + stub + ":" + os.Getenv("PATH"), "YOLO_PACK_UPDATE=1"}
	cmd.Stdin = strings.NewReader("y\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack update: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "VIA=1") || !strings.Contains(string(out), "STDIN=EOF") {
		t.Errorf("the update's installer did not run through the verb with no stdin:\n%s", out)
	}
	if !strings.Contains(readFileString(t, log), "internal "+NoTerminalVerb+" -- bash ") {
		t.Errorf("the update did not call the verb:\n%s", readFileString(t, log))
	}
}

// With no yolo that has the verb (none on PATH here), the installer still runs with a /dev/null
// stdin, and the launcher says it could not take the terminal away.
func TestTheNativeLauncherWithoutTheVerbStillWithholdsStdin(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", installerProbeBody)
	out, _ := runInstallerLauncher(t, url, pathWithout(t, "yolo"))
	for _, want := range []string{"VIA=direct", "STDIN=EOF", "INSTALLER_RAN",
		"cannot detach probetool's installer from this terminal"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
