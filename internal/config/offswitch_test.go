package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// offSwitchUserTOML writes a TOML user config selecting git (a broker, whose readable policy
// resolves the whole `allow` table) beside the given packs and tables.
func offSwitchUserTOML(t *testing.T, packs, tables string) {
	t.Helper()
	home := layerHome(t, "")
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = [\"git\", "+packs+"]\n\n[allow.git]\nalways = [\"fetch\"]\n\n"+tables)
}

// TestATOMLOffSwitchIsAccepted: `off = true` under a selected pack's `[allow.<pack>]` table is the
// TOML spelling of LC-D3's off switch for the pack's off-switch loophole, so a TOML user config
// carrying it, beside a setting or alone in a settingless pack's table, resolves: the git
// broker's readable policy, which reads every `allow` table, still holds its read grant. Refusing
// it would take the user's broker grants down with it.
func TestATOMLOffSwitchIsAccepted(t *testing.T) {
	offSwitchUserTOML(t, `"aws-auth", "cgroup-delegate", "claude"`,
		"[allow.aws-auth]\nprofile = \"example-profile\"\noff = true\n\n"+
			"[allow.cgroup-delegate]\noff = true\n\n[allow.claude-auth]\noff = true\n")
	kinds, canonical, err := BrokeredGrantKindsUsingReadablePolicy("git")
	if err != nil || !canonical {
		t.Fatalf("the off switch broke the git broker's readable policy: kinds %v, canonical %t, err %v", kinds, canonical, err)
	}
}

// TestATOMLOffOnAPackWithNoOffSwitchIsRefused: `off` is reserved only where its pack ships an
// off-switch loophole; on any other pack it is an undeclared key, as before.
func TestATOMLOffOnAPackWithNoOffSwitchIsRefused(t *testing.T) {
	offSwitchUserTOML(t, `"journal"`, "[allow.journal]\noff = true\n")
	if _, _, err := BrokeredGrantKindsUsingReadablePolicy("git"); err == nil || !strings.Contains(err.Error(), "allow.journal.off") {
		t.Errorf("allow.journal.off must be refused naming the key, got %v", err)
	}
}

// TestATOMLOffMustBeABoolean: the switch is `true` or `false`; anything else is refused.
func TestATOMLOffMustBeABoolean(t *testing.T) {
	offSwitchUserTOML(t, `"aws-auth"`, "[allow.aws-auth]\noff = \"yes\"\n")
	if _, _, err := BrokeredGrantKindsUsingReadablePolicy("git"); err == nil || !strings.Contains(err.Error(), "allow.aws-auth.off") {
		t.Errorf("a non-boolean allow.aws-auth.off must be refused naming the key, got %v", err)
	}
}

// TestAnAllowTableForANeededPackIsSelected: claude-auth runs whenever claude is selected (claude's
// `needs`), so `[allow.claude-auth] off = true` under `packs = ["claude"]` names a selected pack,
// and the launch's pack-switch check (AllowPackSwitchErrors) does not refuse it. A table for a pack
// nothing selects still is.
func TestAnAllowTableForANeededPackIsSelected(t *testing.T) {
	home := layerHome(t, "")
	path := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write(t, path, "packs = [\"claude\"]\n\n[allow.claude-auth]\noff = true\n")
	if errs := AllowPackSwitchErrors(); len(errs) != 0 {
		t.Errorf("a table for a pack claude needs is refused: %v", errs)
	}
	write(t, path, "packs = [\"journal\"]\n\n[allow.claude-auth]\noff = true\n")
	if errs := AllowPackSwitchErrors(); len(errs) != 1 || !strings.Contains(errs[0], "allow.claude-auth names a pack that is not selected") {
		t.Errorf("a table for a pack nothing selects must still be refused, got %v", errs)
	}
}
