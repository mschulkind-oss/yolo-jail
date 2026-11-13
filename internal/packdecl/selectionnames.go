package packdecl

// selectionnames.go is the names a provider declaration answers to in a selection
// (docs/reference/config/rulings.md#RC-D68, RC-D69): a provider's `alias`, a string or a list of
// names for exactly the provider's own configuration; a variant's `name`, which replaces the
// `<provider>/<variant>` spelling, and its `alias`, names for exactly the variant's configuration.
// A provider's own `name` is the user's to write, never a pack's (RC-D68), so a pack's
// `[providers.<id>]` table refuses it and DecodeProviderTable, which reads the user's composed
// table, carries it as ProviderContribution.SelectName.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NameList is a selection name or a list of them: `alias = "or"` or `alias = ["or", "o"]`
// (RC-D68). It marshals one name as a string, so a manifest that wrote a string reads back as
// one, and a list otherwise.
type NameList []string

// UnmarshalJSON reads a string or an array of strings.
func (n *NameList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*n = NameList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("must be a name or a list of names")
	}
	*n = NameList(many)
	return nil
}

// MarshalJSON writes one name as a string and several as a list.
func (n NameList) MarshalJSON() ([]byte, error) {
	if len(n) == 1 {
		return json.Marshal(n[0])
	}
	return json.Marshal([]string(n))
}

// SelectionNameProblem is why name cannot be a written selection name (a provider's or a
// variant's `name`, or an `alias`), or "" when it can: it is not empty, holds no whitespace, and
// holds no `/`, which only the `<provider>/<variant>` default spelling carries (RC-D68: "no written
// name contains `/`").
func SelectionNameProblem(name string) string {
	switch {
	case name == "":
		return "must not be empty"
	case strings.ContainsAny(name, " \t\r\n"):
		return "must not contain whitespace"
	case strings.Contains(name, "/"):
		return "must not contain '/'; only a variant's default spelling, <provider>/<variant>, has one"
	}
	return ""
}

// decodeNameList reads an `alias` value: a name or a list of distinct names, each one a written
// selection name (SelectionNameProblem).
func decodeNameList(label string, raw any) (NameList, error) {
	var names []string
	switch value := raw.(type) {
	case string:
		names = []string{value}
	case []any:
		if len(value) == 0 {
			return nil, fmt.Errorf("%s must name at least one name", label)
		}
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a name or a list of names", label)
			}
			names = append(names, text)
		}
	default:
		return nil, fmt.Errorf("%s must be a name or a list of names", label)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if problem := SelectionNameProblem(name); problem != "" {
			return nil, fmt.Errorf("%s %q %s", label, name, problem)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s lists %q twice", label, name)
		}
		seen[name] = true
	}
	return NameList(names), nil
}

// ProviderNameKeyRefusal is the refusal of a `name` key in a pack's `[providers.<id>]` table: the
// table key is the provider's id, and `name` there is the user's choice of selection name
// (RC-D68: "a pack may not write a provider `name`, only a variant `name`").
func ProviderNameKeyRefusal(id string) string {
	return fmt.Sprintf("providers.%s.name: a pack may not name its provider; the table key %q is its "+
		"id, and a different selection name is the user's to write in their own [providers.%s] "+
		"table. Give it an `alias`, or give one of its variants a `name`", id, id, id)
}

// SelectionNames is every name the provider's own declaration answers to in a selection before any
// user `name` replaces one (RC-D68): its id, its aliases, and for each variant its `name` (else
// `<id>/<variant>`) and its aliases. In declaration order, the variants by VariantOrder.
func (p ProviderContribution) SelectionNames() []string {
	out := []string{p.Name}
	out = append(out, p.Aliases...)
	for _, variant := range p.OrderedVariantNames() {
		v := p.Variants[variant]
		if v.Name != "" {
			out = append(out, v.Name)
		} else {
			out = append(out, p.Name+"/"+variant)
		}
		out = append(out, v.Alias...)
	}
	return out
}

// OrderedVariantNames is the provider's variant names, VariantOrder first and then any it misses
// in sorted order, so a caller walks them the same way every time.
func (p ProviderContribution) OrderedVariantNames() []string {
	return orderedNames(p.VariantOrder, p.Variants)
}

// DeclaresSelectionName reports whether a provider this manifest ships answers to name in a
// selection, by its id, an alias, or a variant's name or default spelling (SelectionNames).
func (m *Manifest) DeclaresSelectionName(name string) bool {
	if m == nil || name == "" {
		return false
	}
	for _, p := range m.Providers() {
		for _, n := range p.SelectionNames() {
			if n == name {
				return true
			}
		}
	}
	return false
}

// SelectionProfiles is Profiles plus one profile for each name a provider of the manifest answers
// to besides its id: each alias, over the provider, and each variant `name` (and its aliases),
// over the provider with the variant's `via` and `enforce_models`. They are what a legacy
// `[[profile]]` written in those places said (RC-D69), so a reader that resolves a pack's
// selection names through its profiles reads a keyed manifest the same way. The legacy profiles
// come last, so a name both spell is the profile's (a later declaration wins).
func (m *Manifest) SelectionProfiles() []ProfileContribution {
	var out []ProfileContribution
	for _, p := range m.Providers() {
		for _, alias := range p.Aliases {
			out = append(out, ProfileContribution{Name: alias, Provider: p.Name})
		}
		for _, key := range p.OrderedVariantNames() {
			v := p.Variants[key]
			if v.Name == "" {
				continue
			}
			for _, name := range append([]string{v.Name}, v.Alias...) {
				out = append(out, ProfileContribution{Name: name, Provider: p.Name, Via: v.Via, EnforceModels: v.EnforceModels})
			}
		}
	}
	return append(out, m.Profiles()...)
}
