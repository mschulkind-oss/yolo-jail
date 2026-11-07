package packdecl

// profilefold.go is what `yolo pack migrate` turns a pack's legacy `[[profile]]` into
// (docs/design/readable-config.md RC-D69, retiring `[[profile]]` in a pack.toml): a profile that
// only names its provider under another name becomes that provider's `alias`, the same settings
// under another name (RC-D68), and one that changes a setting (its `via`, its `enforce_models`)
// becomes a variant with that `name`, a version with different settings. Only a profile whose
// provider the same manifest declares folds, since a manifest may not add an alias or a variant to
// another pack's provider; the rest stay legacy, with the reason. `formerly.profiles` becomes
// `formerly.providers`.

import (
	"fmt"
	"strings"
)

// ProfileFold is one `[[profile]]` folded into its provider: as an alias (Variant ""), or as the
// variant keyed Variant, whose `name` is the profile's name.
type ProfileFold struct {
	// Index is the profile's contribution index in the manifest folded.
	Index    int
	Profile  string
	Provider string
	Variant  string
	// Value is the variant written, when Variant is set.
	Value ProviderVariant
}

// KeptProfile is a `[[profile]]` the fold leaves legacy, and why.
type KeptProfile struct {
	Index   int
	Profile string
	Reason  string
}

// FoldProfiles plans the fold of m's `[[profile]]` entries into its providers, in contribution
// order. It reads m and changes nothing; FoldedProfiles applies the plan.
func (m *Manifest) FoldProfiles() ([]ProfileFold, []KeptProfile) {
	var folds []ProfileFold
	var kept []KeptProfile
	providers := map[string]ProviderContribution{}
	for _, p := range m.Providers() {
		providers[p.Name] = p
	}
	taken := map[string]map[string]bool{}
	for i, c := range m.Contributes {
		if c.Kind != KindProfile {
			continue
		}
		p, declared := providers[c.Provider]
		if !declared {
			kept = append(kept, KeptProfile{Index: i, Profile: c.Name, Reason: fmt.Sprintf(
				"its provider %q is not declared in this pack, and a pack may not add a name to another "+
					"pack's provider", c.Provider)})
			continue
		}
		if c.Via == "" && c.EnforceModels == nil {
			folds = append(folds, ProfileFold{Index: i, Profile: c.Name, Provider: c.Provider})
			continue
		}
		if taken[c.Provider] == nil {
			taken[c.Provider] = map[string]bool{}
			for name := range p.Variants {
				taken[c.Provider][name] = true
			}
		}
		key := strings.TrimPrefix(c.Name, c.Provider+"-")
		if key == "" || taken[c.Provider][key] {
			key = c.Name
		}
		if taken[c.Provider][key] {
			kept = append(kept, KeptProfile{Index: i, Profile: c.Name, Reason: fmt.Sprintf(
				"provider %q already declares a variant %q", c.Provider, key)})
			continue
		}
		variant := ProviderVariant{Name: c.Name, Via: c.Via, EnforceModels: c.EnforceModels}
		if variant.Via == "" {
			if p.Model == "" {
				kept = append(kept, KeptProfile{Index: i, Profile: c.Name, Reason: fmt.Sprintf(
					"a variant must set model or via, and neither the profile nor provider %q names one", c.Provider)})
				continue
			}
			variant.Model = p.Model
		}
		taken[c.Provider][key] = true
		folds = append(folds, ProfileFold{Index: i, Profile: c.Name, Provider: c.Provider, Variant: key, Value: variant})
	}
	return folds, kept
}

// FoldedProfiles is a copy of m with folds applied: each folded profile removed, its provider
// given the alias or variant, and `formerly.profiles` moved to `formerly.providers`. Every other
// contribution keeps its order.
func (m *Manifest) FoldedProfiles(folds []ProfileFold) *Manifest {
	out := *m
	removed := map[int]bool{}
	byProvider := map[string][]ProfileFold{}
	for _, f := range folds {
		removed[f.Index] = true
		byProvider[f.Provider] = append(byProvider[f.Provider], f)
	}
	out.Contributes = nil
	for i, c := range m.Contributes {
		if removed[i] {
			continue
		}
		if c.Kind == KindProvider && len(byProvider[c.Name]) != 0 {
			c.ProviderAliases = append([]string(nil), c.ProviderAliases...)
			if c.ProviderVariants != nil || hasVariantFold(byProvider[c.Name]) {
				variants := make(map[string]ProviderVariant, len(c.ProviderVariants)+len(byProvider[c.Name]))
				for name, v := range c.ProviderVariants {
					variants[name] = v
				}
				c.ProviderVariants = variants
				c.ProviderVariantOrder = append([]string(nil), c.ProviderVariantOrder...)
			}
			for _, f := range byProvider[c.Name] {
				if f.Variant == "" {
					c.ProviderAliases = append(c.ProviderAliases, f.Profile)
					continue
				}
				c.ProviderVariants[f.Variant] = f.Value
				c.ProviderVariantOrder = append(c.ProviderVariantOrder, f.Variant)
			}
			if len(c.ProviderAliases) == 0 {
				c.ProviderAliases = nil
			}
		}
		out.Contributes = append(out.Contributes, c)
	}
	if m.Formerly != nil && len(m.Formerly.Profiles) != 0 {
		folded := map[string]bool{}
		for _, f := range folds {
			folded[f.Profile] = true
		}
		formerly := *m.Formerly
		formerly.Profiles, formerly.Providers = nil, nil
		for old, now := range m.Formerly.Providers {
			formerly.Providers = setName(formerly.Providers, old, now)
		}
		for old, now := range m.Formerly.Profiles {
			if folded[now] {
				formerly.Providers = setName(formerly.Providers, old, now)
			} else {
				formerly.Profiles = setName(formerly.Profiles, old, now)
			}
		}
		out.Formerly = &formerly
	}
	return &out
}

func setName(m map[string]string, old, now string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[old] = now
	return m
}

func hasVariantFold(folds []ProfileFold) bool {
	for _, f := range folds {
		if f.Variant != "" {
			return true
		}
	}
	return false
}
