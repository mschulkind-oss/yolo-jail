package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// programs.overrides (docs/design/pack-buildable-programs.md §5, BG-D8) is the user's dev
// checkout for one program. It shares the autoprune key's scope rule and its failure rule:
// a user file this cannot read applies NO override, and a workspace file cannot set one.
// These tests pin the reader and the validator; the launch's use is run's programoverrides.go.

// TestProgramOverridesReadsTheUserConfig: path is ~-expanded and cleaned.
func TestProgramOverridesReadsTheUserConfig(t *testing.T) {
	home := programsHome(t)
	userConfigWith(t, home, `{"programs": {"overrides": {
		"board": {"path": "~/x"},
		"pi":    {"path": "/srv/pi/"}
	}}}`)
	got := ProgramOverrides(nil)
	if len(got) != 2 {
		t.Fatalf("want two overrides, got %d: %+v", len(got), got)
	}
	if want := filepath.Join(home, "x"); got["board"].Path != want {
		t.Errorf("board.Path = %q, want %q (~ expanded)", got["board"].Path, want)
	}
	if got["pi"].Path != "/srv/pi" {
		t.Errorf("pi = %+v, want path /srv/pi (cleaned)", got["pi"])
	}
}

// TestProgramOverridesShapeIsValidated: every malformed shape is an error NAMING the field
// (and so the fix), reached through ValidateConfig, which is what `yolo check` and the
// launch preflight both call.
//
// MUTATION: delete the overrides branch from validatePrograms and every row goes red.
func TestProgramOverridesShapeIsValidated(t *testing.T) {
	programsHome(t)
	ws := t.TempDir()
	cases := []struct{ body, want string }{
		{`{"programs": {"overrides": {"board": {"path": ""}}}}`, "config.programs.overrides.board.path"},
		{`{"programs": {"overrides": {"board": {}}}}`, "config.programs.overrides.board.path"},
		{`{"programs": {"overrides": {"board": {"path": 7}}}}`, "config.programs.overrides.board.path"},
		{`{"programs": {"overrides": {"board": {"path": "rel/x"}}}}`, "config.programs.overrides.board.path"},
		{`{"programs": {"overrides": {"board": {"path": "~bob/x"}}}}`, "config.programs.overrides.board.path"},
		// THE RETIRED `run` (OQ-BG3, BG-D19): named with what replaced it, never as an unknown key.
		{`{"programs": {"overrides": {"board": {"path": "/x", "run": "dist/y"}}}}`, "config.programs.overrides.board.run: no longer read"},
		{`{"programs": {"overrides": {"board": {"path": "/x", "color": "red"}}}}`, "config.programs.overrides.board.color: unknown key"},
		{`{"programs": {"overrides": {"a/b": {"path": "/x"}}}}`, "config.programs.overrides.a/b: program name"},
		{`{"programs": {"overrides": {"board": "/x"}}}`, "config.programs.overrides.board: expected an object"},
		{`{"programs": {"overrides": ["board"]}}`, "config.programs.overrides: expected an object"},
	}
	for _, c := range cases {
		errs, _ := ValidateConfig(decode(t, c.body), ws, nil)
		if !anyContains(errs, c.want) {
			t.Errorf("%s should report %q, got %v", c.body, c.want, errs)
		}
	}
	// The documented spelling validates clean.
	errs, _ := ValidateConfig(decode(t, `{"programs": {"overrides": {"board": {"path": "~/code/board"}}}}`), ws, nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.programs") {
			t.Errorf("the documented override spelling must validate: %s", e)
		}
	}
}

// TestProgramOverridesFailsClosed: a user file this cannot read applies no override at all,
// and one bad entry drops only itself (with a warning), never its valid siblings. A launch
// never runs a checkout the user did not, in full, ask for.
//
// MUTATION: make ProgramOverrides return the entries it could parse from a malformed file,
// or keep an entry it warned about, and this goes red.
func TestProgramOverridesFailsClosed(t *testing.T) {
	home := programsHome(t)
	userConfigWith(t, home, `{"programs": {"overrides": {"board": {"path": `)
	if got := ProgramOverrides(nil); len(got) != 0 {
		t.Errorf("a malformed user file must apply no override, got %+v", got)
	}

	userConfigWith(t, home, `{"programs": {"overrides": {
		"bad":  {"path": "rel/x"},
		"good": {"path": "/srv/good"}
	}}}`)
	var warned []string
	got := ProgramOverrides(func(m string) { warned = append(warned, m) })
	if _, ok := got["bad"]; ok {
		t.Errorf("an invalid entry must not apply: %+v", got["bad"])
	}
	if got["good"].Path != "/srv/good" {
		t.Errorf("a valid sibling must still apply: %+v", got)
	}
	if !anyContains(warned, "programs.overrides.bad") {
		t.Errorf("the dropped entry must be named in a warning, got %v", warned)
	}
}

// TestProgramOverridesIsUserScopeOnly: a workspace config setting overrides is a validation
// error that names the user file, and the reader never consults the workspace.
//
// MUTATION: point ProgramOverrides at the merged config, or delete the `programs` workspace
// check, and the assertions go red.
func TestProgramOverridesIsUserScopeOnly(t *testing.T) {
	programsHome(t)
	ws := t.TempDir()
	writeConfigAt(t, filepath.Join(ws, "yolo-jail.jsonc"),
		`{"programs": {"overrides": {"board": {"path": "/srv/board"}}}}`)
	t.Chdir(ws)

	if got := ProgramOverrides(nil); len(got) != 0 {
		t.Errorf("a WORKSPACE CONFIG applied an override: %+v", got)
	}
	cfg := decode(t, `{"programs": {"overrides": {"board": {"path": "/srv/board"}}}}`)
	errs, _ := ValidateConfig(cfg, ws, nil)
	if !anyContains(errs, "user-scope only") || !anyContains(errs, paths.UserConfigDisplayPath()) {
		t.Errorf("a workspace override must be an ERROR naming the user config file: %v", errs)
	}
}

// TestProgramOverridesAreNotInherited: a nested jail's generated user files carry no
// programs key, so an inner launch cannot inherit an outer checkout (BG-D15).
//
// MUTATION: classify `programs` as inherited in inherit.go and this goes red.
func TestProgramOverridesAreNotInherited(t *testing.T) {
	effective := decode(t, `{"programs": {"autoprune": true, "overrides": {"board": {"path": "/srv/board"}}}}`)
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		out, _ := FilterInherit(effective, scope)
		if _, ok := out.Get("programs"); ok {
			t.Errorf("scope %v inherited programs (overrides must never cross a jail boundary)", scope)
		}
	}
}

// THE PREBUILT OPT-IN (BG-D18, OQ-BG2): `programs.overrides.<bin>: {"prebuilt": true}` is read by
// ProgramPrebuiltOptIns and never by ProgramOverrides, since it names no checkout to run; it is
// user scope only, like every `programs` key; and it cannot sit beside `path` in one
// entry, the validation error naming both fields.
//
// MUTATION: drop prebuiltKey from parseProgramOverride, or have ProgramOverrides keep a Prebuilt
// entry, and this goes red.
func TestProgramPrebuiltOptInIsReadValidatedAndExclusive(t *testing.T) {
	home := programsHome(t)
	userConfigWith(t, home, `{"programs": {"overrides": {
		"chart": {"prebuilt": true},
		"board": {"path": "/srv/board"}
	}}}`)
	if got := ProgramPrebuiltOptIns(nil); len(got) != 1 || !got["chart"] {
		t.Errorf("ProgramPrebuiltOptIns = %v, want chart alone", got)
	}
	if got := ProgramOverrides(nil); len(got) != 1 || got["board"].Path != "/srv/board" {
		t.Errorf("ProgramOverrides = %+v, want board alone: a prebuilt opt-in is no dev override", got)
	}

	ws := t.TempDir()
	for body, want := range map[string]string{
		`{"programs": {"overrides": {"chart": {"prebuilt": true, "path": "/x"}}}}`: `"prebuilt" and "path" cannot both be set`,
		`{"programs": {"overrides": {"chart": {"prebuilt": "yes"}}}}`:              "config.programs.overrides.chart.prebuilt: expected true or false",
	} {
		errs, _ := ValidateConfig(decode(t, body), ws, nil)
		if !anyContains(errs, want) {
			t.Errorf("%s should report %q, got %v", body, want, errs)
		}
	}
	errs, _ := ValidateConfig(decode(t, `{"programs": {"overrides": {"chart": {"prebuilt": true}}}}`), ws, nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.programs") {
			t.Errorf("the documented opt-in spelling must validate: %s", e)
		}
	}

	// Both forms in one entry are dropped from both readers, with a warning.
	userConfigWith(t, home, `{"programs": {"overrides": {"chart": {"prebuilt": true, "path": "/x"}}}}`)
	var warned []string
	if got := ProgramPrebuiltOptIns(func(m string) { warned = append(warned, m) }); len(got) != 0 {
		t.Errorf("an entry with both prebuilt and path opted in: %v", got)
	}
	if !anyContains(warned, "programs.overrides.chart") {
		t.Errorf("the dropped entry must be named in a warning, got %v", warned)
	}

	// A workspace config is never read.
	userConfigWith(t, home, `{}`)
	writeConfigAt(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"programs": {"overrides": {"chart": {"prebuilt": true}}}}`)
	t.Chdir(ws)
	if got := ProgramPrebuiltOptIns(nil); len(got) != 0 {
		t.Errorf("a WORKSPACE CONFIG opted a program into its prebuilt: %v", got)
	}
}
