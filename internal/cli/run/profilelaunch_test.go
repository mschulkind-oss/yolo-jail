package run

// profilelaunch_test.go pins the DIRECT-INVOCATION half of a pack's launch flags
// (profiles-as-pack-variants.md §3.4): `yolo -- <bin>` must carry the flags of the
// SELECTED variant, not just the pack's static baseline.
//
// The interactive alias learned profiles first (entrypoint/shell.go folds the jail's
// YOLO_USE_PROFILES table), and the host CLI's injection did not — so one pack's variant
// flags appeared on the alias and vanished from a direct invocation. Nothing failed: the
// launch looked normal and ran with the wrong approval posture. That is why the test below
// drives Run() and reads the argv the backend is handed, rather than calling
// packload.InjectLaunchFlags itself — a test on the callee would stay green if Run stopped
// passing the table, which is exactly the defect.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// profileLaunchLocalPack writes the conventional LOCAL pack (paths.LocalPackDir, so no
// `packs` config entry is needed) that installs `acme` with a static launch flag and a
// `bedrock` variant replacing it.
func profileLaunchLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"local","contributes":[` +
		`{"kind":"program","bin":"acme","via":"npm","package":"@acme/acme"},` +
		`{"kind":"launch","bin":"acme","flags":["--static"]},` +
		`{"kind":"profile","name":"bedrock",` +
		`"launch":[{"bin":"acme","flags":["--bedrock"]}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The selected variant's flags reach the command the backend is handed. Asserted through
// Run on the macos-user arm, whose handler seam exposes the argv: the injection sits above
// the backend dispatch, so either backend must see the same result.
func TestRunInjectsTheSelectedProfilesLaunchFlags(t *testing.T) {
	home := packHome(t)
	profileLaunchLocalPack(t, home)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"acme", "user-arg"}
	o.UseProfiles = map[string]string{"acme": "bedrock"}

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, agentArgv []string, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if len(got) != 3 || got[0] != "acme" || got[1] != "--bedrock" || got[2] != "user-arg" {
		t.Fatalf("the command must carry the selected variant's flag and the user's own "+
			"arguments, got %v", got)
	}
}

// Without a selection the static baseline is what reaches the command — the same fixture,
// no profile named, so the variant must stay inert rather than folding anyway.
func TestRunWithoutAProfileSelectionInjectsTheStaticFlags(t *testing.T) {
	home := packHome(t)
	profileLaunchLocalPack(t, home)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"acme"}

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, agentArgv []string, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if len(got) != 2 || got[0] != "acme" || got[1] != "--static" {
		t.Fatalf("an unprofiled launch must inject the static flags, got %v", got)
	}
}
