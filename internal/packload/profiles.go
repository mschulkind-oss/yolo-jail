package packload

// profiles.go resolves the PROFILES table a launch feeds its derives: what each
// profile NAME means, as a provider plus the option values that are active under it
// (provider-catalog-and-selection.md §5.2 — a profile is a NAMED SELECTION OVER A
// PROVIDER, user intent over a surface the provider defines).
//
// The resolution happens HERE, in the host CLI, exactly once per launch, and its output
// is what crosses to the jail as YOLO_PROFILES — the same one-composition rule
// providers.go states for YOLO_PROVIDERS, and for the same reason: the in-jail side
// reads the table verbatim and never re-derives it, so a second implementation of this
// merge has nowhere to live and a first one in the entrypoint would be the drift.
//
// Three inputs make a resolution:
//
//   - the selected packs' shipped `kind: "profile"` declarations, which today name a
//     provider and carry no option values (§5.2 sends their BODIES to the `profile:`
//     modifier, which is the later shrink this file deliberately does not do);
//   - the user's `profiles` config entries, read at USER SCOPE by config.LoadProfiles
//     and handed here already lowered (OQ-CS5 — the same scope rule `packs` follows);
//   - the options the RESOLVED provider declares (OQ-CS4), which are the only schema a
//     profile's values answer to and the only census core applies (OQ-CS7).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// UserProfile is one user-declared profile entry: the provider it selects and the
// option values it states — only what it changes, per §5.2's definition.
//
// config.LoadProfiles is this type's producer and the only place a raw JSONC entry
// becomes one, which is what keeps the user scope where the reading happens; packload
// sees the lowered form, never the config file.
type UserProfile struct {
	// Provider is the profile's selection — property 3's MANDATORY declaration. An
	// entry without one is refused by the config layer before it reaches here.
	Provider string

	// Options are the profile's own values, keyed by option NAME. Free strings: core
	// validates no VALUE (OQ-CS7) — what an option means is the derive's business, and
	// the only thing core checks is that the NAME is one the provider declares.
	Options map[string]string
}

// ResolvedProfile is one profile after resolution: the provider it selects and the full
// option map a derive reads as ctx.profile — declared defaults UNDER the profile's own
// values, so a derive never has to know whether a value was stated or inherited.
type ResolvedProfile struct {
	Provider string
	Options  map[string]string
}

// ResolveProfiles returns the resolved table for a launch: every profile name anything
// declares — a selected pack's kind:profile OR the user's `profiles` entry — mapped to
// its ResolvedProfile.
//
// THE TABLE IS THE WHOLE DECLARED SET, not just the names this launch activates. The
// activation decision (which profile is active for a given agent) is made per surface
// downstream, from YOLO_USE_PROFILES; shipping only the active names would make the
// table's contents depend on a value that arrives separately, and the jail side would
// then have to refuse a lookup this side already knew the answer to.
//
// Merge order, per name, bottom to top (the providers convention, pack under user):
//
//  1. the resolved provider's DECLARED DEFAULTS — every option the provider declares
//     that carries a default value. A declared option with NO default (the null
//     spelling, OQ-CS7) composes nothing here, so a profile that does not set it
//     reaches the derive as nothing, which is exactly what "declared, no default"
//     promises;
//  2. the pack-shipped profile's own values — none exist today, because the kind
//     carries no options field; the layer is here so the merge has one shape and the
//     shrink that moves a body into options does not re-order it;
//  3. the user's own values — the intent the definition puts on top.
//
// Two refusals, both fatal, because a launch that silently mis-composes a profile is
// indistinguishable from a working one:
//
//   - an option NAME the resolved provider does not declare (OQ-CS7): the provider owns
//     the schema, so the refusal names what it accepts. Asked only when the provider
//     DECLARES at least one option — a provider with no `options` imposes no census, or
//     every profile over today's shipped providers would be refused on sight;
//   - a profile whose resolution ends with NO provider at all (property 3): a name
//     that selects nothing is not a profile. Unreachable through config (the config
//     layer refuses an entry without `provider`) and unreachable through a validated
//     manifest (a kind:profile must name its requires_provider), so it is the belt for
//     a caller that skipped one of those — and the brace that makes this function total.
//
// A name both sides declare is the §5.2 "customize the pack's own profile" case: the
// user's entry keeps the pack's provider when it states none, and wins per option key.
// A name only a pack declares resolves to the pack's provider plus that provider's
// defaults — the shipped profile is a DEFAULT a user overrides, never a second schema.
func ResolveProfiles(packs []*Pack, user map[string]UserProfile) (map[string]ResolvedProfile, error) {
	shipped := packShippedProfiles(packs)
	options := providerOptions(packs)

	names := make([]string, 0, len(shipped)+len(user))
	for name := range shipped {
		names = append(names, name)
	}
	for name := range user {
		if _, dup := shipped[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	out := make(map[string]ResolvedProfile, len(names))
	var problems []string
	for _, name := range names {
		userProf, fromUser := user[name]
		packProf, fromPack := shipped[name]

		provider := ""
		switch {
		case fromUser && userProf.Provider != "":
			provider = userProf.Provider
		case fromPack:
			provider = profileProvider(packProf)
		case fromUser:
			provider = userProf.Provider // "" — the belt, refused below
		}
		if provider == "" {
			problems = append(problems, fmt.Sprintf(
				"profile %q names no provider — a profile is a selection over a provider, "+
					"so the name declares nothing", name))
			continue
		}

		declared := options[provider]
		opts := make(map[string]string, len(declared)+len(userProf.Options))
		for _, key := range sortedOptionNames(declared) {
			if d := declared[key]; d.Defaulted {
				opts[key] = d.Value
			}
		}
		for _, key := range sortedMapKeys(userProf.Options) {
			if _, ok := declared[key]; !ok && len(declared) > 0 {
				problems = append(problems, undeclaredOptionMessage(name, provider, key, declared))
				continue
			}
			opts[key] = userProf.Options[key]
		}
		out[name] = ResolvedProfile{Provider: provider, Options: opts}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("profiles: %s", strings.Join(problems, "\nprofiles: "))
	}
	return out, nil
}

// packShippedProfiles returns every `kind: "profile"` the selected packs declare, keyed
// by name, first pack in delivery order winning — the same resolution ProviderFor
// applies (bin-owner first, then delivery order) with no bin to key on. A name claimed
// across packs is not refused here: the selector fold downstream already had to pick
// one, and refusing would move a decision that has always been "first wins" into the
// one pass that can least afford to invent a second rule for it.
func packShippedProfiles(packs []*Pack) map[string]packdecl.ProfileContribution {
	out := map[string]packdecl.ProfileContribution{}
	for _, p := range packs {
		for _, prof := range p.Decl.Profiles() {
			if _, seen := out[prof.Name]; seen {
				continue
			}
			out[prof.Name] = prof
		}
	}
	return out
}

// profileProvider is the provider a pack-shipped profile selects.
//
// COMPAT SHIM, and the kind:profile shrink deletes it with its rename: the kind spells
// the field `requires_provider` today, and §5.2 renames the concept to `provider` — a
// profile does not REQUIRE a provider, it IS a selection of one. Read HERE only, in the
// lowering, never in the schema: the schema keeps the one spelling until the shrink
// lands, so a manifest author has exactly one word to write and no reason to learn this
// function exists.
func profileProvider(p packdecl.ProfileContribution) string {
	return p.RequiresProvider
}

// providerOptions returns each pack-declared provider's `options` map, keyed by
// provider name, first shipper winning — ComposeProviders' convention, so the census
// and the composed table can never disagree about which pack's declaration applies.
func providerOptions(packs []*Pack) map[string]map[string]packdecl.OptionDefault {
	out := map[string]map[string]packdecl.OptionDefault{}
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			if _, seen := out[prov.Name]; !seen {
				out[prov.Name] = prov.Options
			}
		}
	}
	return out
}

// undeclaredOptionMessage is the census refusal, and the ONE message for it (OQ-CS7):
// the option, the provider that does not have it, and what the provider does accept.
// A provider that declares nothing never reaches here — see ResolveProfiles.
func undeclaredOptionMessage(profile, provider, key string, declared map[string]packdecl.OptionDefault) string {
	return fmt.Sprintf("profile %q: option %q is not declared by provider %q "+
		"(declared: %s) — a profile states only what its provider's options define",
		profile, key, provider, strings.Join(sortedOptionNames(declared), ", "))
}

// DeclaredProfileNames returns every profile name this launch could activate: the
// selected packs' kind:profile names plus the user's own entries.
//
// It is the OQ-CS6 check's source of truth — declaration is MANDATORY, so a name in
// use_profiles or on -p that answers to neither is a reportable error, not a silent
// no-op — and it is a FUNCTION rather than a private set so the launch pre-flight and
// the host notch cannot grow different ideas of what "declared" means.
func DeclaredProfileNames(packs []*Pack, user map[string]UserProfile) []string {
	shipped := packShippedProfiles(packs)
	out := make([]string, 0, len(shipped)+len(user))
	for name := range shipped {
		out = append(out, name)
	}
	for name := range user {
		if _, dup := shipped[name]; !dup {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// UndeclaredProfileMessage explains a selected name nothing declares, listing what IS
// declared — the same shape unknownProfileCLIMessage and unknownEmbeddedMessage take,
// because a typo is the likely cause and the real list is the whole fix.
func UndeclaredProfileMessage(name string, declared []string) string {
	have := "none"
	if len(declared) > 0 {
		have = strings.Join(declared, ", ")
	}
	return fmt.Sprintf("no profile named %q is declared — a profile name must be declared "+
		"by a selected pack's manifest or by your config's `profiles` key (declared: %s)",
		name, have)
}

// ProfilesWireTable renders the resolved table as the object that travels in
// YOLO_PROFILES: profile name → {provider, <options in sorted order>}. Deterministic by
// construction — a Go map has no order, and this is an env var the jail parses — so an
// unchanged resolution yields byte-identical argv.
func ProfilesWireTable(resolved map[string]ResolvedProfile) *jsonx.OrderedMap {
	names := make([]string, 0, len(resolved))
	for name := range resolved {
		names = append(names, name)
	}
	sort.Strings(names)
	out := jsonx.NewOrderedMap()
	for _, name := range names {
		r := resolved[name]
		entry := jsonx.NewOrderedMap()
		entry.Set("provider", r.Provider)
		for _, key := range sortedMapKeys(r.Options) {
			entry.Set(key, r.Options[key])
		}
		out.Set(name, entry)
	}
	return out
}

// sortedOptionNames returns a declared-options map's keys sorted.
func sortedOptionNames(m map[string]packdecl.OptionDefault) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
