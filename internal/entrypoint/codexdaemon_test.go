package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// OQ-CDX1 and OQ-CDX2 (docs/research/codex-background-service.md §5): Codex's background server
// — the "daemon", the second copy of Codex that an interactive `codex` starts and then talks to —
// is OFF wherever yolo launches Codex, and a Codex the user runs directly is never touched. The
// key is `features.daemon_auto_start = false` in config.toml (Codex 0.159: FeaturesToml's
// flattened boolean entries, features/src/lib.rs; read by tui/src/startup_orchestration.rs).
//
// It rides the codex pack's AUTONOMOUS posture, the one notch-scoping mechanism
// (docs/design/notch-scoped-config-contributions.md P1): every jail boot renders that posture —
// podman, Apple Container and macos-user alike (render.Jail from Env.renderTarget) — and
// `yolo host apply` renders the guarded one into the user's own ~/.codex. So deleting the key
// from packs/codex/pack.json fails the first test, and moving it into the always-on `managed`
// block fails the second.

// codexFeatures reads the rendered config.toml's [features] table.
func codexFeatures(t *testing.T, home string) map[string]any {
	t.Helper()
	got := decodeCodexTOML(t, filepath.Join(home, ".codex", "config.toml"))
	features, _ := got["features"].(map[string]any)
	return features
}

// THE JAIL HALF, through the boot's own loop, with a user-written [features] key beside it that
// must survive: the managed key is a merge into the table, not a replacement of it.
func TestEveryJailBootTurnsCodexsBackgroundServerOff(t *testing.T) {
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := overlayRenderEnv(t)
	dir := filepath.Join(e.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("[features]\nweb_search_request = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bootJail(t, e, codex)
	features := codexFeatures(t, e.Home)
	if v, ok := features["daemon_auto_start"]; !ok || v != false {
		t.Fatalf("a jail's Codex config does not turn the background server off: "+
			"features.daemon_auto_start = %v (present=%v); want false", v, ok)
	}
	if features["web_search_request"] != true {
		t.Errorf("the managed key replaced the user's own [features] table: %v", features)
	}
}

// `yolo check`'s dry-run probe (ConfigurePackByName) renders what the boot renders.
func TestYoloChecksCodexProbeRendersTheBackgroundServerOff(t *testing.T) {
	e := codexComputedEnv(t, "")
	if err := ConfigurePackByName(e, "codex"); err != nil {
		t.Fatal(err)
	}
	if v := codexFeatures(t, e.Home)["daemon_auto_start"]; v != false {
		t.Fatalf("yolo check's codex probe: features.daemon_auto_start = %v, want false", v)
	}
}

// THE HOST HALF, OQ-CDX2: `yolo host apply` writes the user's OWN ~/.codex/config.toml, which is
// the Codex they run from a terminal. It must never carry the key under `own`, the one writing
// contract since the `assert` retirement (OQ-CO14), whatever else the render writes there.
func TestHostApplyNeverTurnsOffTheUsersOwnCodexBackgroundServer(t *testing.T) {
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	applyHostPacks(t, home, render.OwnershipOwn, false, []*packload.Pack{codex}...)
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("the host render wrote no ~/.codex/config.toml, so this test proves nothing: %v", err)
	}
	if strings.Contains(string(data), "daemon_auto_start") {
		t.Errorf("yolo host apply wrote the daemon key into the user's own Codex config:\n%s", data)
	}
}
