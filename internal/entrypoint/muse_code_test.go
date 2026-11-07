package entrypoint

// muse_code_test.go pins the two declarations that make Meta's Muse Code usable in a jail:
// the workspace-trust store it reads before the model runs, and the §4.2 autonomy posture —
// Muse's own `--yolo` on its interactive TUI, and nothing in front of its subcommands.
//
// EVERY ASSERTION IS ON THE RENDERED RESULT OR THE DISPATCHED ARGV, not on a manifest field,
// so deleting either declaration from packs/muse-code/pack.toml turns them red:
//
//   - the trust test renders the shipped pack through the boot's own loop
//     (ConfigurePackByName → renderPackSet) into a real home and reads the file Muse reads;
//   - the autonomy tests drive packload.InjectLaunchFlags, the function every carrier folds
//     through, at the jail AND host notches, and run the GENERATED launcher against a fake
//     `muse` to read the argv its exec line actually passes.
//
// Without the trust surface Muse's interactive TUI parks on "Do you trust this workspace?"
// (measured: the dialog opens and `muse exec` prints `workspace is untrusted`); without the
// autonomy posture it stalls on the first tool-approval prompt; and with the posture prefixed
// to a subcommand Muse refuses the whole command (`invalid TUI options: unknown argument
// 'exec'`, measured against 1.4.14).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

// TestMuseCodeTrustStoreRendersTheWorkspaceTrusted renders the shipped muse-code pack at the
// jail notch and requires `~/.config/muse/trust.json` to hold a `trusted` entry for the
// render-time workspace — the exact shape Muse Code 1.4.7 was measured to accept
// (`muse: workspace trust: trusted source=user-config`).
func TestMuseCodeTrustStoreRendersTheWorkspaceTrusted(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}
	withCtxRoot(t, t.TempDir(), "muse")

	if err := ConfigurePackByName(e, "muse-code"); err != nil {
		t.Fatalf("rendering muse-code: %v", err)
	}

	path := filepath.Join(home, ".config", "muse", "trust.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Muse's trust store was NOT rendered at %s — the trust dialog will park every "+
			"launch: %v", path, err)
	}
	if strings.Contains(string(data), agentcfg.WorkspacePlaceholder) {
		t.Errorf("the ${workspace} placeholder reached the trust store instead of the jail root:\n%s", data)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("trust store is not JSON: %v\n%s", err, data)
	}
	if got["schema_version"] != float64(1) {
		t.Errorf("schema_version = %v, want 1 — Muse refuses to start without it: %s",
			got["schema_version"], data)
	}
	// MUSE REJECTS A FLOAT VERSION, so the byte-level form is load-bearing and a JSON-unmarshal
	// comparison would not catch it (`1` and `1.0` both decode to float64(1) here).
	// `{"schema_version": 1.0}` is `invalid type: floating point `1.0`, expected u32` — exit 1,
	// worse than the dialog this surface exists to clear. This is why the surface is `stateful`
	// and not claude's `rmw`.
	if !strings.Contains(string(data), `"schema_version": 1
`) || strings.Contains(string(data), `"schema_version": 1.0`) {
		t.Errorf("schema_version must be rendered as the INTEGER 1 Muse accepts, not `1.0`:\n%s", data)
	}
	projects, _ := got["projects"].(map[string]any)
	entry, _ := projects[ws].(map[string]any)
	if entry["decision"] != "trusted" {
		t.Errorf("projects[%q].decision = %v, want %q — Muse refuses the exact shape otherwise\nfile: %s",
			ws, entry["decision"], "trusted", data)
	}
}
