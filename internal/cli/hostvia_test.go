package cli

// hostvia_test.go pins WG-I12 (docs/design/wire-bridge-gateway.md): `via` is inert at the
// host notch even when the user lists wire-bridge in `packs` explicitly, which gives
// ResolveProfiles a via_address to resolve there. No jail daemon runs on a host, so neither
// the host's env derive nor the footer's tables may carry a via URL.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostViaHome selects wire-bridge explicitly, plus a local pack whose agent's env derive
// reports ctx.via_url as VIA_URL and always sets DERIVE_RAN, so an absent VIA_URL is known
// to be the derive's input rather than a derive that never ran. The agent's active profile
// routes it through wire-bridge.
func hostViaHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"viaagent","contributes":[` +
		`{"kind":"program","bin":"viaagent","via":"npm","package":"@example/viaagent"},` +
		`{"kind":"provider","name":"up","endpoints":{"openai":{"base_url":"https://up.example/v1"}}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"), []byte(
		`yolo.env("viaagent", function(ctx)
  return { VIA_URL = ctx.via_url or "", DERIVE_RAN = "1" }
end)`), 0o644); err != nil {
		t.Fatal(err)
	}
	userCfg(t, home, `{
	  "packs": ["wire-bridge"],
	  "profiles": {"pv": {"provider": "up", "via": "wire-bridge"}},
	  "use_profiles": {"viaagent": "pv"}
	}`)
}

// TestHostEnvDeriveGetsNoViaURL: `yolo host -- viaagent` composes the agent's env with no
// via URL, though its profile names wire-bridge and wire-bridge is selected.
func TestHostEnvDeriveGetsNoViaURL(t *testing.T) {
	hostViaHome(t)
	env, _, err := composeHostEnv("viaagent", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	for _, kv := range env {
		if kv == "DERIVE_RAN=1" {
			ran = true
		}
		if strings.HasPrefix(kv, "VIA_URL=") && kv != "VIA_URL=" {
			t.Errorf("the host notch handed the env derive a via URL nothing serves there: %s", kv)
		}
	}
	if !ran {
		t.Fatal("the agent's env derive did not run, so the via URL was never asked about")
	}
}

// TestHostFooterTablesCarryNoViaAddress: the footer's profile table names the via and no
// address for it, the table `yolo host env` composes.
func TestHostFooterTablesCarryNoViaAddress(t *testing.T) {
	hostViaHome(t)
	tables := hostFooterTables()
	if !strings.Contains(tables.Profiles, `"_via"`) {
		t.Fatalf("the footer's profile table lost the via itself: %s", tables.Profiles)
	}
	if strings.Contains(tables.Profiles, "_via_base") {
		t.Errorf("the footer's profile table carries a via address at the host notch: %s", tables.Profiles)
	}
}
