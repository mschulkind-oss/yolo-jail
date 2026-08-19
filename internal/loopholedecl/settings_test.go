package loopholedecl_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// settings_test.go pins the manifest's `settings` block — the declaration half of
// docs/design/pack-config-keys.md. The config half (what a user may supply against a
// declaration) is internal/config/validate_loopholesettings_test.go; the delivery
// half is internal/loopholes/settings_test.go.

// settingsManifest builds a manifest declaring one `settings` block.
func settingsManifest(t *testing.T, settings any, extra map[string]any) []byte {
	t.Helper()
	m := map[string]any{"name": "acme"}
	if settings != nil {
		m["settings"] = settings
	}
	for k, v := range extra {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestSettingsDeclarationDecodes is the happy path over all four types, and it also
// pins DECLARATION ORDER: the settings file is written in this order, so its bytes
// are stable across launches and a diff of two launches is a diff of the values.
func TestSettingsDeclarationDecodes(t *testing.T) {
	raw := settingsManifest(t, map[string]any{
		"visible": map[string]any{
			"type": "string_list", "scope": "workspace",
			"default": []any{"sway"}, "description": "names",
		},
		"label":   map[string]any{"type": "string", "default": "x"},
		"verbose": map[string]any{"type": "bool", "default": true},
		"depth":   map[string]any{"type": "int", "default": 3},
	}, nil)
	m, err := loopholedecl.Decode(raw, "/loopholes/acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Settings) != 4 {
		t.Fatalf("settings = %+v, want four", m.Settings)
	}
	want := map[string]any{
		"visible": []string{"sway"},
		"label":   "x",
		"verbose": true,
		"depth":   3,
	}
	for key, wantDefault := range want {
		got, ok := loopholedecl.SettingByKey(m.Settings, key)
		if !ok {
			t.Fatalf("no declaration for %q", key)
		}
		if !reflect.DeepEqual(got.Default, wantDefault) {
			t.Errorf("%s default = %#v, want %#v", key, got.Default, wantDefault)
		}
		if !got.DefaultSet {
			t.Errorf("%s DefaultSet = false; the manifest declared one", key)
		}
	}
}

// TestSettingScopeDefaultsToUser pins the ruling that silence is the SAFE choice.
//
// A settings value can reach a host daemon's argv-named file, so an author who says
// nothing about who may supply it gets the same answer `env` already gets: user
// config only. Widening to `workspace` has to be written down, which is what makes
// it auditable in the manifest instead of discoverable by reading core.
func TestSettingScopeDefaultsToUser(t *testing.T) {
	raw := settingsManifest(t, map[string]any{
		"quiet": map[string]any{"type": "bool"},
	}, nil)
	m, err := loopholedecl.Decode(raw, "/loopholes/acme")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := loopholedecl.SettingByKey(m.Settings, "quiet")
	if got.Scope != loopholedecl.SettingScopeUser {
		t.Errorf("absent scope = %q, want %q — an undeclared scope must not admit the "+
			"agent-editable workspace file", got.Scope, loopholedecl.SettingScopeUser)
	}
}

// TestSettingDefaultsToTypeZeroWhenUndeclared pins TOTALITY: every declared key ends
// up in the settings file, so a daemon never has to distinguish "absent" from
// "false". A nil here would put that distinction back.
func TestSettingDefaultsToTypeZeroWhenUndeclared(t *testing.T) {
	raw := settingsManifest(t, map[string]any{
		"names":   map[string]any{"type": "string_list"},
		"label":   map[string]any{"type": "string"},
		"verbose": map[string]any{"type": "bool"},
		"depth":   map[string]any{"type": "int"},
	}, nil)
	m, err := loopholedecl.Decode(raw, "/loopholes/acme")
	if err != nil {
		t.Fatal(err)
	}
	zeros := map[string]any{
		"names": []string{}, "label": "", "verbose": false, "depth": 0,
	}
	for key, want := range zeros {
		got, _ := loopholedecl.SettingByKey(m.Settings, key)
		if got.Default == nil {
			t.Errorf("%s default is nil; an absent 'default' must resolve to the type ZERO, "+
				"or the settings file stops being total", key)
			continue
		}
		if !reflect.DeepEqual(got.Default, want) {
			t.Errorf("%s default = %#v, want the type zero %#v", key, got.Default, want)
		}
		if got.DefaultSet {
			t.Errorf("%s DefaultSet = true; the manifest declared none", key)
		}
	}
}

// TestUnknownSettingDeclarationKeyIsRefusedByBOTHDecoders is the OQ-K1 teeth, and it
// is the one place this package's version-boundary tolerance is deliberately absent.
//
// DecodeTolerant exists because a key only a NEWER build knows must not make a
// loophole vanish. That reasoning INVERTS for a settings declaration: a newer
// manifest writing {"type":"string","enum":["a","b"]} would have its `enum` silently
// dropped, and core would then validate a value against half a constraint and write
// the result into a file a host daemon reads. So both decoders refuse, and the
// tolerant one is the half worth pinning — the strict one would refuse an unknown
// key anywhere.
func TestUnknownSettingDeclarationKeyIsRefusedByBOTHDecoders(t *testing.T) {
	raw := settingsManifest(t, map[string]any{
		"label": map[string]any{"type": "string", "enum": []any{"a", "b"}},
	}, nil)
	if _, err := loopholedecl.Decode(raw, "/loopholes/acme"); err == nil {
		t.Error("the STRICT decoder accepted an unknown settings-declaration key")
	}
	m, skipped, err := loopholedecl.DecodeTolerant(raw, "/loopholes/acme")
	if err == nil {
		t.Fatalf("the TOLERANT decoder accepted an unknown settings-declaration key "+
			"(manifest=%+v skipped=%v) — tolerating it means validating a value against a "+
			"constraint this build did not understand and then handing it to a host daemon",
			m, skipped)
	}
	if !strings.Contains(err.Error(), "enum") {
		t.Errorf("refusal %q does not name the offending key", err)
	}
}

// TestSettingDeclarationRefusals covers the rest of the schema's closed edges. Each
// is a REFUSAL rather than a tolerated oddity for the same reason: core must never
// hand a host daemon a value it could not validate, and every one of these leaves
// core unable to validate something.
func TestSettingDeclarationRefusals(t *testing.T) {
	cases := []struct {
		name     string
		settings any
		fragment string
	}{
		{"missing-type", map[string]any{"a": map[string]any{"scope": "user"}}, "'settings.a.type' is required"},
		{"unknown-type", map[string]any{"a": map[string]any{"type": "float"}}, "not in"},
		{"unknown-scope", map[string]any{"a": map[string]any{"type": "string", "scope": "team"}}, "scope"},
		{"default-wrong-type", map[string]any{
			"a": map[string]any{"type": "string_list", "default": "sway"},
		}, "list of strings"},
		{"default-quoted-bool", map[string]any{
			"a": map[string]any{"type": "bool", "default": "false"},
		}, "boolean"},
		{"bad-key-name", map[string]any{"Visible": map[string]any{"type": "string"}}, "must match"},
		{"declaration-not-an-object", map[string]any{"a": "string"}, "declaration object"},
		{"block-not-an-object", []any{"a"}, "must be a mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := settingsManifest(t, tc.settings, nil)
			err := errFrom(loopholedecl.Decode(raw, "/loopholes/acme"))
			if err == nil {
				t.Fatalf("accepted %v", tc.settings)
			}
			if !strings.Contains(err.Error(), tc.fragment) {
				t.Errorf("refusal %q does not contain %q", err, tc.fragment)
			}
			// Tolerant too: none of these is version skew.
			if _, _, terr := loopholedecl.DecodeTolerant(raw, "/loopholes/acme"); terr == nil {
				t.Errorf("the tolerant decoder accepted %v", tc.settings)
			}
		})
	}
}

// TestSettingsTokenRequiresADeclaration pins the {settings} placement rules: the
// token resolves to a file yolo writes FROM the declarations, so an argv naming it
// with no declarations names a file that will never exist, and a jail-side command
// naming it names a HOST path the container cannot see.
func TestSettingsTokenRequiresADeclaration(t *testing.T) {
	decl := map[string]any{"visible": map[string]any{"type": "string_list"}}
	hostDaemon := map[string]any{"cmd": []any{"d", "--socket", "{socket}", "--settings", "{settings}"}}

	// With declarations: accepted, and the token survives decoding RAW (resolving it
	// needs the state dir, which is internal/loopholes' fact, not the schema's).
	m, err := loopholedecl.Decode(
		settingsManifest(t, decl, map[string]any{"host_daemon": hostDaemon}), "/loopholes/acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.HostDaemon.Cmd[4]; got != "{settings}" {
		t.Errorf("host_daemon.cmd[4] = %q; {settings} resolves in internal/loopholes", got)
	}

	// Without any declaration: refused.
	err = errFrom(loopholedecl.Decode(
		settingsManifest(t, nil, map[string]any{"host_daemon": hostDaemon}), "/loopholes/acme"))
	if err == nil || !strings.Contains(err.Error(), "declares no 'settings'") {
		t.Errorf("a {settings} token with no declarations gave %v, want a refusal naming the "+
			"empty block — the daemon would be handed a path to a missing file", err)
	}

	// doctor_cmd is held to the same rule: it is the OTHER host-side argv.
	err = errFrom(loopholedecl.Decode(
		settingsManifest(t, nil, map[string]any{"doctor_cmd": []any{"d", "{settings}"}}),
		"/loopholes/acme"))
	if err == nil || !strings.Contains(err.Error(), "doctor_cmd") {
		t.Errorf("doctor_cmd with a bare {settings} gave %v, want a refusal", err)
	}

	// jail_daemon.cmd runs INSIDE the container, where the host state dir does not
	// exist — refused even though this manifest declares settings.
	err = errFrom(loopholedecl.Decode(
		settingsManifest(t, decl, map[string]any{
			"jail_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/d", "{settings}"}},
		}), "/loopholes/acme"))
	if err == nil || !strings.Contains(err.Error(), "HOST-side") {
		t.Errorf("jail_daemon.cmd naming {settings} gave %v, want a refusal naming the "+
			"host/container split", err)
	}
}

// TestSettingsBlockIsAKnownTopLevelKey guards the census: `settings` must be in
// topKeys or the strict decoder reports every declaring manifest as a typo, and
// KnownKeys() — which authoring tools print — would not suggest it.
func TestSettingsBlockIsAKnownTopLevelKey(t *testing.T) {
	found := false
	for _, k := range loopholedecl.KnownKeys() {
		if k == "settings" {
			found = true
		}
	}
	if !found {
		t.Errorf("KnownKeys() = %v, missing \"settings\"", loopholedecl.KnownKeys())
	}
}

// TestCoerceSettingValueRefusesTruthyCoercion pins the shared type checker both
// halves use — the manifest's `default` and internal/config's supplied value — and
// pins it on the slip that fails in the GRANTING direction: Truthy("false") is TRUE,
// so a quoted "false" coerced rather than checked would turn a setting ON.
func TestCoerceSettingValueRefusesTruthyCoercion(t *testing.T) {
	if _, err := loopholedecl.CoerceSettingValue(loopholedecl.SettingTypeBool, "false"); err == "" {
		t.Error(`CoerceSettingValue(bool, "false") accepted a string; Truthy would read it as TRUE`)
	}
	if v, err := loopholedecl.CoerceSettingValue(loopholedecl.SettingTypeBool, false); err != "" || v != false {
		t.Errorf("CoerceSettingValue(bool, false) = %v, %q", v, err)
	}
	if _, err := loopholedecl.CoerceSettingValue(loopholedecl.SettingTypeInt, "3"); err == "" {
		t.Error(`CoerceSettingValue(int, "3") accepted a string`)
	}
	if _, err := loopholedecl.CoerceSettingValue(
		loopholedecl.SettingTypeStringList, []any{"a", 1}); err == "" {
		t.Error("CoerceSettingValue(string_list, [\"a\", 1]) accepted a non-string element")
	}
}

func errFrom(_ *loopholedecl.Manifest, err error) error { return err }
