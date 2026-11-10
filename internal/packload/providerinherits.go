package packload

// providerinherits.go resolves a provider's `inherits` key while the providers table composes:
// the child starts from its parent's finished entry, then its own pack declaration, its `models`
// contributions and its user entry lay over it. The design, every ruling and the refusals' next
// steps are in docs/design/provider-inherits.md (PI-D1..PI-D9); this file is the mechanism.
//
// THE KEY NEVER LEAVES THIS FILE'S REACH: it is read off the pack layer and the user layer, and
// stripInherits deletes it from every composed entry, so no consumer of the table learns it.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// inheritsKey is the provider-entry key naming the parent.
const inheritsKey = "inherits"

// notInherited are the entry keys a child never takes from its parent (PI-D2): the credential,
// the selection names, the variants (their names are top-level selection names) and the key.
var notInherited = []string{"api_key_env_name", "name", "alias", "variants", inheritsKey}

// inheritsPlan is what resolveInherits found: the children, in table order, with their parents,
// and the entries of parents the user disabled, kept so their children can still inherit (PI-D7).
type inheritsPlan struct {
	children  map[string]bool
	parent    map[string]string
	order     []string
	snapshots map[string]*jsonx.OrderedMap
}

// effectiveInherits is a provider's parent name: the user's `inherits` when the user's entry is a
// map holding the key (a string; "" or null means none, PI-D6), else the pack's. A provider the
// user nulled has none.
func effectiveInherits(out, user *jsonx.OrderedMap, name string) string {
	if user != nil {
		if uv, has := user.Get(name); has {
			if uv == nil {
				return ""
			}
			if m, ok := uv.(*jsonx.OrderedMap); ok {
				if raw, ok := m.Get(inheritsKey); ok {
					s, _ := raw.(string)
					return s
				}
			}
		}
	}
	if e := providerEntry(out, name); e != nil {
		s, _ := e.Get(inheritsKey)
		if str, ok := s.(string); ok {
			return str
		}
	}
	return ""
}

// resolveInherits finds the children and refuses the shapes PI-D4 refuses, before anything is
// composed, so a refusal never leaves a half-built table.
func resolveInherits(out, user *jsonx.OrderedMap) (*inheritsPlan, error) {
	plan := &inheritsPlan{children: map[string]bool{}, parent: map[string]string{},
		snapshots: map[string]*jsonx.OrderedMap{}}
	names := append([]string(nil), out.Keys()...)
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if user != nil {
		for _, n := range user.Keys() {
			if !seen[n] {
				names = append(names, n)
			}
		}
	}
	for _, name := range names {
		if p := effectiveInherits(out, user, name); p != "" {
			plan.children[name] = true
			plan.parent[name] = p
			plan.order = append(plan.order, name)
		}
	}
	for _, child := range plan.order {
		p := plan.parent[child]
		switch {
		case p == child:
			return nil, selfInheritRefusal(child)
		case plan.children[p]:
			return nil, chainInheritRefusal(child, p, plan.parent[p] == child)
		case !providerDeclared(out, user, p):
			return nil, missingParentRefusal(child, p)
		}
	}
	return plan, nil
}

// providerDeclared reports whether the pack layer or the user's own entry gives the table a
// provider by that name. A pack's provider the user nulled still counts: it lends (PI-D7).
func providerDeclared(out, user *jsonx.OrderedMap, name string) bool {
	if providerEntry(out, name) != nil {
		return true
	}
	if user != nil {
		if uv, ok := user.Get(name); ok {
			_, isMap := uv.(*jsonx.OrderedMap)
			return isMap
		}
	}
	return false
}

// snapshot keeps a copy of name's composed entry when it is some child's parent and is about to
// be dropped by the user's null (PI-D7).
func (p *inheritsPlan) snapshot(out *jsonx.OrderedMap, name string) {
	if p == nil {
		return
	}
	for _, parent := range p.parent {
		if parent != name {
			continue
		}
		if e := providerEntry(out, name); e != nil {
			p.snapshots[name] = jsonx.DeepCopy(e).(*jsonx.OrderedMap)
		}
		return
	}
}

// compose builds one child's entry: the parent's finished entry minus what never carries over,
// the child's own pack declaration over it, its `models` contributions (adds, then onlys, PI-D3),
// and last its user entry through the same fold the user loop runs.
func (p *inheritsPlan) compose(child string, out, user *jsonx.OrderedMap, packs []*Pack, cfg composeOpts,
	pinned map[string]bool, shipper map[string]string, supplied map[string]bool) error {
	parent := p.parent[child]
	source := providerEntry(out, parent)
	if source == nil {
		source = p.snapshots[parent]
	}
	if source == nil {
		return missingParentRefusal(child, parent)
	}
	base := jsonx.DeepCopy(source).(*jsonx.OrderedMap)
	for _, key := range notInherited {
		base.Delete(key)
	}
	if own := providerEntry(out, child); own != nil {
		mergeUnder(base, jsonx.DeepCopy(own).(*jsonx.OrderedMap))
	}
	out.Set(child, base)
	if applyModelContributions(out, user, packs, cfg.modelNote, nil, map[string]bool{child: true})[child] {
		supplied[child] = true
	}
	if user != nil {
		if value, ok := user.Get(child); ok && value != nil {
			if err := mergeUserEntry(out, child, value, pinned, shipper); err != nil {
				return err
			}
		}
	}
	if supplied[parent] {
		supplied[child] = true
	}
	return nil
}

// stripInherits deletes the key from every composed entry (PI-D1).
func stripInherits(out *jsonx.OrderedMap) {
	for _, name := range out.Keys() {
		if e := providerEntry(out, name); e != nil {
			e.Delete(inheritsKey)
		}
	}
}

func optOutStep(child string) string {
	return fmt.Sprintf("yolo config set providers.%s.inherits '\"\"' --user", child)
}

func selfInheritRefusal(child string) error {
	return fmt.Errorf("providers.%s.inherits: refused — %q names itself as its parent. Name the provider "+
		"it should start from, or drop the inheritance: %s", child, child, optOutStep(child))
}

func chainInheritRefusal(child, parent string, cycle bool) error {
	what := fmt.Sprintf("%q is itself a child of another provider, and inheritance is one level only", parent)
	if cycle {
		what = fmt.Sprintf("%q and %q inherit from each other", child, parent)
	}
	return fmt.Errorf("providers.%s.inherits: refused — %s. Point %q at the root provider, or opt "+
		"%q out: %s", child, what, child, parent, optOutStep(parent))
}

func missingParentRefusal(child, parent string) error {
	return fmt.Errorf("providers.%s.inherits: refused — it inherits from provider %q, which no selected "+
		"pack ships and your config's `providers` does not declare. Select the pack that ships it "+
		"(`yolo pack add <pack>`), declare providers.%s in your config, or drop the inheritance: %s",
		child, parent, parent, optOutStep(child))
}
