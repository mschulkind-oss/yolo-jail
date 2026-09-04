package run

// profilelaunch_test.go pins the DIRECT-INVOCATION half of a pack's launch flags
// (profiles-as-pack-variants.md §3.4): `yolo -- <bin>` and the interactive alias must
// carry the SAME flags for the same launch.
//
// The history is why this drives Run() and reads the argv the backend is handed, rather
// than calling packload.InjectLaunchFlags itself: the interactive alias learned profiles
// first and the host CLI's injection did not, so one pack's variant flags appeared on the
// alias and vanished from a direct invocation — and nothing failed, because a test on the
// callee stays green when Run stops passing the table. That defect is exactly the shape
// the OQ-PT8 shrink left behind in reverse: a profile used to CARRY flags, and both
// channels folded them; now no kind carries them (a `launch` with `profile` set is
// refused at the schema — nothing consumes it), so the pin is the weaker and sufficient
// fact that a SELECTED profile injects nothing the static baseline does not have. If a
// profile ever grows flags again, one of these two tests is where it shows up first.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// profileLaunchLocalPack writes the conventional LOCAL pack (paths.LocalPackDir, so no
// `packs` config entry is needed) that installs `acme` with one static launch flag, plus
// a profile declaration that contributes no flag of its own — the shrunken kind's whole
// point.
func profileLaunchLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"local","contributes":[` +
		`{"kind":"program","bin":"acme","via":"npm","package":"@acme/acme"},` +
		`{"kind":"launch","bin":"acme","flags":["--static"]},` +
		`{"kind":"profile","name":"bedrock","provider":"bedrock"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The selected profile's flag list is the static baseline: a profile is a selection, so
// the argv it produces is the pack's own. Asserted through Run on the macos-user arm,
// whose handler seam exposes the argv.
func TestRunInjectsNoFlagForASelectedProfile(t *testing.T) {
	home := packHome(t)
	profileLaunchLocalPack(t, home)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"acme", "user-arg"}
	o.UseProfiles = map[string]string{"acme": "bedrock"}

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, agentArgv []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
		got = agentArgv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if len(got) != 3 || got[0] != "acme" || got[1] != "--static" || got[2] != "user-arg" {
		t.Fatalf("a selected profile contributes no flag, so the argv must be the static "+
			"baseline plus the user's own arguments, got %v", got)
	}
}

// Without a selection the same baseline reaches the command — the same fixture, no
// profile named. The two tests together are the parity claim: selecting a profile and not
// selecting one cannot change the flags, only the provider env and config.
func TestRunWithoutAProfileSelectionInjectsTheStaticFlags(t *testing.T) {
	home := packHome(t)
	profileLaunchLocalPack(t, home)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"acme"}

	var got []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, agentArgv []string, _, _, _ string, _ bool, _ *jsonx.OrderedMap) int {
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
