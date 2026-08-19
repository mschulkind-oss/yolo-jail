package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// validate_loopholesettings_test.go pins core's half of
// docs/design/pack-config-keys.md: what a config may supply under
// `loopholes.<name>.settings`, checked against the loophole's manifest
// declarations rather than passed through opaquely.

// declaring builds a fakeResolver for one loophole with the given declarations.
func declaring(name string, settings ...loopholedecl.Setting) fakeResolver {
	return fakeResolver{name: {Name: name, HasHostDaemon: true, Settings: settings}}
}

func wsSetting(key, typ string) loopholedecl.Setting {
	return loopholedecl.Setting{
		Key: key, Type: typ, Scope: loopholedecl.SettingScopeWorkspace,
		Default: loopholedecl.SettingZero(typ),
	}
}

func userSetting(key, typ string) loopholedecl.Setting {
	return loopholedecl.Setting{
		Key: key, Type: typ, Scope: loopholedecl.SettingScopeUser,
		Default: loopholedecl.SettingZero(typ),
	}
}

// TestDeclaredSettingIsAccepted is the baseline: a declared key with a value of the
// declared type is clean at either scope when the declaration says `workspace`.
func TestDeclaredSettingIsAccepted(t *testing.T) {
	res := declaring("host-processes", wsSetting("visible", loopholedecl.SettingTypeStringList))
	_, errs, _ := validateScoped(t, "",
		`{"loopholes": {"host-processes": {"settings": {"visible": ["sway"]}}}}`, res)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
	_, errs, _ = validateScoped(t,
		`{"loopholes": {"host-processes": {"settings": {"visible": ["sway"]}}}}`, "", res)
	if len(errs) != 0 {
		t.Errorf("user-scope errors = %v, want none", errs)
	}
}

// TestSettingsOnlyEntryIsNotAnInlineService is the shape-dispatch fix
// pack-config-keys.md §5 calls out by name, and the case that needs it is the
// loophole this machine cannot SEE — a pack whose module has not staged here, which
// is the ordinary state of a config being read on another machine.
//
// For a known loophole the entry takes the override branch unconditionally. For an
// unknown one the dispatch asks whether the entry's keys are a subset of the
// override census, and a `settings`-only entry falls through to "inline service" —
// erroring `command: required` over a config that is perfectly correct — unless
// `settings` is in that census.
func TestSettingsOnlyEntryIsNotAnInlineService(t *testing.T) {
	// Unknown loophole: no resolver at all.
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"unstaged": {"settings": {"visible": ["sway"]}}}}`, "", nil)
	if len(containing(errs, "command")) != 0 {
		t.Errorf("errors = %v — a settings-only entry is an OVERRIDE shape, so it must not "+
			"be dispatched as an inline service and demand a 'command'", errs)
	}
	if len(containing(warns, "no loophole named 'unstaged'")) != 1 {
		t.Errorf("warnings = %v, want the existing unknown-loophole warning", warns)
	}
	// And the same entry for a KNOWN loophole is clean too.
	res := declaring("host-processes", wsSetting("visible", loopholedecl.SettingTypeStringList))
	_, errs, _ = validateScoped(t, "",
		`{"loopholes": {"host-processes": {"settings": {"visible": ["sway"]}}}}`, res)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

// TestUndeclaredSettingIsAnError is the typo protection OQ-K1 kept. The design first
// assumed core might not see a pack's declaration at launch and would have to accept
// values unvalidated; launch is offline-by-design and an unresolvable pack is already
// a fatal error, so validation keeps its teeth and a misspelled key is caught.
func TestUndeclaredSettingIsAnError(t *testing.T) {
	res := declaring("host-processes",
		wsSetting("visible", loopholedecl.SettingTypeStringList),
		wsSetting("fields", loopholedecl.SettingTypeStringList))
	_, errs, _ := validateScoped(t, "",
		`{"loopholes": {"host-processes": {"settings": {"visable": ["sway"]}}}}`, res)
	hits := containing(errs, "settings.visable", "no such setting")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want one 'no such setting' for the typo", errs)
	}
	// The message has to name the alternatives, or the reader has no way to find the
	// spelling from inside the error.
	for _, want := range []string{"'fields'", "'visible'"} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("error %q does not list %s", hits[0], want)
		}
	}
}

// TestSettingTypeIsChecked pins that a value is checked against the DECLARED type,
// including the coercion slips that would otherwise fail in the granting direction.
func TestSettingTypeIsChecked(t *testing.T) {
	res := declaring("acme",
		wsSetting("names", loopholedecl.SettingTypeStringList),
		wsSetting("quiet", loopholedecl.SettingTypeBool),
		wsSetting("depth", loopholedecl.SettingTypeInt))
	cases := []struct{ json, path, fragment string }{
		{`{"names": "sway"}`, "settings.names", "list of strings"},
		{`{"quiet": "false"}`, "settings.quiet", "boolean"},
		{`{"depth": "3"}`, "settings.depth", "integer"},
		{`{"quiet": null}`, "settings.quiet", "null"},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.fragment, func(t *testing.T) {
			_, errs, _ := validateScoped(t, "",
				`{"loopholes": {"acme": {"settings": `+tc.json+`}}}`, res)
			if len(containing(errs, tc.path, tc.fragment)) == 0 {
				t.Errorf("errors = %v, want one naming %q and %q", errs, tc.path, tc.fragment)
			}
		})
	}
}

// TestUserScopedSettingIsRefusedFromTheWorkspace is the per-key scope rule, and it is
// the mechanism pack-config-keys.md §3 needs because R5's "the weak scope is bounded
// by the strong one" is FALSE for lists: MergeConfig union-merges every list at every
// depth, so a workspace can only ever WIDEN a user-scope allowlist. There is no
// intersection to fall back on, so the answer is a refusal.
func TestUserScopedSettingIsRefusedFromTheWorkspace(t *testing.T) {
	res := declaring("acme", userSetting("visible", loopholedecl.SettingTypeStringList))
	ws, errs, _ := validateScoped(t, "",
		`{"loopholes": {"acme": {"settings": {"visible": ["sway"]}}}}`, res)
	hits := containing(errs, "settings.visible", "user-scope only")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want one scope refusal", errs)
	}
	for _, want := range []string{filepath.Join(ws, WorkspaceConfigName), loopholeUserConfigHint} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("error %q does not name %q — a scope refusal has to name the file it "+
				"came from and the file it belongs in", hits[0], want)
		}
	}
	// The SAME key from the user config is clean: the rule is about which file
	// supplied it, not about the key.
	_, errs, _ = validateScoped(t,
		`{"loopholes": {"acme": {"settings": {"visible": ["sway"]}}}}`, "", res)
	if len(errs) != 0 {
		t.Errorf("user-config errors = %v, want none", errs)
	}
}

// TestUserScopedSettingDowngradesInJail mirrors the §4.3b asymmetry every other
// loophole scope violation already has. /workspace is live-mounted, so a hard error
// in-jail would refuse every nested launch over a file the in-jail user may still be
// migrating.
func TestUserScopedSettingDowngradesInJail(t *testing.T) {
	res := declaring("acme", userSetting("visible", loopholedecl.SettingTypeStringList))
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"loopholes": {"acme": {"settings": {"visible": ["sway"]}}}}`)
	t.Setenv("YOLO_VERSION", "1")
	merged := MergeConfig(decode(t, "{}"), decode(t,
		`{"loopholes": {"acme": {"settings": {"visible": ["sway"]}}}}`))
	errs, warns := ValidateConfig(merged, ws, res)
	if len(containing(errs, "user-scope only")) != 0 {
		t.Errorf("in-jail errors = %v — the violation must downgrade to a warning", errs)
	}
	if len(containing(warns, "settings.visible", "user-scope only")) != 1 {
		t.Errorf("in-jail warnings = %v, want the downgraded scope violation", warns)
	}
}

// TestSettingsOnALoopholeThatDeclaresNoneIsAnError: an EMPTY declaration list on a
// KNOWN loophole is meaningful and is not "unknown" — it says the loophole owns no
// config keys, so every supplied key is a typo. Silence here would make
// `settings` an opaque map for exactly the loopholes that declared nothing.
func TestSettingsOnALoopholeThatDeclaresNoneIsAnError(t *testing.T) {
	res := fakeResolver{"acme": {Name: "acme", HasHostDaemon: true}}
	_, errs, _ := validateScoped(t, "",
		`{"loopholes": {"acme": {"settings": {"anything": "x"}}}}`, res)
	if len(containing(errs, "settings", "declares no settings")) != 1 {
		t.Errorf("errors = %v, want one refusal naming the empty declaration", errs)
	}
}

// TestSettingsOnAnUnknownLoopholeIsNotValidated is the ONE unvalidated case, and it
// is unvalidated on purpose: no loophole of that name is discoverable, so there are
// no declarations to check against — and the entry already carries the existing "no
// loophole named X is installed" warning. A second message for the same absence
// would report one mistake twice, which is what validateAgentsRetired's comment
// warns about.
func TestSettingsOnAnUnknownLoopholeIsNotValidated(t *testing.T) {
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"ghost": {"settings": {"whatever": 1}}}}`, "", nil)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — an unseen loophole means UNVALIDATED, never invalid", errs)
	}
	if len(containing(warns, "no loophole named 'ghost'")) != 1 {
		t.Errorf("warnings = %v, want the existing unknown-loophole warning and nothing more", warns)
	}
}

// TestRetiredHostProcessesKeyIsRefusedAndNamesItsReplacement pins the DELETION.
//
// The key was honored-with-a-warning through the step that moved its values into
// `loopholes.host-processes.settings`; it is deleted by the step that moved the
// loophole into a pack. What this asserts is that deletion did not become SILENCE:
// this block decided what a host daemon would reveal about the user's machine, so a
// config that still writes it and gets nothing has been denied a capability it asked
// for, in the one direction where silence reads as success.
//
// The message has four jobs, all asserted: name the replacement path, warn about the
// hyphen (the two spellings differ by one character and the loophole's is not the
// key's), say that the loophole now needs SELECTING as a pack, and repeat that the
// value is frozen at launch. The last one is the behaviour change a reader who
// migrates correctly still gets wrong.
func TestRetiredHostProcessesKeyIsRefusedAndNamesItsReplacement(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"host_processes": {"visible": ["sway"]}}`)
	errs, warns := ValidateConfig(cfg, t.TempDir(), nil)
	hits := containing(errs, "config.host_processes")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want ONE refusal naming the retired key; a key that stopped "+
			"working must not be ignored, and it must not be reported twice either "+
			"(it stays in knownTopLevelConfigKeys so the generic unknown-key error "+
			"does not fire beside this one)", errs)
	}
	for _, want := range []string{
		"REMOVED",
		"loopholes",
		"host-processes",
		"settings",
		"packs",   // the loophole is a pack now; migrating the keys alone is not enough
		"restart", // the freeze is a behaviour change and has to be said here too
	} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("refusal %q does not mention %q", hits[0], want)
		}
	}
	if len(containing(warns, "config.host_processes")) != 0 {
		t.Errorf("warnings = %v — on the host this is an error, not a warning", warns)
	}
	// TYPE CHECKS ARE GONE WITH THE KEY, deliberately: asking a user to fix the shape
	// of a block they must delete is two contradictory instructions about one line.
	bad := decode(t, `{"host_processes": {"visible": "sway"}}`)
	badErrs, _ := ValidateConfig(bad, t.TempDir(), nil)
	if len(containing(badErrs, "list of strings")) != 0 {
		t.Errorf("errors = %v — a removed key has no shape left to be wrong", badErrs)
	}
	if len(containing(badErrs, "REMOVED")) != 1 {
		t.Errorf("errors = %v, want the refusal regardless of the block's shape", badErrs)
	}
}

// TestRetiredHostProcessesKeyIsAWarningInsideAJail is the same asymmetry
// validateAgentsRetired carries, and it exists for the same measured reason: in-jail
// the config is the HOST-GENERATED snapshot, so an error there refuses every nested
// launch over a key the in-jail user cannot fix at its source — and it would make
// `yolo check` (which merges the real files) disagree with the launch.
//
// The population is narrow by construction: inherit.go stops emitting the key, so
// only a snapshot written by an OLDER launcher can still carry it. That is exactly
// the population an error would strand with no way out.
func TestRetiredHostProcessesKeyIsAWarningInsideAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	cfg := decode(t, `{"host_processes": {"visible": ["sway"]}}`)
	errs, warns := ValidateConfig(cfg, t.TempDir(), nil)
	if len(containing(errs, "config.host_processes")) != 0 {
		t.Errorf("errors = %v — in-jail this must not refuse: the file is the host's "+
			"snapshot, not something the in-jail user typed", errs)
	}
	hits := containing(warns, "config.host_processes")
	if len(hits) != 1 {
		t.Fatalf("warnings = %v, want the downgraded notice", warns)
	}
	if !strings.Contains(hits[0], "HOST config") {
		t.Errorf("in-jail notice %q does not say where the key actually has to be "+
			"removed, which is the only actionable thing about it", hits[0])
	}
}

// TestTheInheritCensusStopsEmittingTheRetiredKey closes the loop the two tests above
// open: the key is now a host ERROR, so a generated inner scope that still carried it
// would hand a nested launcher a config that refuses itself.
func TestTheInheritCensusStopsEmittingTheRetiredKey(t *testing.T) {
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		for _, k := range InheritKeys(scope) {
			if k == "host_processes" {
				t.Errorf("the %s scope still emits host_processes — a nested launcher would "+
					"read a key this build refuses", scope)
			}
		}
	}
	if _, _, reason, ok := InheritDisposition("host_processes"); !ok || reason == "" {
		t.Error("host_processes has no census entry — a key in NO scope is still listed, " +
			"with the reason it is excluded, because \"assigned to neither\" has to be a " +
			"decision on the record rather than an omission")
	}
}

// TestSettingsIsAnOverrideKeyNotAnInlineKey pins the census split. An INLINE config
// loophole has no manifest, hence no declarations, hence nothing core could validate
// a value against — and the rule this whole mechanism rests on is that core never
// hands a host daemon a value it could not validate.
func TestSettingsIsAnOverrideKeyNotAnInlineKey(t *testing.T) {
	if _, ok := knownLoopholeOverrideKeys["settings"]; !ok {
		t.Error("knownLoopholeOverrideKeys is missing \"settings\" — a settings-only entry " +
			"would then be dispatched as an inline service and demand a 'command'")
	}
	if _, ok := knownHostServiceKeys["settings"]; ok {
		t.Error("knownHostServiceKeys accepts \"settings\" — an inline entry has no manifest " +
			"to declare against, so the values would reach a host daemon unvalidated")
	}
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {"command": ["/bin/true"], "settings": {"a": 1}}}}`)
	if errs, _ := ValidateConfig(cfg, t.TempDir(), nil); len(containing(errs, "settings")) == 0 {
		t.Errorf("errors = %v, want a refusal of 'settings' on an inline service", errs)
	}
}

// TestLoopholeEntryErrorsAppliesTheSettingsScopeRule pins the SECOND call site.
//
// `yolo loopholes list` and `status` read the config WITHOUT the full schema pass,
// through LoopholeEntryErrors — and `status` EXECUTES each entry's doctor_cmd from
// what it reads. Every other test in this file goes through ValidateConfig, so the
// scope rule could be dropped from this path with the whole suite green, and the two
// surfaces would then disagree about whether a workspace may set a user-scope key.
func TestLoopholeEntryErrorsAppliesTheSettingsScopeRule(t *testing.T) {
	info := &LoopholeInfo{
		Name: "acme", HasHostDaemon: true,
		Settings: []loopholedecl.Setting{userSetting("visible", loopholedecl.SettingTypeStringList)},
	}
	ws := t.TempDir()
	src := filepath.Join(ws, WorkspaceConfigName)
	spec := decode(t, `{"settings": {"visible": ["sway"]}}`)

	errs := LoopholeEntryErrors("acme", spec, info, false, true, false, src, ws)
	if len(containing(errs, "settings.visible", "user-scope only")) != 1 {
		t.Errorf("errors = %v, want the scope refusal — `yolo loopholes status` executes "+
			"doctor_cmd from what it reads, so an entry this path honors is an entry that "+
			"runs host code", errs)
	}
	// From the USER config the same entry is clean, and in-jail it downgrades to the
	// warning this function deliberately does not return.
	if errs := LoopholeEntryErrors("acme", spec, info, false, false, false, src, ws); len(errs) != 0 {
		t.Errorf("user-scope errors = %v, want none", errs)
	}
	if errs := LoopholeEntryErrors("acme", spec, info, false, true, true, src, ws); len(errs) != 0 {
		t.Errorf("in-jail errors = %v, want none — the launch path honors the entry in-jail", errs)
	}
}
