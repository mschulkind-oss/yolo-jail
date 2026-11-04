package configmigrate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// removedsetupkeys_test.go pins what `yolo config migrate` does with the keys named setups replace
// (named-setups.md §10; SUP-D15): a user file's value naming one setup is written as `setup`, the
// old default `confinement = "jail"` is dropped as inert, and every other value, and every value a
// project file holds, is left out naming its refusal's next step. Each case runs Plan, so deleting
// lowerRemovedSetupKey's case in lower fails it (the key then falls to its rename row, unconverted).

func planOne(t *testing.T, userCfg, wsCfg string) *MigrationPlan {
	t.Helper()
	p, err := planOneOrRefusal(t, userCfg, wsCfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return p
}

// planOneOrRefusal is the plan, or Plan's refusal and the plan it refused.
func planOneOrRefusal(t *testing.T, userCfg, wsCfg string) (*MigrationPlan, error) {
	t.Helper()
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	if userCfg != "" {
		write(t, filepath.Join(dir, "config.jsonc"), userCfg)
	}
	if wsCfg != "" {
		write(t, filepath.Join(ws, "yolo-jail.jsonc"), wsCfg)
	}
	g, err := Discover(Request{UserConfigDir: dir, Workspace: ws})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	p, err := Plan(g)
	var refusal *InequalityError
	if errors.As(err, &refusal) {
		p = refusal.Plan
	} else if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return p, err
}

func targetText(p *MigrationPlan) string {
	var b strings.Builder
	for _, tg := range p.Targets {
		b.Write(tg.Bytes)
	}
	return b.String()
}

func TestMigrateWritesTheOneSetupTheOldValueMeant(t *testing.T) {
	for _, tc := range []struct{ cfg, want string }{
		{`{"runtime": "podman"}`, "podman-self"},
		{`{"runtime": "container"}`, "apple-container-self"},
		{`{"runtime": "container", "confinement": "jail"}`, "apple-container-self"},
	} {
		t.Run(tc.cfg, func(t *testing.T) {
			p := planOne(t, tc.cfg, "")
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			text := targetText(p)
			if !strings.Contains(text, "setup = ") || !strings.Contains(text, tc.want) {
				t.Errorf("the converted user config does not write setup %s:\n%s", tc.want, text)
			}
			if strings.Contains(text, "runtime") {
				t.Errorf("the converted user config still writes runtime:\n%s", text)
			}
		})
	}
}

func TestMigrateDropsTheDefaultConfinement(t *testing.T) {
	p := planOne(t, `{"confinement": "jail", "kvm": false}`, "")
	if len(p.NotConverted) > 0 {
		t.Fatalf("not converted: %+v", p.NotConverted)
	}
	dropped := false
	for _, tg := range p.Targets {
		for _, d := range tg.Inert {
			dropped = dropped || d.Path == "confinement"
		}
	}
	if !dropped {
		t.Errorf("confinement = jail was not reported as dropped")
	}
	if text := targetText(p); strings.Contains(text, "confinement") || strings.Contains(text, "setup") {
		t.Errorf("the converted user config writes the dropped default:\n%s", text)
	}
}

func TestMigrateLeavesAValueNamingNoOneSetupNamingTheVerb(t *testing.T) {
	for _, tc := range []struct{ user, ws, key, want string }{
		{`{"confinement": "guest"}`, "", "confinement", "yolo config set setup seatbelt-yolo-account --user"},
		{`{"runtime": "macos-user"}`, "", "runtime", "seatbelt-self"},
		{`{"macos_user_mode": "self"}`, "", "macos_user_mode", "yolo config set setup seatbelt-self --user"},
		{`{"confinement": "host"}`, "", "confinement", "yolo host -- <cmd>"},
		{"", `{"runtime": "podman"}`, "runtime", "yolo config unset runtime --project"},
	} {
		t.Run(tc.key+" "+tc.user+tc.ws, func(t *testing.T) {
			// The plan refuses (nothing converts what the key said); its refusal lists the key.
			p, err := planOneOrRefusal(t, tc.user, tc.ws)
			if err == nil {
				t.Fatalf("migrate converted %s+%s with nothing left out", tc.user, tc.ws)
			}
			for _, unrelated := range []string{"setup\n", "merged: setup"} {
				if strings.Contains(err.Error(), unrelated) {
					t.Errorf("the refusal names a setup the old config never chose:\n%v", err)
				}
			}
			var reason string
			for _, nc := range p.NotConverted {
				if nc.Path == tc.key {
					reason = nc.Reason
				}
			}
			if !strings.Contains(reason, tc.want) {
				t.Errorf("%s: not-converted reason %q, want it to name %q (all: %+v)", tc.key, reason, tc.want, p.NotConverted)
			}
		})
	}
}
