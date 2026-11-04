package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// mergedEnabled is the composed config's `loopholes.<name>.enabled`, or nil.
func mergedEnabled(t *testing.T, ws, name string) any {
	t.Helper()
	cfg, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	lp, _ := cfg.Get("loopholes")
	block, _ := asMap(lp)
	if block == nil {
		return nil
	}
	ev, _ := block.Get(name)
	entry, _ := asMap(ev)
	if entry == nil {
		return nil
	}
	v, _ := entry.Get("enabled")
	return v
}

// TestAWorkspaceCannotTurnOnAUserOffSwitch: LC-D3's off is the user's to lift. A workspace
// config's `enabled: true` (a file the workspace's agent can edit) over a user-scope
// `enabled: false` on an off-switch loophole leaves it off. A workspace's own `enabled: false`
// over a user `true` turns it off: between the user and the workspace, false wins in either
// order. The per-workspace file is the human's own user-scope switch for this one workspace
// (`yolo loopholes enable`), so its `true` lifts the user's off there.
func TestAWorkspaceCannotTurnOnAUserOffSwitch(t *testing.T) {
	cases := []struct {
		name, user, ws, wsFile string
		want                   bool
	}{
		{"workspace config true", `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": false, "settings": {"profile": "p"}}}}`,
			`{"loopholes": {"aws-auth": {"enabled": true}}}`, "", false},
		{"per-workspace file true", `{"packs": ["cgroup-delegate"], "loopholes": {"cgroup-delegate": {"enabled": false}}}`,
			`{}`, `{"workspace": "WS", "loopholes": {"cgroup-delegate": {"enabled": true}}}`, true},
		{"workspace false over user true", `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "p"}}}}`,
			`{"loopholes": {"aws-auth": {"enabled": false}}}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, userCfg, ws := workspaceFileHome(t, "app")
			write(t, userCfg, c.user)
			write(t, filepath.Join(ws, "yolo-jail.jsonc"), c.ws)
			if c.wsFile != "" {
				write(t, WorkspaceFilePath(ws), strings.ReplaceAll(c.wsFile, "WS", ws))
			}
			name := "aws-auth"
			if strings.Contains(c.user, "cgroup-delegate") {
				name = "cgroup-delegate"
			}
			if v := mergedEnabled(t, ws, name); v != c.want {
				t.Errorf("composed loopholes.%s.enabled = %v, want %t", name, v, c.want)
			}
		})
	}
}

// TestTheGatesComposeKeepsAUserOffSwitchOff: the config-change gate composes the user scope under
// a workspace read it already holds (ComposeWithWorkspaceRead), and a workspace `enabled: true`
// there cannot lift a user-scope off either.
func TestTheGatesComposeKeepsAUserOffSwitchOff(t *testing.T) {
	_, userCfg, ws := workspaceFileHome(t, "app")
	write(t, userCfg, `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"enabled": false}}}`)
	wsCfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	block := jsonx.NewOrderedMap()
	block.Set("aws-auth", entry)
	wsCfg.Set("loopholes", block)
	cfg, err := ComposeWithWorkspaceRead(ws, wsCfg)
	if err != nil {
		t.Fatal(err)
	}
	lp, _ := cfg.Get("loopholes")
	b, _ := asMap(lp)
	e, _ := asMap(mapGet(b, "aws-auth"))
	if v := mapGet(e, "enabled"); v != false {
		t.Errorf("the gate's compose lifted the user's off: enabled = %v", v)
	}
}

// TestAWorkspaceOffSwitchIsDisclosedAsOff: a workspace `enabled: false` on an off-switch loophole
// is honored (LC-D3), so validation says it keeps the loophole off for this workspace, not that
// it switches nothing, and offers no `yolo request` (aws-auth has no grant kinds). A workspace
// `enabled: true` says it cannot run a loophole the user scope keeps off, and is otherwise inert.
func TestAWorkspaceOffSwitchIsDisclosedAsOff(t *testing.T) {
	resolver := fakeResolver{"aws-auth": {Name: "aws-auth", HasHostDaemon: true}}
	for _, c := range []struct {
		name, ws, want string
	}{
		{"false", `{"loopholes": {"aws-auth": {"enabled": false}}}`, "keeps the loophole 'aws-auth' off for jails launched from this workspace"},
		{"true", `{"loopholes": {"aws-auth": {"enabled": true}}}`, "cannot turn the loophole 'aws-auth' on"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, userCfg, ws := workspaceFileHome(t, "app")
			write(t, userCfg, `{"packs": ["aws-auth"], "loopholes": {"aws-auth": {"settings": {"profile": "p"}}}}`)
			write(t, filepath.Join(ws, "yolo-jail.jsonc"), c.ws)
			cfg, err := LoadConfig(ws, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, warns := ValidateConfig(cfg, ws, resolver)
			all := strings.Join(warns, "\n")
			if !strings.Contains(all, c.want) || strings.Contains(all, "switches nothing") || strings.Contains(all, "yolo request aws-auth") {
				t.Errorf("want %q and no switches-nothing line, got:\n%s", c.want, all)
			}
		})
	}
}

// TestAMisspelledOffSwitchIsRefusedAtLaunch: AllowOff reads only a boolean `off` under a pack that
// ships an off-switch loophole, so anything else would leave the loophole running while the file
// says off. The launch's pack-switch check (AllowPackSwitchErrors, which `yolo check` renders
// too) refuses a non-boolean `off`, and an `off` under a pack with no off-switch loophole, naming
// the table that has one where a needed pack does.
func TestAMisspelledOffSwitchIsRefusedAtLaunch(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"wrong table", "packs = [\"claude\"]\n\n[allow.claude]\noff = true\n",
			"allow.claude.off: claude has no loophole an off switch turns off. claude-auth, which a listed pack needs, has one: move the line under [allow.claude-auth], and delete [allow.claude] if it is then empty"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := layerHome(t, "")
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), c.body)
			errs := strings.Join(AllowPackSwitchErrors(), "\n")
			if !strings.Contains(errs, c.want) {
				t.Errorf("want %q, got:\n%s", c.want, errs)
			}
		})
	}
	// A non-boolean `off` is refused where the user config is read, so every reader (a launch,
	// `yolo check`) refuses it, with the fix.
	home := layerHome(t, "")
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "packs = [\"claude\"]\n\n[allow.claude-auth]\noff = \"yes\"\n")
	_, err := LoadConfig(t.TempDir(), true, nil)
	want := "config.allow.claude-auth.off: expected a boolean (got \"yes\"); write `off = true` to keep the pack's loophole off, or delete the line to let it run"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a non-boolean off: err = %v, want %q", err, want)
	}
}
