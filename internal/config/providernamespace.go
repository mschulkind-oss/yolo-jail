package config

// providernamespace.go is the one set of names the provider declarations answer to in a selection
// (docs/reference/config/rulings.md#RC-D68): each provider id has one entry, its own configuration
// answers to its `name` (the id when unwritten) and its aliases, and each variant answers to its
// `name` (`<provider name>/<variant>` when unwritten) and its aliases. Every collision is fatal
// (the owner, 2026-11-03: *"colliding alias/name should be fatal"*), except an alias equal to its
// own configuration's name, which names nothing new. A name a `name` replaced selects nothing and
// is refused naming its replacement (*"openrouter is named "or" in your config
// (providers.openrouter.name): -p or"*). Agent configs, environment variables and config paths
// keep the id: only the selection namespace changes.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// selectionTarget is what one name in the namespace selects: a provider's own configuration
// (variant "") or one of its variants.
type selectionTarget struct {
	provider, variant string
	// what words the holder in a collision: "provider openrouter", "alias of openrouter", ...
	what string
}

// providerNamespace is the composed declarations' names (buildProviderNamespace).
type providerNamespace struct {
	byName map[string]packdecl.ProviderContribution
	names  map[string]selectionTarget
	// order is every name in names, in declaration order.
	order []string
	// replaced maps a name a `name` replaced to its refusal (replacedRefusal).
	replaced map[string]string
	// display is each provider's own name with its aliases, and each variant's name, for the
	// "Declared:" list of an unknown selection.
	display []string
}

// buildProviderNamespace checks declarations as one namespace and returns it, or the first
// collision or unwritable name, naming both holders.
func buildProviderNamespace(declarations []packdecl.ProviderContribution) (providerNamespace, error) {
	ns := providerNamespace{byName: map[string]packdecl.ProviderContribution{}, names: map[string]selectionTarget{},
		replaced: map[string]string{}}
	claim := func(name string, target selectionTarget) error {
		if prior, exists := ns.names[name]; exists {
			return fmt.Errorf("provider namespace collision %q between %s and %s; every name selects one "+
				"configuration, so rename one", name, prior.what, target.what)
		}
		ns.names[name] = target
		ns.order = append(ns.order, name)
		return nil
	}
	for _, d := range declarations {
		if problem := providerNameProblem(d.Name); problem != "" {
			return ns, fmt.Errorf("provider declaration %s: %s", d.Name, problem)
		}
		if d.Name == NativeProvider {
			return ns, fmt.Errorf("provider name %q is reserved; it means an agent is left to its own login", d.Name)
		}
		if strings.Contains(d.Name, "/") {
			return ns, fmt.Errorf("provider name %q cannot contain '/'; use provider/variant for a variant", d.Name)
		}
		if _, dup := ns.byName[d.Name]; dup {
			return ns, fmt.Errorf("provider %q has two entries; each provider id has one", d.Name)
		}
		ns.byName[d.Name] = d
	}
	for _, d := range declarations {
		own := d.Name
		ownWhat := "provider " + d.Name
		if d.SelectName != "" {
			if err := writtenNameProblem(fmt.Sprintf("providers.%s.name", d.Name), d.SelectName); err != nil {
				return ns, err
			}
			own = d.SelectName
			ownWhat = fmt.Sprintf("the name of provider %s (providers.%s.name)", d.Name, d.Name)
		}
		if err := claim(own, selectionTarget{provider: d.Name, what: ownWhat}); err != nil {
			return ns, err
		}
		if own != d.Name {
			ns.replaced[d.Name] = replacedRefusal(d.Name, own, fmt.Sprintf("providers.%s.name", d.Name), true)
		}
		shown := own
		var aliases []string
		for _, alias := range d.Aliases {
			if err := writtenNameProblem(fmt.Sprintf("providers.%s.alias", d.Name), alias); err != nil {
				return ns, err
			}
			if alias == own {
				continue
			}
			if err := claim(alias, selectionTarget{provider: d.Name, what: "alias of " + d.Name}); err != nil {
				return ns, err
			}
			aliases = append(aliases, alias)
		}
		if len(aliases) != 0 {
			shown += " (" + strings.Join(aliases, ", ") + ")"
		}
		ns.display = append(ns.display, shown)
		for _, variant := range d.OrderedVariantNames() {
			if problem := providerNameProblem(variant); problem != "" {
				return ns, fmt.Errorf("provider %s variant %q: %s", d.Name, variant, problem)
			}
			if strings.Contains(variant, "/") {
				return ns, fmt.Errorf("provider %s has invalid variant name %q", d.Name, variant)
			}
			v := d.Variants[variant]
			for option := range v.Options {
				if _, declared := d.Options[option]; !declared {
					return ns, fmt.Errorf("provider %s variant %s: option %q is not declared by the provider", d.Name, variant, option)
				}
			}
			spelled := own + "/" + variant
			name := spelled
			what := "variant " + spelled
			if v.Name != "" {
				path := fmt.Sprintf("providers.%s.variants.%s.name", d.Name, variant)
				if err := writtenNameProblem(path, v.Name); err != nil {
					return ns, err
				}
				name = v.Name
				what = fmt.Sprintf("the name of variant %s/%s (%s)", d.Name, variant, path)
				ns.replaced[spelled] = replacedRefusal(spelled, name, path, false)
			}
			if err := claim(name, selectionTarget{provider: d.Name, variant: variant, what: what}); err != nil {
				return ns, err
			}
			if own != d.Name {
				ns.replaced[d.Name+"/"+variant] = replacedRefusal(d.Name+"/"+variant, name,
					fmt.Sprintf("providers.%s.name", d.Name), true)
			}
			shownVariant := name
			var variantAliases []string
			for _, alias := range v.Alias {
				if err := writtenNameProblem(fmt.Sprintf("providers.%s.variants.%s.alias", d.Name, variant), alias); err != nil {
					return ns, err
				}
				if alias == name {
					continue
				}
				if err := claim(alias, selectionTarget{provider: d.Name, variant: variant,
					what: fmt.Sprintf("alias of variant %s/%s", d.Name, variant)}); err != nil {
					return ns, err
				}
				variantAliases = append(variantAliases, alias)
			}
			if len(variantAliases) != 0 {
				shownVariant += " (" + strings.Join(variantAliases, ", ") + ")"
			}
			ns.display = append(ns.display, shownVariant)
		}
	}
	// A replaced spelling some declaration still answers to is no longer replaced: the holder
	// selects it.
	for name := range ns.replaced {
		if _, held := ns.names[name]; held {
			delete(ns.replaced, name)
		}
	}
	sort.Strings(ns.display)
	return ns, nil
}

// writtenNameProblem refuses a written selection name (a provider's or variant's `name`, an
// alias) that cannot be one: packdecl.SelectionNameProblem, the reserved word, or a -p separator.
func writtenNameProblem(path, name string) error {
	if problem := packdecl.SelectionNameProblem(name); problem != "" {
		return fmt.Errorf("%s %q %s", path, name, problem)
	}
	if problem := providerNameProblem(name); problem != "" {
		return fmt.Errorf("%s %q: %s", path, name, problem)
	}
	if name == NativeProvider {
		return fmt.Errorf("%s %q is reserved; it means an agent is left to its own login", path, name)
	}
	return nil
}

// replacedRefusal is the refusal of a selection naming old, which a `name` at path replaced with
// now (RC-D68), in the owner's words: `openrouter is named "or" in your config
// (providers.openrouter.name): -p or`. A provider's `name` is always the user's (a pack may not
// write one); a variant's may be a pack's, so its wording names no owner.
func replacedRefusal(old, now, path string, user bool) string {
	where := ""
	if user {
		where = " in your config"
	}
	return fmt.Sprintf("%s is named %q%s (%s): -p %s", old, now, where, path, now)
}
