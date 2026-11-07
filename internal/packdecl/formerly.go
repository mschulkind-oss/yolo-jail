package packdecl

// formerly.go is the `formerly` key: the names a pack and its profiles were known by before a
// rename, so a config still naming an old one is refused with the new name rather than with a
// list of everything that exists (docs/design/provider-enablement.md OQ-PE3, PE-D7).
//
// # Why it is a manifest fact and not a core table
//
// The renames it records are packs' own (`openai-auth` became `codex-auth`, the `github-copilot`
// pack became `copilot-auth`, and its profile `github-copilot` became `copilot`), and core
// switches on no pack's name: "AGENTS ARE PACKS" and the provider-enablement rule both put such
// facts in the manifest. So the pack that took a name over says so, and the two refusals that
// read it (an unknown `packs` entry, an undeclared profile) word their message from the shipped
// manifests. Nothing is aliased: a former name selects nothing and declares nothing.
//
// Top-level, beside `needs`, for needs.go's reason: it delivers nothing at a target and owns no
// target, and an older reader ignores the unknown key instead of dropping a contribution.

import (
	"fmt"
	"sort"
)

// Formerly is a pack's record of the names it and its profiles had before a rename.
type Formerly struct {
	// Packs are names this pack had before. A `packs` entry naming one is refused naming this
	// pack.
	Packs []string `json:"packs,omitempty"`
	// Profiles maps a former profile name to the profile this pack declares now. A selection
	// naming the former one, which nothing declares, is refused naming the new one.
	//
	// The legacy spelling of Providers, from before `[[profile]]` was retired (RC-D69).
	Profiles map[string]string `json:"profiles,omitempty"`
	// Providers maps a former selection name to the name one of this pack's providers answers to
	// now: its id, an alias, or a variant's name (RC-D69). A selection naming the former one is
	// refused naming the new one, and the former name selects nothing, even where it is the id
	// of a provider this pack ships (copilot-auth's `github-copilot`, renamed `copilot`, an
	// alias of the provider `github-copilot`, OQ-PE3).
	Providers map[string]string `json:"providers,omitempty"`
}

// FormerSelectionNames is every former selection name the record maps to its new name: Providers
// and its legacy spelling, Profiles. Nil when there are none.
func (f *Formerly) FormerSelectionNames() map[string]string {
	if f == nil || (len(f.Profiles) == 0 && len(f.Providers) == 0) {
		return nil
	}
	out := make(map[string]string, len(f.Profiles)+len(f.Providers))
	for old, now := range f.Profiles {
		out[old] = now
	}
	for old, now := range f.Providers {
		out[old] = now
	}
	return out
}

// FormerNames returns the pack's Formerly record, nil when it declares none. THE accessor.
func (m *Manifest) FormerNames() *Formerly { return m.Formerly }

// validateFormerly refuses a record no refusal could use as written: a former pack name that is
// not a pack name, an empty former profile name, or one mapped to a profile this pack does not
// declare, which would send the user to a name that is refused in turn.
func (m *Manifest) validateFormerly() []string {
	if m.Formerly == nil {
		return nil
	}
	var problems []string
	for i, name := range m.Formerly.Packs {
		if !ValidPackName(name) {
			problems = append(problems, fmt.Sprintf("formerly.packs[%d]: %q is not a pack name", i, name))
		}
	}
	declared := map[string]bool{}
	for _, p := range m.Profiles() {
		declared[p.Name] = true
	}
	olds := make([]string, 0, len(m.Formerly.Profiles))
	for old := range m.Formerly.Profiles {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	for _, old := range olds {
		now := m.Formerly.Profiles[old]
		switch {
		case old == "":
			problems = append(problems, "formerly.profiles: a former profile name must not be \"\"")
		case declared[old]:
			problems = append(problems, fmt.Sprintf("formerly.profiles.%s: this pack still declares "+
				"a profile named %q, so it is not a former name", old, old))
		case !declared[now]:
			problems = append(problems, fmt.Sprintf("formerly.profiles.%s: %q is not a profile this "+
				"pack declares, so the refusal would name a profile that is refused in turn", old, now))
		}
	}
	problems = append(problems, m.validateFormerProviders()...)
	return problems
}

// validateFormerProviders refuses a `formerly.providers` entry no refusal could use: an empty or
// unwritable former name, one an alias or variant of this pack still answers to, or one mapped to
// a name none of this pack's providers answers to.
func (m *Manifest) validateFormerProviders() []string {
	ids := map[string]bool{}
	answers := map[string]bool{}
	for _, p := range m.Providers() {
		ids[p.Name] = true
		for _, name := range p.SelectionNames() {
			answers[name] = true
		}
	}
	olds := make([]string, 0, len(m.Formerly.Providers))
	for old := range m.Formerly.Providers {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	var problems []string
	for _, old := range olds {
		now := m.Formerly.Providers[old]
		switch {
		case SelectionNameProblem(old) != "":
			problems = append(problems, fmt.Sprintf(formerProvidersLabel+": the former name %q %s", old, SelectionNameProblem(old)))
		case answers[old] && !ids[old]:
			problems = append(problems, fmt.Sprintf(formerProvidersLabel+".%s: a provider of this pack still "+
				"answers to %q, so it is not a former name", old, old))
		case !answers[now]:
			problems = append(problems, fmt.Sprintf(formerProvidersLabel+".%s: no provider of this pack answers "+
				"to %q, so the refusal would name a name that is refused in turn", old, now))
		}
	}
	return problems
}

// formerProvidersLabel heads every formerly.providers problem, so the TOML decode can re-run the
// check once a keyed provider's aliases and variants are attached.
const formerProvidersLabel = "formerly.providers"
