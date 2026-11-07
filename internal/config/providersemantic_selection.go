package config

// providersemantic_selection.go is the ONE selection namespace a launch reads
// (docs/design/config-user.md §5; readable-config.md OQ-RC3, OQ-RC14, OQ-RC20, RC-D4): the
// `active_provider` key, the legacy `profile` key it renames (RC-D1, kept for the dual-reader
// window, RC-D19), and `-p`, all lowered to one ProfileSelection that FoldProfiles folds, and all
// resolved against one set of names:
//
//   - every profile a selected pack ships or the user declares (`profiles`), as before;
//   - every provider the selected packs and the config's `providers` declare, by its own name,
//     starting on its `model` (OQ-RC3);
//   - every provider's `alias` (OQ-RC14), and every `<provider>/<variant>` (OQ-RC3);
//   - "native", the one reserved word: an agent set to it is left to its own login (OQ-RC20,
//     RC-D15). It selects no provider, so it writes no provider rows, and the provider variables
//     the agent's pack declares are cleared from its environment as for any agent with no
//     selection (PP-D14).
//
// A provider, alias or variant name that a profile already holds is that profile: the profile is
// what already works (OQ-RC3, "the rare case keeps the profile that already works"), and the
// shipped profiles that only repeat their provider's name are being deleted (OQ-RC3), at which
// point the provider's own name answers instead. The implied profiles this file builds
// (SelectableProfiles) are handed to the same packload.ResolveProfiles every reader calls, so the
// receiver machinery, the derives and the YOLO_PROFILES table read them unchanged. *Implied
// profile* is a term coined here: a profile entry yolo builds from a provider declaration for a
// name no profile declares, so the declaration is selectable without one.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ActiveProviderKey is the readable config's selection key (OQ-RC14), the rename of ProfileKey.
const ActiveProviderKey = "active_provider"

// ActiveProviderEveryAgent is the `active_provider` entry for every agent the key does not name:
// the rename of the `profile` key's "*" (RC-D4).
const ActiveProviderEveryAgent = "default"

// NativeProvider is the one reserved word in a selection (OQ-RC20): the agent is left to its own
// login. A provider, alias or profile may not be named it.
const NativeProvider = "native"

// ConfigActiveSelection is cfg's selection, lowered: its `active_provider` key when it has one,
// else its legacy `profile` key (ProfileSelectionOf), else nothing. It is the one reader every
// notch folds (FoldProfiles), so `-p`, `active_provider` and `profile` mean one thing. A malformed
// entry lowers to what can be read, as ProfileSelectionOf does; validation is where the refusal
// is said.
func ConfigActiveSelection(cfg *jsonx.OrderedMap) ProfileSelection {
	if cfg == nil {
		return ProfileSelection{}
	}
	if raw, present := cfg.Get(ActiveProviderKey); present {
		s, _ := activeProviderSelectionOf(raw)
		s.Key = ActiveProviderKey
		return s
	}
	return legacyProfileSelection(cfg)
}

// legacyProfileSelection is the `profile` key, lowered: the format-1 spelling ConfigActiveSelection
// falls back to when the config holds no `active_provider`.
func legacyProfileSelection(cfg *jsonx.OrderedMap) ProfileSelection {
	if cfg == nil {
		return ProfileSelection{}
	}
	v, _ := cfg.Get(ProfileKey)
	s, _ := ProfileSelectionOf(v)
	return s
}

// activeProviderSelectionOf lowers an `active_provider` table: its "default" entry selects for
// every agent the table does not name, every other entry for the agent it names. An entry is a
// name or a list of names (the agent's active set). ok is false for a value that is not a table
// and for an entry that is neither; the readable rest still lowers.
func activeProviderSelectionOf(v any) (ProfileSelection, bool) {
	var s ProfileSelection
	table, isTable := v.(*jsonx.OrderedMap)
	if !isTable {
		return s, v == nil
	}
	ok := true
	for _, key := range table.Keys() {
		entry, _ := table.Get(key)
		set, entryOK := profileSetValue(entry)
		if !entryOK {
			ok = false
			continue
		}
		if key == ActiveProviderEveryAgent {
			s.Default = set
			continue
		}
		if s.Named == nil {
			s.Named = map[string][]string{}
		}
		s.Named[key] = set
	}
	return s, ok
}

// selectsThroughActiveProvider reports whether cfg's `providers` table is the readable declaration
// table: cfg holds the readable selection key, or, selecting through neither key and with no
// legacy user file, one of its providers declares what an old profile became (RC-D32): a starting
// `model` or `variants`. A config that selects nothing still declares those, and `-p` must find
// them (RC-D43). The other readable-only keys (`alias`, `via`, `every_model`) declare no
// selectable model, so they do not switch the namespace, and a legacy JSONC user file keeps its
// old namespace whatever its table spells (the launch refuses those keys there).
func selectsThroughActiveProvider(cfg *jsonx.OrderedMap) bool {
	if _, present := cfg.Get(ActiveProviderKey); present {
		return true
	}
	if _, legacy := cfg.Get(ProfileKey); legacy {
		return false
	}
	if filepath.Ext(paths.UserConfigDisplayPath()) != ".toml" {
		return false
	}
	raw, _ := cfg.Get(providersKey)
	table, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	readable := map[string]bool{"model": true, "variants": true}
	for _, name := range table.Keys() {
		v, _ := table.Get(name)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, k := range entry.Keys() {
			if readable[k] {
				return true
			}
		}
	}
	return false
}

// selectsNative reports whether set is the reserved selection "native": the agent left to its own
// login, which a fold writes as no profile for it.
func selectsNative(set []string) bool { return len(set) == 1 && set[0] == NativeProvider }

// SelectableProfiles is user with an implied profile added for every provider name, alias and
// `<provider>/<variant>` the selected packs and cfg's `providers` declare that no profile in packs
// or user already names — the one namespace a selection resolves against. The provider
// declarations are composed as the launch composes them (packload.ComposeProviderDeclarations)
// and checked as one namespace first (ResolveActiveProviders), so a provider, alias or variant
// named "native", or two declarations claiming one name, refuse here, naming the fix.
//
// An implied profile carries what a profile can: the provider, its via, its starting model as the
// `model` option, and a variant's option values, enforce_models and subagents. The model is
// omitted when the provider declares options that do not include `model`, because the profile
// census (packload.ResolveProfiles) would refuse the option on every launch, not only one that
// selects the name. A variant's capabilities and protocols have no profile field, so they stay with
// the provider semantics (SelectedProvider) and are not carried here.
//
// cfg's own `providers` table joins the namespace whole when cfg selects through `active_provider`:
// that is the readable file's key, and its `providers` table is the typed declaration
// ComposeProviderDeclarations reads. A config still selecting through the legacy `profile` key
// holds the legacy `providers` shape (a `models` role map, read by packload.ComposeProviders), so
// only the entries a TOML file wrote join it (TOMLWrittenProviders): a piecemeal mix's legacy
// `profile` names what its TOML files declare as active_provider would (RC-D57), and a bare
// variant name a TOML file declares under one provider is that `<provider>/<variant>`
// (LegacyProfileVariant), since RC-D32 converted the profile of that name to it.
func SelectableProfiles(user map[string]packload.UserProfile, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) (map[string]packload.UserProfile, error) {
	declarations, authored, legacyKey, err := selectionDeclarations(cfg, packs)
	if err != nil {
		return nil, err
	}
	out, err := withImpliedProfiles(user, declarations, packs)
	if err != nil || !legacyKey || authored == nil {
		return out, err
	}
	// The legacy key's bare variant names (RC-D57): each is its implied `<provider>/<variant>`, and a
	// profile, provider or alias already holding the name keeps it.
	for _, name := range authored.Keys() {
		raw, _ := authored.Get(name)
		entry, _ := raw.(*jsonx.OrderedMap)
		for _, variant := range entryMap(entry, "variants").Keys() {
			selection, ok := LegacyProfileVariant(authored, variant)
			if !ok {
				continue
			}
			if _, held := out[variant]; held || packProfileDeclared(packs, variant) {
				continue
			}
			if profile, implied := out[selection]; implied {
				out[variant] = profile
			}
		}
	}
	return out, nil
}

// selectionDeclarations is the provider declarations SelectableProfiles resolves names against,
// composed as the launch composes them and checked as one namespace (RC-D68), with the TOML-written
// entries of a legacy-key config (authored, nil otherwise) and whether cfg selects through the
// legacy `profile` key.
func selectionDeclarations(cfg *jsonx.OrderedMap, packs []*packload.Pack) ([]packdecl.ProviderContribution,
	*jsonx.OrderedMap, bool, error) {
	var authored *jsonx.OrderedMap
	legacyKey := false
	if cfg != nil && selectsThroughActiveProvider(cfg) {
		if raw, present := cfg.Get(providersKey); present && raw != nil {
			table, ok := raw.(*jsonx.OrderedMap)
			if !ok {
				return nil, nil, false, fmt.Errorf("config.%s: expected a table of provider declarations", providersKey)
			}
			authored = table
		}
	} else if cfg != nil {
		_, legacyKey = cfg.Get(ProfileKey)
		authored = TOMLWrittenProviders(cfg)
	}
	declarations, err := packload.ComposeProviderDeclarations(authored, packs)
	if err != nil && authored != nil && !selectsThroughActiveProvider(cfg) {
		// A TOML entry that the legacy entries merged into it leave unreadable as a declaration
		// keeps the legacy namespace as it read before RC-D57, rather than refusing a launch that
		// read before it.
		authored = nil
		declarations, err = packload.ComposeProviderDeclarations(nil, packs)
	}
	if err != nil {
		return nil, nil, false, err
	}
	if _, err := buildProviderNamespace(declarations); err != nil {
		return nil, nil, false, fmt.Errorf("config.%s: %w. Rename it in your config's `%s` table, or deselect "+
			"the pack that declares it", providersKey, err, providersKey)
	}
	return declarations, authored, legacyKey, nil
}

// ReplacedNameRefusals is, for each name a `name` replaced in the provider declarations cfg's
// `providers` table and packs compose to, the refusal of selecting it (RC-D68): `openrouter is
// named "or" in your config (providers.openrouter.name): -p or`. A name a profile or another
// declaration holds is not in it, since that holder selects it. Nil when there are none or the
// declarations do not compose, which SelectableProfiles refuses in its own words.
func ReplacedNameRefusals(cfg *jsonx.OrderedMap, packs []*packload.Pack, user map[string]packload.UserProfile) map[string]string {
	declarations, _, _, err := selectionDeclarations(cfg, packs)
	if err != nil {
		return nil
	}
	ns, err := buildProviderNamespace(declarations)
	if err != nil || len(ns.replaced) == 0 {
		return nil
	}
	held := map[string]bool{}
	for _, name := range packload.DeclaredProfileNames(packs, user) {
		held[name] = true
	}
	out := map[string]string{}
	for name, refusal := range ns.replaced {
		if !held[name] {
			out[name] = refusal
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TOMLWrittenProviders is the entries of cfg's `providers` table a TOML file wrote: those holding a
// key only the readable vocabulary spells (readableOnlyProviderKeys), which a legacy file is
// refused. Nil when there are none.
func TOMLWrittenProviders(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	raw, _ := cfg.Get(providersKey)
	table, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	readable := readableOnlyProviderKeys()
	var out *jsonx.OrderedMap
	for _, name := range table.Keys() {
		v, _ := table.Get(name)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, k := range entry.Keys() {
			if readable[k] {
				if out == nil {
					out = jsonx.NewOrderedMap()
				}
				out.Set(name, entry)
				break
			}
		}
	}
	return out
}

// LegacyProfileVariant is what a legacy `profile` selection's name means when it is a bare variant
// name in a TOML-written providers table (TOMLWrittenProviders): `<provider>/<name>`, when exactly
// one provider there declares that variant and none is named or aliased name. RC-D32 converts a
// profile to a variant of its own name, so a selection still naming the profile names that
// variant (RC-D57). A profile holding the name keeps it; the caller checks that.
func LegacyProfileVariant(providers *jsonx.OrderedMap, name string) (string, bool) {
	if providers == nil || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	found := ""
	for _, provider := range providers.Keys() {
		raw, _ := providers.Get(provider)
		entry, _ := raw.(*jsonx.OrderedMap)
		if entry == nil {
			continue
		}
		own := provider
		if written, ok := entry.Get("name"); ok {
			if text, isString := written.(string); isString && text != "" {
				own = text
			}
		}
		if provider == name || own == name || writtenNamesHold(entry, "alias", name) {
			return "", false
		}
		if raw, declared := entryMap(entry, "variants").Get(name); declared {
			if found != "" {
				return "", false
			}
			found = own + "/" + name
			if variant, ok := raw.(*jsonx.OrderedMap); ok {
				if written, ok := variant.Get("name"); ok {
					if text, isString := written.(string); isString && text != "" {
						found = text
					}
				}
			}
		}
	}
	return found, found != ""
}

// writtenNamesHold reports whether m[key], a name or a list of names (an `alias`, RC-D68), holds
// name.
func writtenNamesHold(m *jsonx.OrderedMap, key, name string) bool {
	raw, _ := m.Get(key)
	switch value := raw.(type) {
	case string:
		return value == name
	case []any:
		for _, item := range value {
			if item == name {
				return true
			}
		}
	}
	return false
}

// entryMap is m[key] as a table, an empty one when m or the value is not a table.
func entryMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m != nil {
		if raw, ok := m.Get(key); ok {
			if t, ok := raw.(*jsonx.OrderedMap); ok {
				return t
			}
		}
	}
	return jsonx.NewOrderedMap()
}

// packProfileDeclared reports whether a pack in packs declares a profile named name.
func packProfileDeclared(packs []*packload.Pack, name string) bool {
	for _, declared := range packload.DeclaredProfileNames(packs, nil) {
		if declared == name {
			return true
		}
	}
	return false
}

// withImpliedProfiles is SelectableProfiles over declarations already composed and checked: an
// implied profile for every name the namespace holds (buildProviderNamespace).
//
// A name a profile already holds: the user's legacy `profiles` entry keeps it (RC-D68 leaves the
// legacy JSONC path alone), and so does a pack's legacy `[[profile]]` over the same provider; a
// pack's `[[profile]]` over another provider is a collision, fatal like every other (RC-D68). A
// former name a selected pack records (`formerly`, PE-D7) selects nothing, and is refused naming
// its new name (OQ-PE3), even where a provider of that name is declared (copilot-auth's
// `github-copilot`).
func withImpliedProfiles(user map[string]packload.UserProfile, declarations []packdecl.ProviderContribution,
	packs []*packload.Pack) (map[string]packload.UserProfile, error) {
	ns, err := buildProviderNamespace(declarations)
	if err != nil {
		return nil, err
	}
	packProfiles := map[string]string{}
	former := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, prof := range p.Decl.Profiles() {
			packProfiles[prof.Name] = prof.Provider
		}
		for old := range p.Decl.FormerNames().FormerSelectionNames() {
			former[old] = true
		}
	}
	out := make(map[string]packload.UserProfile, len(user)+len(ns.order))
	for name, profile := range user {
		out[name] = profile
	}
	for _, name := range ns.order {
		target := ns.names[name]
		if _, held := user[name]; held || former[name] {
			continue
		}
		if provider, held := packProfiles[name]; held {
			if provider == target.provider {
				continue
			}
			return nil, fmt.Errorf("config.%s: provider namespace collision %q between a pack's profile over "+
				"provider %s and %s; every name selects one configuration, so rename one in your config's `%s` "+
				"table, or deselect the pack that declares it", providersKey, name, provider, target.what, providersKey)
		}
		d := ns.byName[target.provider]
		if target.variant == "" {
			base := packload.UserProfile{Provider: d.Name, Via: d.Via}
			if model := impliedModel(d, d.Model); model != "" {
				base.Options = map[string]string{"model": model}
			}
			out[name] = base
			continue
		}
		v := d.Variants[target.variant]
		subagents, err := selectedSubagents(v.Subagents)
		if err != nil {
			return nil, fmt.Errorf("providers.%s.variants.%s: %w", d.Name, target.variant, err)
		}
		profile := packload.UserProfile{Provider: d.Name, Via: firstString(v.Via, d.Via),
			EnforceModels: v.EnforceModels, Subagents: subagents}
		for option, value := range v.Options {
			if !value.Defaulted {
				continue
			}
			if profile.Options == nil {
				profile.Options = map[string]string{}
			}
			profile.Options[option] = value.Value
		}
		if model := impliedModel(d, firstString(v.Model, d.Model)); model != "" {
			if profile.Options == nil {
				profile.Options = map[string]string{}
			}
			profile.Options["model"] = model
		}
		out[name] = profile
	}
	return out, nil
}

// impliedModel is model when an implied profile of d may carry it as the `model` option: d
// declares no options, or declares `model` among them.
func impliedModel(d packdecl.ProviderContribution, model string) string {
	if model == "" {
		return ""
	}
	if len(d.Options) == 0 {
		return model
	}
	if _, declared := d.Options["model"]; declared {
		return model
	}
	return ""
}

// LoadSelectableProfiles is LoadProfiles (the user's `profiles`, at user scope) with
// SelectableProfiles' implied profiles over packs and cfg's `providers`: what a reader that
// resolves a selection hands packload.ResolveProfiles.
func LoadSelectableProfiles(warn Warn, cfg *jsonx.OrderedMap, packs []*packload.Pack) (map[string]packload.UserProfile, error) {
	user, err := LoadProfiles(warn)
	if err != nil {
		return nil, err
	}
	return SelectableProfiles(user, cfg, packs)
}
