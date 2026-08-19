package loopholes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// settings_test.go pins the DELIVERY half of docs/design/pack-config-keys.md: the
// declarations meeting the config, the file yolo writes, and the {settings} token
// resolving to it.

// redirectState points StateDirFor at a temp tree so nothing here touches the
// developer's real ~/.local/share.
func redirectState(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	real := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { StateDirFor = real })
	return root
}

func declaredLoophole(name string, settings ...Setting) *Loophole {
	return &Loophole{Name: name, Settings: settings}
}

func supplied(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// TestResolveSettingsIsTotal is the contract a daemon reads against: EVERY declared
// key is present, so nothing downstream has to distinguish "absent" from "false".
// A supplied key wins; an unsupplied one takes the declaration's default; a
// declaration with no default takes the type zero.
func TestResolveSettingsIsTotal(t *testing.T) {
	lp := declaredLoophole("acme",
		Setting{Key: "visible", Type: SettingTypeStringList, Default: []string{"pre"}},
		Setting{Key: "label", Type: SettingTypeString, Default: "d"},
		Setting{Key: "quiet", Type: SettingTypeBool, Default: false},
		Setting{Key: "depth", Type: SettingTypeInt, Default: 0},
	)
	got, problems := ResolveSettings(lp, supplied("visible", []any{"sway"}, "quiet", true))
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if !reflect.DeepEqual(got.Keys(), []string{"visible", "label", "quiet", "depth"}) {
		t.Errorf("keys = %v, want every declared key in DECLARATION order — the file's bytes "+
			"have to be stable across launches", got.Keys())
	}
	want := map[string]any{
		"visible": []string{"sway"}, // supplied wins
		"label":   "d",              // declaration default
		"quiet":   true,             // supplied wins
		"depth":   0,                // declaration default
	}
	for k, w := range want {
		v, _ := got.Get(k)
		if !reflect.DeepEqual(v, w) {
			t.Errorf("%s = %#v, want %#v", k, v, w)
		}
	}
}

// TestResolveSettingsDeclarationWinsOverABadValue: a wrong-typed value is reported
// and DROPPED, never coerced. ValidateConfig refuses these host-side, so what reaches
// here is the in-jail downgrade — and the fail-closed direction is the declaration's
// default, not whatever the config said.
func TestResolveSettingsDeclarationWinsOverABadValue(t *testing.T) {
	lp := declaredLoophole("acme",
		Setting{Key: "visible", Type: SettingTypeStringList, Default: []string{"safe"}})
	got, problems := ResolveSettings(lp, supplied("visible", "sway"))
	if len(problems) != 1 || !strings.Contains(problems[0], "list of strings") {
		t.Fatalf("problems = %v, want one type complaint", problems)
	}
	v, _ := got.Get("visible")
	if !reflect.DeepEqual(v, []string{"safe"}) {
		t.Errorf("visible = %#v, want the declared default — a value yolo could not validate "+
			"must never reach the file", v)
	}
}

// TestResolveSettingsReportsAnUndeclaredKey: the loop is driven by the DECLARATIONS,
// so an undeclared key cannot reach the file by construction. It is still reported,
// or a typo is invisible at exactly the moment the user is wondering why nothing
// changed.
func TestResolveSettingsReportsAnUndeclaredKey(t *testing.T) {
	lp := declaredLoophole("acme",
		Setting{Key: "visible", Type: SettingTypeStringList, Default: []string{}})
	got, problems := ResolveSettings(lp, supplied("visable", []any{"sway"}))
	if _, present := got.Get("visable"); present {
		t.Error("an undeclared key reached the settings file")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "no such setting") {
		t.Fatalf("problems = %v, want one 'no such setting'", problems)
	}
	if !strings.Contains(problems[0], "'visible'") {
		t.Errorf("problem %q does not name the declared keys", problems[0])
	}
}

// TestWriteSettingsProducesAFlatFile pins the file yolo writes: flat JSON, one entry
// per declared key, 0600, at the path {settings} resolves to.
func TestWriteSettingsProducesAFlatFile(t *testing.T) {
	redirectState(t)
	lp := declaredLoophole("acme",
		Setting{Key: "visible", Type: SettingTypeStringList, Default: []string{}},
		Setting{Key: "quiet", Type: SettingTypeBool, Default: false})
	path, problems, err := WriteSettings(lp, supplied("visible", []any{"sway", "waykeeper"}))
	if err != nil || len(problems) != 0 {
		t.Fatalf("WriteSettings = %v, %v", problems, err)
	}
	if path != SettingsFileFor("acme") {
		t.Errorf("path = %q, want %q — the manifest's {settings} token resolves to the "+
			"latter at record load, so the two must agree", path, SettingsFileFor("acme"))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A mode assertion, not a permission probe: the suite runs as UID 0, which can
	// read anything, so what is checkable is the BITS yolo asked for.
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600 — nothing in the schema says a setting is not a "+
			"credential", perm)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("the settings file is not plain JSON (%v): %s", err, raw)
	}
	if len(flat) != 2 {
		t.Errorf("file = %s, want exactly the two declared keys", raw)
	}
	names, _ := flat["visible"].([]any)
	if len(names) != 2 || names[0] != "sway" {
		t.Errorf("visible = %v", flat["visible"])
	}
	if flat["quiet"] != false {
		t.Errorf("quiet = %v, want the declared default false — the file must be TOTAL",
			flat["quiet"])
	}
}

// TestWriteSettingsRevokesADroppedValue: the file is rewritten whole on every
// launch, so dropping a key from the config puts the declared default back. Same
// rule env_sources has — dropping a key REVOKES it — and it is the reason the file
// is written unconditionally rather than only when values exist.
func TestWriteSettingsRevokesADroppedValue(t *testing.T) {
	redirectState(t)
	lp := declaredLoophole("acme",
		Setting{Key: "visible", Type: SettingTypeStringList, Default: []string{}})
	if _, _, err := WriteSettings(lp, supplied("visible", []any{"sway"})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteSettings(lp, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SettingsFileFor("acme"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sway") {
		t.Errorf("file = %s — a dropped value must be revoked, not left behind", raw)
	}
}

// TestWriteSettingsWritesNothingWithoutDeclarations: no declarations means no file,
// which is the same fact loopholedecl refuses a {settings} token over. A file written
// here would be one no argv names and nothing reads.
func TestWriteSettingsWritesNothingWithoutDeclarations(t *testing.T) {
	redirectState(t)
	lp := declaredLoophole("acme")
	path, _, err := WriteSettings(lp, supplied("anything", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("path = %q, want \"\"", path)
	}
	if _, err := os.Stat(SettingsFileFor("acme")); err == nil {
		t.Error("a loophole declaring no settings still got a settings file")
	}
}

// TestSettingsTokenResolvesAtLoad pins WHERE the token is substituted, and the
// placement is the argument: the settings path is a function of the loophole's NAME
// alone — exactly like {state} in ca_cert — so it resolves once, at record load, and
// every consumer of a record gets a real path. The spawn list, `yolo check`'s doctor
// run and `yolo loopholes status` do not share a substitution site, so a token left
// for the run pipeline would reach two of them unresolved.
func TestSettingsTokenResolvesAtLoad(t *testing.T) {
	redirectState(t)
	dir := t.TempDir()
	mod := filepath.Join(dir, "acme")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "acme",
		"settings": {"visible": {"type": "string_list"}},
		"host_daemon": {"cmd": ["d", "--socket", "{socket}", "--settings", "{settings}"],
			"publishes": "socket"},
		"doctor_cmd": ["d", "--self-check", "--settings", "{settings}"]
	}`
	if err := os.WriteFile(filepath.Join(mod, loopholedecl.ManifestName),
		[]byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := loopholedecl.LoadDir(mod)
	if err != nil {
		t.Fatal(err)
	}
	lp := resolve(m, mod)
	want := SettingsFileFor("acme")
	if got := lp.HostDaemon.Cmd[4]; got != want {
		t.Errorf("host_daemon.cmd[4] = %q, want %q", got, want)
	}
	if got := lp.DoctorCmd[3]; got != want {
		t.Errorf("doctor_cmd[3] = %q, want %q — the doctor must read the SAME file the "+
			"daemon does, or the two disagree about what is in force", got, want)
	}
	// {socket} is NOT resolved here: it is a per-launch fact the run pipeline owns.
	if got := lp.HostDaemon.Cmd[2]; got != "{socket}" {
		t.Errorf("host_daemon.cmd[2] = %q, want the raw token", got)
	}
	if !reflect.DeepEqual(loopholedecl.SettingKeys(lp.Settings), []string{"visible"}) {
		t.Errorf("declarations did not survive load: %+v", lp.Settings)
	}
}

// TestListPrintsTheSettingsDeclarations pins DISCOVERABILITY, which is a real gap
// this mechanism opens rather than a nicety: `yolo config-ref` is generated from
// core's own schema, and the whole point of a pack-declared key is that it is NOT in
// core's schema. Without this listing a user can only find a key by guessing it wrong
// and reading the validation error.
//
// The default is rendered in the JSON spelling a config author would TYPE — `[]`, not
// Go's `[]string{}` — because the reader of this line is about to write the value.
func TestListPrintsTheSettingsDeclarations(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "acme")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "acme", "default_enabled": true, "transport": "none",
		"settings": {
			"visible": {"type": "string_list", "scope": "workspace", "default": [],
				"description": "names to reveal"},
			"quiet": {"type": "bool"}
		}
	}`
	if err := os.WriteFile(filepath.Join(mod, loopholedecl.ManifestName),
		[]byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	realBundled := BundledLoopholesDir
	BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { BundledLoopholesDir = realBundled })

	var out, errOut strings.Builder
	rc := List(Deps{
		Out: &out, Err: &errOut, Cwd: t.TempDir(),
		LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil },
	})
	if rc != 0 {
		t.Fatalf("List rc=%d: %s", rc, errOut.String())
	}
	for _, want := range []string{
		"settings.visible: string_list, workspace-scope, default []",
		"names to reveal",
		// The undeclared scope renders as what it MEANS — user — not as an empty
		// column, because the strict default is the fact a reader most needs.
		"settings.quiet: bool, user-scope, default false",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("`loopholes list` output does not contain %q:\n%s", want, out.String())
		}
	}
}

// TestResolverCarriesTheDeclarationsToConfig pins the SEAM, which is the only place
// a declaration crosses from this package into the validator.
//
// Nothing else fails if `Settings: lp.Settings` is dropped from Resolver.Known():
// internal/config's own tests build a fakeResolver, so they would stay green while
// every real loophole's declarations vanished and every correct `settings` block
// started erroring "declares no settings". That is the call-site-unpinned shape
// AGENTS.md warns about, pointed at a struct literal instead of a function call.
func TestResolverCarriesTheDeclarationsToConfig(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "acme")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, loopholedecl.ManifestName), []byte(`{
		"name": "acme", "transport": "none",
		"settings": {"names": {"type": "string_list", "scope": "workspace"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	realBundled := BundledLoopholesDir
	BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { BundledLoopholesDir = realBundled })

	known, ok := NewResolver().Known()
	if !ok {
		t.Fatal("Known() reported discovery failure")
	}
	info, present := known["acme"]
	if !present {
		t.Fatalf("Known() = %v, missing the fixture loophole", known)
	}
	if len(info.Settings) != 1 || info.Settings[0].Key != "names" {
		t.Fatalf("LoopholeInfo.Settings = %+v — without the declarations the validator "+
			"cannot tell a declared key from a typo, and every correct settings block "+
			"becomes an error", info.Settings)
	}
	if info.Settings[0].Scope != SettingScopeWorkspace {
		t.Errorf("scope = %q, want the manifest's %q — the per-key scope rule is enforced "+
			"from THIS copy", info.Settings[0].Scope, SettingScopeWorkspace)
	}
}
