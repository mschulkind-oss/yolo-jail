package config

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// validate_loopholesettings.go is core's half of docs/design/pack-config-keys.md:
// a loophole's MANIFEST declares which config keys it owns and what type each
// takes, and this is where `loopholes.<name>.settings` is checked against that
// declaration.
//
// # Why the block is nested rather than spread across the entry
//
// The `loopholes.<name>` key set is deliberately CLOSED (knownLoopholeOverrideKeys),
// and closing it is what makes a typo beside `enabled` an error rather than an inert
// key. A nested `settings` object keeps that closure intact while opening exactly one
// door: an unknown key INSIDE settings is checked against the pack's declaration, an
// unknown key BESIDE it is still the existing error.
//
// # Why an unknown key here is an ERROR and not a warning
//
// Because the declarations are AUTHORITATIVE (OQ-K1). The design first assumed core
// might be unable to see a pack's declaration at launch and would therefore have to
// accept values unvalidated — but launch is strictly offline BY DESIGN (it resolves
// from the local pack store and never reaches out mid-boot) and a pack that cannot
// be resolved is already a FATAL launch error naming `yolo pack install`. So there
// is no launch in which a configured pack's declaration is missing and the jail
// starts anyway, and validation keeps its teeth.
//
// The one case that does survive — this build's decoder not understanding a
// declaration written for a newer one — is handled a level down, in
// loopholedecl.parseSettings, and it is handled as a REFUSAL rather than a
// pass-through, for the reason that governs this whole file: core must never hand a
// host daemon a value it could not validate.

// validateLoopholeSettings checks one entry's `settings` block against the
// loophole's declarations. info is nil when no loophole of that name is
// discoverable from here, which is the ONLY unvalidated case and already carries
// its own "no loophole named X is installed" warning from the caller — adding a
// second message for the same absence would report one mistake twice.
func validateLoopholeSettings(spec *jsonx.OrderedMap, path string, info *LoopholeInfo, errs *[]string) {
	v, present := spec.Get("settings")
	if !present || v == nil {
		return
	}
	settings, ok := asMap(v)
	if !ok {
		add(errs, path+".settings: expected an object of setting name -> value")
		return
	}
	if info == nil {
		return
	}
	if settings.Len() > 0 && len(info.Settings) == 0 {
		add(errs, path+".settings: "+pytext.Repr(info.Name)+" declares no settings, so there is "+
			"nothing to configure here — a loophole's settable keys are declared in its "+
			"manifest.jsonc under 'settings', and only declared keys may be supplied")
		return
	}
	for _, key := range settings.Keys() {
		keyPath := path + ".settings." + key
		decl, declared := loopholedecl.SettingByKey(info.Settings, key)
		if !declared {
			add(errs, keyPath+": no such setting — "+pytext.Repr(info.Name)+" declares "+
				declaredKeyList(info.Settings))
			continue
		}
		value, _ := settings.Get(key)
		if value == nil {
			// null is not "use the default": a declared key with a null value is a
			// value of the wrong type, and treating it as absence would make
			// `"visible": null` and a missing key the same edit with different words.
			add(errs, keyPath+": expected "+settingTypePhrase(decl.Type)+", got null")
			continue
		}
		if _, typeErr := loopholedecl.CoerceSettingValue(decl.Type, value); typeErr != "" {
			add(errs, keyPath+": "+typeErr)
		}
	}
}

// loopholeSettingsScopeViolations applies the per-key `scope` rule to ONE workspace
// config file's contribution to a `loopholes.<name>.settings` block. The returned
// messages are host-side errors; the caller downgrades them in-jail exactly as it
// does the §4.3b key rows.
//
// # The scope rule is per KEY, and this is the only place it can be enforced
//
// `env` is refused at workspace scope wholesale because every value in it reaches a
// host daemon's spawn environment. A settings key is different: it is typed,
// declared, and validated, and OQ-K2 ruled that a workspace MAY supply one — gated
// by the config-change approval flow, which became a real control only when the
// approval snapshot moved host-side and non-interactive launches stopped
// auto-accepting. The per-key `scope` field is what lets a declaration say otherwise
// for a key that should not travel with a repo.
//
// # Why a user-scope key cannot simply be "bounded" by the workspace
//
// docs/design/pack-config-keys.md §3 corrects loophole-activation.md R5 on exactly
// this point, and the correction is load-bearing here: MergeConfig union-merges
// EVERY list at every depth, and the replace-wholesale exception was deleted on
// purpose. So a user-scope ceiling list that a workspace NARROWS is inexpressible —
// a workspace can only ever WIDEN. For an allowlist that inverts the intended safety
// property, which is why the answer is a refusal rather than an intersection.
func loopholeSettingsScopeViolations(name string, spec *jsonx.OrderedMap, srcFile string, info *LoopholeInfo) []string {
	if info == nil {
		return nil
	}
	v, present := spec.Get("settings")
	if !present || v == nil {
		return nil
	}
	settings, ok := asMap(v)
	if !ok {
		return nil // the shape pass reports the type error
	}
	path := "config.loopholes." + name
	var out []string
	for _, key := range settings.Keys() {
		decl, declared := loopholedecl.SettingByKey(info.Settings, key)
		if !declared || decl.Scope != loopholedecl.SettingScopeUser {
			continue
		}
		out = append(out, path+".settings."+key+": user-scope only — "+pytext.Repr(name)+
			" declares this setting "+pytext.Repr(loopholedecl.SettingScopeUser)+
			" in its manifest, and "+srcFile+" is agent-editable. Move this key to "+
			loopholeUserConfigHint+".")
	}
	return out
}

// declaredKeyList renders a loophole's declared setting keys for a "no such
// setting" error, SORTED — the message is a spelling aid, and a reader scanning it
// for the name they meant wants alphabetical order, not the author's.
func declaredKeyList(settings []loopholedecl.Setting) string {
	keys := loopholedecl.SettingKeys(settings)
	if len(keys) == 0 {
		return "none"
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	parts := make([]string, len(sorted))
	for i, k := range sorted {
		parts[i] = pytext.Repr(k)
	}
	return strings.Join(parts, ", ")
}

// settingTypePhrase renders a declared type as the noun phrase the type errors use,
// so "expected a list of strings" reads the same whether it came from here or from
// loopholedecl.CoerceSettingValue.
func settingTypePhrase(typ string) string {
	switch typ {
	case loopholedecl.SettingTypeBool:
		return "a boolean"
	case loopholedecl.SettingTypeInt:
		return "an integer"
	case loopholedecl.SettingTypeStringList:
		return "a list of strings"
	default:
		return "a string"
	}
}
