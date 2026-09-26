package cli

// configpromotevia_test.go pins WG-I11 for `yolo config promote`
// (docs/design/wire-bridge-gateway.md): its fold is the launch's selection, so a pack an
// active via profile adds is in it, in the position the launch appends it.

import (
	"path/filepath"
	"testing"
)

func TestPromoteFoldIncludesThePackAViaAdds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	userCfg(t, home, `{"packs": ["pi", "zai"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}},
	  "use_profiles": {"pi": "pz"}}`)
	fold, unresolved := loadPromoteFold()
	if len(unresolved) != 0 {
		t.Fatalf("unresolved = %+v", unresolved)
	}
	pos, ok := fold.order["wire-bridge"]
	if !ok {
		t.Fatalf("promote's fold omits the pack pi's via profile adds: %v", fold.order)
	}
	if pos < fold.afterConfigured {
		t.Errorf("wire-bridge folds at %d, before the configured entries end (%d) — the launch "+
			"appends a closure-added pack after them", pos, fold.afterConfigured)
	}
}
