package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// programs_test.go pins the `programs.autoprune` option: OFF by default, USER SCOPE ONLY, and
// validated rather than silently ignored. It is the knob that lets a boot DELETE installed
// programs, so every one of those is a safety property and not a nicety (OQ-PD4, §9 R3).

// programsHome isolates the user config this key is read from.
func programsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func writeConfigAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func userConfigWith(t *testing.T, home, body string) {
	t.Helper()
	writeConfigAt(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), body)
}

// DEFAULT OFF, and every way of being absent or unreadable is off too. This is the ruled
// default (OQ-PD4) and it is what makes the feature safe to ship at all.
//
// MUTATION: make ProgramsAutoprune return true on a parse failure, or default the missing key
// to true, and this goes red.
func TestProgramsAutopruneDefaultsOff(t *testing.T) {
	for name, body := range map[string]string{
		"no user config at all": "",
		"no programs key":       `{"packages": []}`,
		"empty programs object": `{"programs": {}}`,
		"explicitly false":      `{"programs": {"autoprune": false}}`,
		"not a boolean":         `{"programs": {"autoprune": "yes"}}`,
		"not an object":         `{"programs": true}`,
		"malformed json":        `{"programs": {`,
	} {
		t.Run(name, func(t *testing.T) {
			home := programsHome(t)
			if body != "" {
				userConfigWith(t, home, body)
			}
			if ProgramsAutoprune(nil) {
				t.Errorf("autoprune must be OFF for %s — a config this cannot read is "+
					"never a licence to delete a binary", name)
			}
		})
	}
}

// ...and ON when the user's own config says so, which is the other half of "an option".
func TestProgramsAutopruneReadsTheUserConfig(t *testing.T) {
	home := programsHome(t)
	userConfigWith(t, home, `{"programs": {"autoprune": true}}`)
	if !ProgramsAutoprune(nil) {
		t.Error("a user config asking for autoprune must turn it on")
	}
}

// USER SCOPE ONLY. A workspace config travels with the repo and is agent-editable; this key
// authorises deleting binaries out of the jail's home, so a repo must not be able to set it.
// Both halves are asserted, because either one alone is a hole: the LOADER must ignore it (or
// a repo sets it for real) and validation must REPORT it (or the user's setting is a silent
// no-op that looks like a broken feature).
//
// MUTATION: point ProgramsAutoprune at the merged config instead of the user file and the
// first half goes red; delete the validatePrograms call from ValidateConfig and the second
// does.
func TestProgramsAutopruneIsUserScopeOnly(t *testing.T) {
	home := programsHome(t)
	ws := t.TempDir()
	writeConfigAt(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"programs": {"autoprune": true}}`)
	// THE WORKSPACE IS THE CWD (`yolo config-ref`: there is no --workspace flag), so this
	// is what makes the assertion real: a reader that consulted the MERGED config — the
	// obvious way to write this function — would find the key here and answer true.
	t.Chdir(ws)

	if ProgramsAutoprune(nil) {
		t.Error("A WORKSPACE CONFIG TURNED ON AUTOPRUNE — a repo-committed, agent-editable " +
			"file must not be able to authorise deleting the user's programs")
	}

	cfg := decode(t, `{"programs": {"autoprune": true}}`)
	errs, _ := ValidateConfig(cfg, ws, nil)
	if !anyContains(errs, "user-scope only") {
		t.Errorf("a workspace-scoped `programs` must be an ERROR, not a silent no-op: %v", errs)
	}
	_ = home
}

// The shape is validated, so a typo is a message rather than a knob the user believes they
// set. Driven through ValidateConfig — the caller both `yolo check` and the launch preflight
// use — so the test fails if the validator is unwired.
func TestProgramsShapeIsValidated(t *testing.T) {
	programsHome(t)
	ws := t.TempDir()
	for body, want := range map[string]string{
		`{"programs": {"autoprun": true}}`:  "unknown key",
		`{"programs": {"autoprune": "on"}}`: "expected true or false",
		`{"programs": ["autoprune"]}`:       "expected an object",
	} {
		errs, _ := ValidateConfig(decode(t, body), ws, nil)
		if !anyContains(errs, want) {
			t.Errorf("%s should report %q, got %v", body, want, errs)
		}
	}
	// ...and the valid spelling validates clean.
	errs, _ := ValidateConfig(decode(t, `{"programs": {"autoprune": true}}`), ws, nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.programs") {
			t.Errorf("the documented spelling must validate: %s", e)
		}
	}
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
