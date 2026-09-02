package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// profiles_test.go pins the `profiles` key's two guarantees: USER SCOPE ONLY, by
// construction (OQ-CS5), and an entry shape narrow enough that a profile can only ever
// be a provider plus free string options (§5.2 property 3, OQ-CS7's "core validates no
// values").

// TestLoadProfilesIgnoresWorkspaceScopeByConstruction is the packs_test.go test of the
// same name, pointed at the second key that reads user scope directly. The guarantee is
// CONSTRUCTION, not validation: LoadProfiles is handed no merged config, so a workspace
// value cannot reach it even if every validator were deleted.
func TestLoadProfilesIgnoresWorkspaceScopeByConstruction(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"profiles": {"zai-fast": {"provider": "zai", "model": "fast"}}}`)
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"profiles": {"from-workspace": {"provider": "zai"}}}`)

	got, err := LoadProfiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("profiles = %v, want exactly the user entry", got)
	}
	p, ok := got["zai-fast"]
	if !ok {
		t.Fatalf("want the USER profile zai-fast, got %v", got)
	}
	if p.Provider != "zai" || p.Options["model"] != "fast" {
		t.Errorf("entry lowered wrong: %+v", p)
	}
	if _, ok := got["from-workspace"]; ok {
		t.Errorf("the workspace entry must not be readable at user scope, got %v", got)
	}
}

// A `profiles` key in workspace scope is a hard ERROR naming the move, not a silently
// inert key — the validatePacks shape, because the two keys are one boundary.
func TestValidateProfilesRejectsWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"profiles": {"zai": {"provider": "zai"}}}`)

	errs, _ := ValidateConfig(decode(t, `{}`), ws, nil)
	found := ""
	for _, e := range errs {
		if strings.HasPrefix(e, "config.profiles") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("no config.profiles error; got %v", errs)
	}
	for _, want := range []string{"user-scope only", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(found, want) {
			t.Errorf("error %q missing %q", found, want)
		}
	}
}

// TestValidateProfilesRejectsWorkspaceScopeForBothKeys is OQ-CS5's ruling as an
// assertion: USER SCOPE ONLY, BOTH keys. `profiles` declares what a profile is and
// `use_profiles` switches one on, and the second is the dangerous half to have missed —
// it is read off the MERGED config by the launch, so a workspace spelling did not sit
// inert the way a workspace `profiles` does, it took effect. Same refusal both ways, and
// the message names the file to move it to, because that is the whole fix.
func TestValidateProfilesRejectsWorkspaceScopeForBothKeys(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"profiles": {"zai": {"provider": "zai"}}, "use_profiles": {"claude": "zai"}}`)

	errs, _ := ValidateConfig(decode(t, `{}`), ws, nil)
	for _, key := range []string{profilesKey, useProfilesKey} {
		found := ""
		for _, e := range errs {
			if strings.HasPrefix(e, "config."+key+":") {
				found = e
			}
		}
		if found == "" {
			t.Errorf("no config.%s error; got %v", key, errs)
			continue
		}
		for _, want := range []string{"user-scope only", "~/.config/yolo-jail/config.jsonc"} {
			if !strings.Contains(found, want) {
				t.Errorf("config.%s error %q missing %q", key, found, want)
			}
		}
		// ONE message for the pair, the key name swapped: the two keys are one boundary,
		// and two wordings of a boundary is how the boundary stops being one.
		if !strings.Contains(found, "a profile steers the endpoint and the model an agent uses") {
			t.Errorf("config.%s error is not the shared user-scope text: %q", key, found)
		}
	}
}

// TestValidateProfilesAcceptsBothKeysAtUserScope is the inverse pin: the ruling limits
// SCOPE, not the keys. A user config may declare a profile and select it in the same
// file, and nothing here may refuse that — the launch is where the declared name is
// resolved, not where the user is told they cannot have one.
func TestValidateProfilesAcceptsBothKeysAtUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"profiles": {"zai-fast": {"provider": "zai", "model": "fast"}},`+
			` "use_profiles": {"claude": "zai-fast"}}`)

	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "profiles") {
			t.Errorf("both keys at user scope must validate clean, got %q", e)
		}
	}
}

// TestValidateProfilesAcceptsTheBriefsEntry is the shape the whole step exists for: a
// new profile name over a shipped provider, stating one option. The entry must lower
// clean HERE — the option-name census, if any, is the provider's to impose later, in
// packload.ResolveProfiles, which is what reads the composed table.
func TestValidateProfilesAcceptsTheBriefsEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"profiles": {"zai-fast": {"provider": "zai", "model": "fast"}}}`)

	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "profiles") {
			t.Errorf("a well-formed profile must validate clean, got %q", e)
		}
	}
}

// TestCheckProfilesRefusesAnEntryWithoutAProvider is property 3, inverted deliberately
// (OQ-CS6): an entry that names no provider declares nothing, and the refusal says so.
func TestCheckProfilesRefusesAnEntryWithoutAProvider(t *testing.T) {
	_, problems := checkProfiles(decodeAny(t, `{"zai": {}}`))
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %v", problems)
	}
	for _, want := range []string{"config.profiles.zai", `missing required "provider"`, "declares nothing"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q missing %q", problems[0], want)
		}
	}
}

// TestCheckProfilesRefusesNonObjectEntries pins the entry shape: a bare string where an
// entry belongs is the typo the message should name.
func TestCheckProfilesRefusesNonObjectEntries(t *testing.T) {
	_, problems := checkProfiles(decodeAny(t, `{"zai": "fast"}`))
	if len(problems) != 1 || !strings.Contains(problems[0], `config.profiles.zai`) {
		t.Fatalf("want the entry-path problem, got %v", problems)
	}
	if _, problems := checkProfiles(decodeAny(t, `["zai"]`)); len(problems) != 1 ||
		!strings.Contains(problems[0], "expected an object of profile name") {
		t.Fatalf("want the key-shape problem, got %v", problems)
	}
}

// TestCheckProfilesRefusesANonStringOptionValue is the one shape check an option gets:
// the derive consumes strings, so a number or a bool is a typo — and a NULL is refused
// with the reason spelled out, because null means DELETE everywhere else in this config
// and would here compose exactly what an omitted key composes (OQ-CS7's wrinkle).
func TestCheckProfilesRefusesANonStringOptionValue(t *testing.T) {
	for _, body := range []string{
		`{"zai": {"provider": "zai", "model": 7}}`,
		`{"zai": {"provider": "zai", "model": null}}`,
	} {
		_, problems := checkProfiles(decodeAny(t, body))
		if len(problems) != 1 || !strings.Contains(problems[0], "expected a string option value") {
			t.Errorf("%s should be refused as a non-string value, got %v", body, problems)
		}
	}
	_, nullProblems := checkProfiles(decodeAny(t, `{"zai": {"provider": "zai", "model": null}}`))
	if !strings.Contains(nullProblems[0], "omit the key") {
		t.Errorf("the null refusal should name the spelling that works, got %q", nullProblems[0])
	}
}

// TestCheckProfilesLowersFreeOptionValues pins that core validates no VALUES (OQ-CS7):
// whatever string the user wrote arrives verbatim, because what it means is the
// derive's business.
func TestCheckProfilesLowersFreeOptionValues(t *testing.T) {
	entries, problems := checkProfiles(decodeAny(t,
		`{"zai-fast": {"provider": "zai", "model": "fast", "thinking": "low"}}`))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	p := entries["zai-fast"]
	if p.Provider != "zai" || p.Options["model"] != "fast" || p.Options["thinking"] != "low" {
		t.Errorf("entry lowered wrong: %+v", p)
	}
}

// TestLoadProfilesWarnsAndSkipsAMalformedEntry pins the reader's fallback: an entry that
// cannot lower is skipped with a warning rather than dropped in silence — and never
// turned into an error here, since the fatal channel is validateProfiles'.
func TestLoadProfilesWarnsAndSkipsAMalformedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"profiles": {"good": {"provider": "zai"}, "bad": {}}}`)

	var warned []string
	got, err := LoadProfiles(func(w string) { warned = append(warned, w) })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["bad"]; ok {
		t.Errorf("the malformed entry must be skipped, got %v", got)
	}
	if _, ok := got["good"]; !ok {
		t.Errorf("the well-formed entry must survive its neighbour, got %v", got)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], `missing required "provider"`) {
		t.Errorf("the skip should be loud and name the reason, got %v", warned)
	}
}
