package integration

// readiness_test.go is the container-level proof of the jail's readiness act
// (docs/design/jail-notch-readiness.md, OQ-JR1): a launch installs every program a selected pack
// declares before its command runs, and STOPS when one cannot be installed, unless the launch
// sets the hatch. The unit tier runs the bootstrap against a fake npm; only a launch proves the
// real launchers, the real stage wrapper and the container's exit status carry it to the host,
// and that the hatch typed on the host reaches the jail.
//
// The suite turns the act off for every other launch (readinessEnvForSuite); each launch here
// turns it back on (withReadiness). The fixture is a LOCAL pack whose installer is a file in the
// pack, as installmechanism_test.go's, so nothing here depends on a vendor or on the network;
// the failing case points its installer at the jail's own loopback, where nothing listens, which
// is what an install sees with no network.
//
// NO AGENT IS STARTED (AGENTS.md: "No agent tests"): the fixture's program is a shell script, and
// the commands check that it is installed without running it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

const (
	readinessPack = "readiness-fixture"
	readinessBin  = "yolo-readiness-tool"
)

// readinessFixture writes a local pack declaring one installer program whose installer is at url
// (a file:// URL into the staged pack, or one that cannot be fetched), selects it, and returns the
// workspace. The installer counts its runs in ~/.local/readiness-installs and plants a program
// that records each time it RUNS in ~/.local/readiness-runs.
func readinessFixture(t *testing.T, url string) string {
	t.Helper()
	pack := t.TempDir()
	installer := `#!/bin/bash
set -euo pipefail
mkdir -p "$HOME/.local/bin"
echo installed >> "$HOME/.local/readiness-installs"
cat > "$HOME/.local/bin/` + readinessBin + `" <<'TOOL'
#!/bin/bash
echo ran >> "$HOME/.local/readiness-runs"
TOOL
chmod +x "$HOME/.local/bin/` + readinessBin + `"
`
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	if url == "" {
		url = "file:///ctx/packs/" + readinessPack + "/install.sh"
	}
	manifest := `{
  "name": "` + readinessPack + `",
  "description": "the readiness act, from the pack's own installer",
  "contributes": [
    {"kind": "program", "bin": "` + readinessBin + `", "via": "installer", "url": "` + url + `"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	// Named explicitly: a staged pack's directory is named by its source's last path segment,
	// a counter under t.TempDir(), and the installer URL names the directory.
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+readinessPack+`"}]}`)
	return dir
}

// unreachableInstaller is an installer URL on the jail's own loopback, where nothing listens:
// curl's connection is refused, as with no network.
const unreachableInstaller = "http://127.0.0.1:9/install.sh"

// The probe never invokes the program's name, so only the readiness act can have installed it.
// $((40+2)) for nodefloor_test's reason: the Executing banner shows the text, only running it
// prints 42.
const readinessProbe = `if [ -x "$HOME/.local/bin/` + readinessBin + `" ]; then echo READY-$((40+2)); fi; ` +
	`echo "installs=$(wc -l < "$HOME/.local/readiness-installs" 2>/dev/null || echo 0)"; ` +
	`echo "runs=$(wc -l < "$HOME/.local/readiness-runs" 2>/dev/null || echo 0)"`

// §7 items 1 and 2: a fresh workspace's launch installs the declared program before the command,
// without running it, and a second launch of the same home installs nothing and says nothing.
func TestALaunchInstallsTheDeclaredProgramsBeforeItsCommand(t *testing.T) {
	requireJail(t)
	dir := readinessFixture(t, "")

	r := runYolo(t, dir, readinessProbe, withReadiness())
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "READY-42") {
		t.Errorf("the declared program was not installed when the command ran:\n%s%s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "installs=1") || !strings.Contains(r.stdout, "runs=0") {
		t.Errorf("want one install and no run of the program:\n%s", r.stdout)
	}

	r = runYolo(t, dir, readinessProbe, withReadiness())
	if r.rc != 0 {
		t.Fatalf("the second launch failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "installs=1") {
		t.Errorf("the second launch of a warm home installed again:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout+r.stderr, "Installing "+readinessBin) {
		t.Errorf("the second launch of a warm home announced an install:\n%s%s", r.stdout, r.stderr)
	}
}

// OQ-JR1 as ruled: an install that cannot happen STOPS the launch, offline included, naming the
// pack, the program and the error, and the command never runs.
func TestALaunchWhoseProgramCannotInstallIsRefused(t *testing.T) {
	requireJail(t)
	dir := readinessFixture(t, unreachableInstaller)

	r := runYolo(t, dir, `echo TARGET-$((40+2))`, withReadiness())
	all := r.stdout + r.stderr
	if r.rc != provision.RefusedStatus {
		t.Errorf("rc = %d, want provision.RefusedStatus (%d) carried through the container's exit\n%s",
			r.rc, provision.RefusedStatus, all)
	}
	if strings.Contains(all, "TARGET-42") {
		t.Errorf("the command ran after the refusal:\n%s", all)
	}
	for what, want := range map[string]string{
		"the refusal": "REFUSING to start this jail",
		"the program": "program " + readinessBin + " (pack " + readinessPack + ")",
		"the error":   "installer download failed: " + unreachableInstaller,
		"the hatch":   paths.AllowMissingProgramsEnv + "=1 yolo <your command>",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusal does not name %s (%q):\n%s", what, want, all)
		}
	}
}

// The hatch, typed on the host, starts the same jail and lists what it could not install.
func TestTheMissingProgramsHatchStartsTheJail(t *testing.T) {
	requireJail(t)
	dir := readinessFixture(t, unreachableInstaller)

	r := runYolo(t, dir, `echo TARGET-$((40+2))`, withReadiness(),
		withEnv(paths.AllowMissingProgramsEnv+"=1"))
	all := r.stdout + r.stderr
	if r.rc != 0 || !strings.Contains(r.stdout, "TARGET-42") {
		t.Fatalf("rc = %d: %s must start the jail and run the command\n%s", r.rc, paths.AllowMissingProgramsEnv, all)
	}
	for _, want := range []string{
		paths.AllowMissingProgramsEnv + " is set, so this jail starts WITHOUT",
		"program " + readinessBin + " (pack " + readinessPack + ")",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the launch does not say %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "REFUSING") {
		t.Errorf("the hatch still printed the refusal:\n%s", all)
	}
}
