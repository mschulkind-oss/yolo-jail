package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAutoCaptureInstallerStdoutReachesParentLaunchLog exercises the real container,
// ttyproxy and parent-launch-log path. A local installer is deliberately not an agent and makes
// no network request; its stdout must be launch output, not part of the command's stdout payload.
func TestAutoCaptureInstallerStdoutReachesParentLaunchLog(t *testing.T) {
	requireJail(t)
	const bin = "integration-autocapture-stdout-probe"
	pack := t.TempDir()
	manifest := `{"name":"fixture","description":"automatic capture stream fixture","contributes":[{"kind":"program","bin":"` + bin + `","via":"installer","url":"file:///ctx/packs/fixture/install.sh"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	installer := "#!/bin/sh\nset -eu\nprintf '%s\\n' AUTO-CAPTURE-INSTALLER-STDOUT\n" +
		"mkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$HOME/.local/bin/" + bin + "\"\n" +
		"chmod +x \"$HOME/.local/bin/" + bin + "\"\n"
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o755); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs":[{"source":"file://`+filepath.ToSlash(pack)+`","name":"fixture"}]}`)
	store := paths.CapturesDir()
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, bin) })

	workspace := writeProject(t, `{"network":{"mode":"bridge"}}`)
	r := runCommand(t, workspace, append(append(jailRunArgs(), "--"), "sh", "-c", `printf 'COMMAND-OUT\n'`), withEnv("YOLO_NO_AUTO_CAPTURE="))
	if r.rc != 0 {
		t.Fatalf("yolo run rc = %d\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	if r.stdout != "COMMAND-OUT\n" {
		t.Fatalf("command stdout = %q, want exactly COMMAND-OUT\\n; stderr:\n%s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "AUTO-CAPTURE-INSTALLER-STDOUT") {
		t.Fatalf("installer stdout did not reach launcher stderr:\n%s", r.stderr)
	}
	launchLog, err := os.ReadFile(filepath.Join(workspace, ".yolo", "logs", "launch.log"))
	if err != nil {
		t.Fatalf("read parent launch log: %v", err)
	}
	if !strings.Contains(string(launchLog), "AUTO-CAPTURE-INSTALLER-STDOUT") {
		t.Fatalf("installer stdout reached launcher stderr but not parent launch.log:\nlog:\n%s\nstderr:\n%s", launchLog, r.stderr)
	}
}
