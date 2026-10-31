package configmigrate

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// userView is what converting one user-scope file needs to know about the whole merged old user
// config, because the facts a conversion rests on can sit in another file: the packs a loophole's
// settings need (config.jsonc), the profiles a selection names (work.jsonc), the provider a
// profile picks. It is built once from what the readers made of the old user files.
type userView struct {
	packs    map[string]bool
	profiles *jsonx.OrderedMap
	// enabled is each loophole's merged `enabled` value, when one file wrote it.
	enabled map[string]any
	// sources names each pack source the merged `packs` list needs (sourceNames), so every file
	// that names one directory writes the same source.
	sources map[string]string
}

func newUserView(user *jsonx.OrderedMap) *userView {
	v := &userView{packs: map[string]bool{}, profiles: jsonx.NewOrderedMap(), enabled: map[string]any{}}
	if user == nil {
		return v
	}
	if list, ok := user.Get("packs"); ok {
		if items, ok := list.([]any); ok {
			v.sources = sourceNames(items)
			for _, item := range items {
				if name, ok := item.(string); ok {
					v.packs[name] = true
				}
			}
		}
	}
	if p, ok := user.Get("profiles"); ok {
		if m, ok := p.(*jsonx.OrderedMap); ok {
			v.profiles = m
		}
	}
	if l, ok := user.Get("loopholes"); ok {
		if m, ok := l.(*jsonx.OrderedMap); ok {
			for _, name := range m.Keys() {
				entry, _ := m.Get(name)
				if em, ok := entry.(*jsonx.OrderedMap); ok {
					if e, has := em.Get("enabled"); has {
						v.enabled[name] = e
					}
				}
			}
		}
	}
	return v
}

// userViewOf reads the old user files through the launch's readers. A read that fails leaves the
// view empty, so every conversion that rests on it refuses and names its next step.
func userViewOf(g *Graph) *userView {
	top := oldTop(g)
	if top == "" {
		return newUserView(nil)
	}
	read, err := config.ReadMigrationGraph(top, "")
	if err != nil || read == nil {
		return newUserView(nil)
	}
	return newUserView(read.User)
}

// userScoped reports whether the lowering may convert the user-only vocabulary (loopholes,
// profile, profiles): a user file, or the merged read the equality check lowers the same way.
func (c loweringContext) userScoped() bool { return c.scope == ScopeUser || c.merged }

func (c loweringContext) view() *userView {
	if c.user != nil {
		return c.user
	}
	return newUserView(nil)
}

// lowerLoopholes converts a user file's loopholes table (RC-D31's companion decision, RC-D32):
// `packs` is the only switch, so `enabled: true` on a listed pack says nothing the list does not,
// and `settings` become the `allow.<pack>` table. Anything the new spelling cannot say stays at
// its old spelling with the next step named.
func lowerLoopholes(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	table, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip([]string{"loopholes"}, value, "loopholes must be an object")
		return
	}
	view := ctx.view()
	for _, name := range table.Keys() {
		entryValue, _ := table.Get(name)
		path := []string{"loopholes", name}
		entry, ok := entryValue.(*jsonx.OrderedMap)
		if !ok {
			skip(path, entryValue, "a loophole's entry must be an object")
			continue
		}
		selected := view.packs[name]
		enabled := view.enabled[name] == true
		for _, key := range entry.Keys() {
			v, _ := entry.Get(key)
			oldPath := append(append([]string(nil), path...), key)
			switch key {
			case "enabled":
				switch {
				case v == true && selected:
					// The list already says it; nothing is written.
				case v == true:
					skip(oldPath, v, "`packs` is the only switch and does not list "+name+"; add it with `yolo pack add "+name+"` (or put \""+name+"\" in packs), then run `yolo config migrate` again")
				default:
					skip(oldPath, v, "a user config cannot turn a listed pack's service off; remove \""+name+"\" from packs to stop it, or keep the old file for now")
				}
			case "settings":
				lowerLoopholeSettings(name, v, oldPath, selected, enabled, place, skip)
			default:
				skip(oldPath, v, "only a loophole's `enabled` and `settings` convert; a loophole declared here (`"+key+"`) belongs in a pack: `yolo pack init`, or keep the old file for now")
			}
		}
	}
}

func lowerLoopholeSettings(name string, value any, oldPath []string, selected, enabled bool, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	settings, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip(oldPath, value, "settings must be an object")
		return
	}
	switch {
	case !selected:
		skip(oldPath, value, "[allow."+name+"] is valid only when "+name+" is in packs; add it with `yolo pack add "+name+"`, then run `yolo config migrate` again")
		return
	case !enabled:
		skip(oldPath, value, "the old file does not say `enabled: true` for "+name+", so its settings are dormant; the new spelling would switch the service on. Set \"enabled\": true in the old file, or delete the settings, then run `yolo config migrate` again")
		return
	}
	for _, key := range settings.Keys() {
		v, _ := settings.Get(key)
		at := append(append([]string(nil), oldPath...), key)
		if !place([]string{"allow", name, key}, v, at) {
			skip(at, v, "allow."+name+"."+key+" is already written in this file")
		}
	}
}

// profilePlacement is where one old profile lands: on its provider (when the profile only repeats
// the provider's name) or as a variant of it.
type profilePlacement struct {
	provider  string
	variant   string // "" when the profile is the provider itself
	selection string // what active_provider names
}

// placeProfile classifies the profile name against the merged old profiles. ok is false, with
// the reason, when no lossless placement exists.
func placeProfile(view *userView, name string) (profilePlacement, string, bool) {
	raw, found := view.profiles.Get(name)
	if !found {
		return profilePlacement{}, "the profile " + name + " is not declared in this user config (a pack declares it); name its provider by hand, as active_provider.<agent> = \"<provider>\" (`yolo check` lists the providers), or keep the old file for now", false
	}
	profile, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return profilePlacement{}, "profiles." + name + " must be an object", false
	}
	pv, _ := profile.Get("provider")
	provider, ok := pv.(string)
	if !ok || provider == "" {
		return profilePlacement{}, "profiles." + name + " names no provider, so it has no provider to start from; add \"provider\" in the old file, or keep it", false
	}
	if name == provider {
		for _, key := range profile.Keys() {
			if key != "provider" && key != "model" && key != "via" {
				return profilePlacement{}, "profiles." + name + " repeats its provider's name but carries `" + key + "`, which only a variant can hold, and a variant may not share its provider's name; rename the profile (for example " + name + "-2) in the old file, then run `yolo config migrate` again", false
			}
		}
		return profilePlacement{provider: provider, selection: provider}, "", true
	}
	return profilePlacement{provider: provider, variant: name, selection: provider + "/" + name}, "", true
}

// lowerProfiles writes each profile onto its provider or as a variant. It runs after the file's
// other keys so it merges into a providers table the same file wrote.
func lowerProfiles(value any, ctx loweringContext, out *lowered, skip func([]string, any, string)) {
	table, ok := value.(*jsonx.OrderedMap)
	if !ok {
		skip([]string{"profiles"}, value, "profiles must be an object")
		return
	}
	view := ctx.view()
	for _, name := range table.Keys() {
		raw, _ := table.Get(name)
		path := []string{"profiles", name}
		placement, reason, ok := placeProfile(view, name)
		if !ok {
			skip(path, raw, reason)
			continue
		}
		profile := raw.(*jsonx.OrderedMap)
		base := []string{"providers", placement.provider}
		if placement.variant != "" {
			base = append(base, "variants", placement.variant)
		}
		var moved [][2]any
		blocked := ""
		for _, key := range profile.Keys() {
			if key == "provider" {
				continue
			}
			v, _ := profile.Get(key)
			target := append(append([]string(nil), base...), key)
			if _, present := get(out.values, target); present {
				blocked = dotted(target) + " is already written in this file; keep one of the two"
				break
			}
			moved = append(moved, [2]any{target, v})
		}
		if blocked != "" {
			skip(path, raw, blocked)
			continue
		}
		for _, m := range moved {
			target := m[0].([]string)
			put(out.values, target, m[1])
			out.origins[strings.Join(target, "\x00")] = path
		}
	}
}

// lowerProfileSelection converts the agent-to-profile map to active_provider. `*` is `default`,
// a null is "native" (left to the agent's own login, config-user.md §5), a name resolves through
// placeProfile.
func lowerProfileSelection(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	view := ctx.view()
	table, ok := value.(*jsonx.OrderedMap)
	if !ok {
		table = jsonx.NewOrderedMap()
		table.Set("*", value)
	}
	for _, key := range table.Keys() {
		v, _ := table.Get(key)
		oldPath := []string{"profile", key}
		if key == "profile" && !ok {
			oldPath = []string{"profile"}
		}
		target := key
		if key == "*" {
			target = "default"
		} else if key == "default" {
			skip(oldPath, v, "an agent named `default` would collide with active_provider.default; rename it in the old file, or keep the old file for now")
			continue
		}
		converted, reason := selectionValue(view, v)
		if reason != "" {
			skip(oldPath, v, reason)
			continue
		}
		if !place([]string{"active_provider", target}, converted, oldPath) {
			skip(oldPath, v, "active_provider."+target+" is already written in this file")
		}
	}
}

func selectionValue(view *userView, v any) (any, string) {
	switch t := v.(type) {
	case nil:
		return "native", ""
	case string:
		p, reason, ok := placeProfile(view, t)
		if !ok {
			return nil, reason
		}
		return p.selection, ""
	case []any:
		out := make([]any, 0, len(t))
		var reasons []string
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				reasons = append(reasons, "a profile list holds profile names")
				continue
			}
			p, reason, ok := placeProfile(view, s)
			if !ok {
				reasons = append(reasons, reason)
				continue
			}
			out = append(out, p.selection)
		}
		if len(reasons) > 0 {
			sort.Strings(reasons)
			return nil, strings.Join(dedupe(reasons), "; ")
		}
		return out, ""
	}
	return nil, "a profile selection is a profile name, a list of them, or null"
}
