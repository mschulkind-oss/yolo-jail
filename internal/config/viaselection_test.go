package config

// viaselection_test.go pins WG-I11 for config validation (docs/design/wire-bridge-gateway.md):
// the selection name reservation resolves is the launch's, `via` half included, so a pack an
// active via profile adds is reserved exactly as the launch stages it. wire-bridge declares no
// writable dir today, so ValidateConfig cannot show the difference; the selection resolver
// validation calls is asserted directly instead.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeViaSelectionConfig(t *testing.T, body string) {
	t.Helper()
	home := useProfileKeysHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func viaHasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func selectedNames(t *testing.T) []string {
	t.Helper()
	packs, complete := resolveSelectedPacks()
	if !complete {
		t.Fatal("the selection did not resolve completely")
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.Name)
	}
	return names
}

// TestValidationsSelectionIncludesThePackAViaAdds: pi selects nothing that needs the bridge,
// and its active via profile adds it, as it does at launch.
func TestValidationsSelectionIncludesThePackAViaAdds(t *testing.T) {
	writeViaSelectionConfig(t, `{"packs": ["pi", "zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}},
	  "use_profiles": {"pi": "pz"}}`)
	if names := selectedNames(t); !viaHasName(names, "wire-bridge") {
		t.Errorf("validation's selection omits the pack pi's via profile adds: %v", names)
	}
}

// TestValidationsSelectionIgnoresAnInactiveVia is the control: the profile declared but not
// selected, or selected for an agent no pack installs (WG-I10), adds nothing.
func TestValidationsSelectionIgnoresAnInactiveVia(t *testing.T) {
	for name, use := range map[string]string{
		"unselected":   `{}`,
		"absent agent": `{"codex": "pz"}`,
		"no via on it": `{"pi": "plain"}`,
	} {
		writeViaSelectionConfig(t, `{"packs": ["pi", "zai"],
		  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}, "plain": {"provider": "zai"}},
		  "use_profiles": `+use+`}`)
		if names := selectedNames(t); viaHasName(names, "wire-bridge") {
			t.Errorf("%s: the bridge joined validation's selection: %v", name, names)
		}
	}
}
