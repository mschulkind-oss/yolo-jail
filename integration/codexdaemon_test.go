package integration

// codexdaemon_test.go is the integration tier of OQ-CDX1 (docs/research/codex-background-service.md
// §5): Codex's background server — the second copy of Codex an interactive `codex` starts and
// then talks to — is off in every jail, because the codex pack's autonomous posture writes
// `features.daemon_auto_start = false` into the jail's ~/.codex/config.toml. The unit pin
// (internal/entrypoint/codexdaemon_test.go) drives the boot loop over the shipped pack; only a
// launch proves the staged pack tree, the boot and the per-workspace home carry it through.
// Selecting the pack renders its surfaces and installs no CLI, so no Codex runs.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

func TestAJailLaunchTurnsCodexsBackgroundServerOff(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["codex"]}`)
	if r := runYolo(t, dir, "true"); r.rc != 0 {
		t.Fatalf("launch failed: rc %d\n%s", r.rc, r.combined())
	}
	raw := renderedSurface(t, dir, "codex", "config.toml")
	config, err := tomlx.Decode(raw)
	if err != nil {
		t.Fatalf("decoding the rendered codex config.toml: %v\n%s", err, raw)
	}
	features, _ := config["features"].(map[string]any)
	if v, ok := features["daemon_auto_start"]; !ok || v != false {
		t.Errorf("the jail's codex config.toml leaves Codex's background server on: "+
			"features.daemon_auto_start = %v (present=%v), want false:\n%s", v, ok, raw)
	}
}
