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

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
// table: cfg holds the readable selection key, or, selecting through neither key, one of its
// providers spells a key only the readable table has (`model`, `variants`). A config that selects
// nothing still declares those (RC-D32 writes an old profile there), and `-p` must find them
// (RC-D43); a legacy table cannot spell them, so a legacy config keeps its old namespace.
func selectsThroughActiveProvider(cfg *jsonx.OrderedMap) bool {
	if _, present := cfg.Get(ActiveProviderKey); present {
		return true
	}
	if _, legacy := cfg.Get(ProfileKey); legacy {
		return false
	}
	raw, _ := cfg.Get(providersKey)
	table, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	readable := readableOnlyProviderKeys()
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
// cfg's own `providers` table joins the namespace only when cfg selects through `active_provider`:
// that is the readable file's key, and its `providers` table is the typed declaration
// ComposeProviderDeclarations reads. A config still selecting through the legacy `profile` key
// holds the legacy `providers` shape (a `models` role map, read by packload.ComposeProviders), so
// only its selected packs' declarations are added, and its own providers stay named by its
// `profiles` as before.
func SelectableProfiles(user map[string]packload.UserProfile, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) (map[string]packload.UserProfile, error) {
	var authored *jsonx.OrderedMap
	if cfg != nil && selectsThroughActiveProvider(cfg) {
		if raw, present := cfg.Get(providersKey); present && raw != nil {
			table, ok := raw.(*jsonx.OrderedMap)
			if !ok {
				return nil, fmt.Errorf("config.%s: expected a table of provider declarations", providersKey)
			}
			authored = table
		}
	}
	declarations, err := packload.ComposeProviderDeclarations(authored, packs)
	if err != nil {
		return nil, err
	}
	if _, err := ResolveActiveProviders(declarations, ActiveProviderSelection{}, nil); err != nil {
		return nil, fmt.Errorf("config.%s: %w. Rename it in your config's `%s` table, or deselect "+
			"the pack that declares it", providersKey, err, providersKey)
	}
	return withImpliedProfiles(user, declarations, packs)
}

// withImpliedProfiles is SelectableProfiles over declarations already composed and checked.
func withImpliedProfiles(user map[string]packload.UserProfile, declarations []packdecl.ProviderContribution,
	packs []*packload.Pack) (map[string]packload.UserProfile, error) {
	taken := map[string]bool{}
	for _, name := range packload.DeclaredProfileNames(packs, user) {
		taken[name] = true
	}
	// A former profile name a selected pack records (`formerly.profiles`, PE-D7) is refused naming
	// its new name, and nothing is aliased (OQ-PE3): a provider sharing that name (copilot-auth's
	// `github-copilot`) does not make it selectable again.
	for _, p := range packs {
		if p == nil || p.Decl == nil || p.Decl.FormerNames() == nil {
			continue
		}
		for old := range p.Decl.FormerNames().Profiles {
			taken[old] = true
		}
	}
	out := make(map[string]packload.UserProfile, len(user)+len(declarations))
	for name, profile := range user {
		out[name] = profile
	}
	add := func(name string, profile packload.UserProfile) {
		if name == "" || name == NativeProvider || taken[name] {
			return
		}
		taken[name] = true
		out[name] = profile
	}
	for _, d := range declarations {
		base := packload.UserProfile{Provider: d.Name, Via: d.Via}
		if model := impliedModel(d, d.Model); model != "" {
			base.Options = map[string]string{"model": model}
		}
		add(d.Name, base)
		add(d.Alias, base)
		for _, name := range sortedVariantNames(d.Variants) {
			v := d.Variants[name]
			subagents, err := selectedSubagents(v.Subagents)
			if err != nil {
				return nil, fmt.Errorf("providers.%s.variants.%s: %w", d.Name, name, err)
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
			add(d.Name+"/"+name, profile)
		}
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

// sortedVariantNames is a provider's variant names in a stable order.
func sortedVariantNames(variants map[string]packdecl.ProviderVariant) []string {
	names := make([]string, 0, len(variants))
	for name := range variants {
		names = append(names, name)
	}
	sortStrings(names)
	return names
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
