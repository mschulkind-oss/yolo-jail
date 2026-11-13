package configmigrate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// userView is what converting one user-scope file needs to know about the whole merged old user
// config, because the facts a conversion rests on can sit in another file: the packs a loophole's
// settings need (config.jsonc), the profiles a selection names (work.jsonc), the provider a
// profile picks. It is built once from what the readers made of the old user files.
type userView struct {
	packs map[string]bool
	// needed is the shipped packs a listed pack's `needs` selects (neededPacks): a loophole of one
	// runs as a listed pack's does.
	needed   map[string]bool
	profiles *jsonx.OrderedMap
	// enabled is each loophole's merged `enabled` value, when one file wrote it.
	enabled map[string]any
	// selectionSwitch is set for a --user-layer over a TOML user config: there a selected pack's
	// loophole runs and reads its settings whatever `enabled` says (loopholes.ConfigBlock), so
	// a layer's `loopholes.<name>.settings` are live, never dormant.
	selectionSwitch bool
	// sources names each pack source the merged `packs` list needs (sourceNames), so every file
	// that names one directory writes the same source.
	sources map[string]string
	// grants is the loophole rows' projection (loopholegrants.go); nil until planLoopholeGrants.
	grants *loopholeGrantView
	// providers is the merged `providers` entries a TOML file wrote (config.TOMLWrittenProviders),
	// whose variants a legacy `profile` name may select (RC-D57).
	providers *jsonx.OrderedMap
	// removedSetupKeys are the merged old user config's values of the keys named setups replace
	// (lowerRemovedSetupKey): the merged read converts only these, as a project's never convert.
	removedSetupKeys map[string]any
	// setupFile is the user file that already writes `setup` (Graph.UserSetupFile), "" for none:
	// a removed key naming a setup is then left for its unset, never converted.
	setupFile string
	// packProfiles is every profile name a selected pack declares, read on first use (packProfile);
	// packsKnown is false when the selected packs did not all resolve.
	packProfiles map[string]bool
	packsKnown   bool
	// loopholePacks is the shipped pack that ships each loophole, by loophole name
	// (shippedLoopholePacks): `github-broker` is the github pack's, not a pack of its own.
	loopholePacks map[string]string
	// configured is each loophole of a configured (non-shipped) pack the user selects, by
	// loophole name (addConfiguredPacks).
	configured map[string]configuredLoophole
	// renamedPacks is, by its legacy name, the name the converted user config gives each
	// configured pack whose name changes (addConfiguredPacks, renamed): a TOML workspace written
	// under the legacy user config names it the old way in `[requests.<pack>]`, which the plan
	// respells (withRenamedPackRequests).
	renamedPacks map[string]string
}

// configuredLoophole is what the lowering needs of a configured pack's loophole: its pack, its
// declared settings, and whether it runs whenever its pack is selected (a grantless loophole;
// a legacy manifest's `default_enabled: false` reads as a grant, so it is not one, LC-D10).
type configuredLoophole struct {
	pack      string
	settings  []loopholedecl.Setting
	grantless bool
}

// addConfiguredPacks returns shipped with each configured pack the user selects added; it records
// each one's name as selected and each of its loopholes in v.configured. configured is
// config.UserScopeSelectedPacks, which resolves the shipped packs a bare name selects too: a pack
// of a shipped pack's name is read as the shipped one, as every other loophole row here reads it.
//
// renamed is whether the read is of the converted user config: a pack then takes the name the
// converted `packs` list gives it (convertedPackNames); a legacy user config, kept or read as the
// old side, names it as before.
func (v *userView) addConfiguredPacks(shipped, configured []*packload.Pack, renamed bool) []*packload.Pack {
	shippedNames := map[string]bool{}
	for _, p := range shipped {
		if p != nil {
			shippedNames[p.Name] = true
		}
	}
	var names map[string]string
	if renamed {
		names = convertedPackNames()
	}
	var own []*packload.Pack
	for _, p := range configured {
		if p == nil || p.Decl == nil || shippedNames[p.Name] {
			continue
		}
		if to := names[p.Name]; to != "" && to != p.Name {
			// The converted `packs` list names the pack by where it comes from (lowerPacks), and
			// its `[allow.<pack>]` and `[requests.<pack>]` tables are keyed by that name: both
			// sides of the proof read it so, as the read-back after Apply does.
			if v.renamedPacks == nil {
				v.renamedPacks = map[string]string{}
			}
			v.renamedPacks[p.Name] = to
			cp := *p
			cp.Name = to
			p = &cp
		}
		own = append(own, p)
	}
	if len(own) == 0 {
		return shipped
	}
	for _, p := range own {
		v.packs[p.Name] = true
		modules, _, _, err := packload.ReadableLoopholeModules(p)
		if err != nil {
			continue
		}
		for _, m := range modules {
			if m.Decl == nil {
				continue
			}
			if v.configured == nil {
				v.configured = map[string]configuredLoophole{}
			}
			v.configured[m.Name] = configuredLoophole{pack: p.Name,
				settings: append([]loopholedecl.Setting(nil), m.Decl.Settings...), grantless: len(m.Decl.Grants) == 0}
		}
	}
	return append(append([]*packload.Pack(nil), shipped...), own...)
}

// convertedPackNames is, by the name a legacy `packs` entry gives its pack, the name the
// converted list gives it: `<source>/<directory>` for a directory, as lowerPacks writes it, with
// the source named by sourceNames over the same list. An entry already in its TOML spelling (a
// handle, which holds a `/`) keeps its name, so a read of the converted files renames nothing.
func convertedPackNames() map[string]string {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil
	}
	var items []any
	for _, e := range entries {
		if !e.IsEmbedded && !strings.Contains(e.Name, "/") && strings.HasPrefix(e.Source, "file://") {
			items = append(items, e.Source)
		}
	}
	if len(items) == 0 {
		return nil
	}
	names := sourceNames(items)
	out := map[string]string{}
	for _, e := range entries {
		if e.IsEmbedded || strings.Contains(e.Name, "/") || !strings.HasPrefix(e.Source, "file://") {
			continue
		}
		c := classifyPack(e.Source)
		switch h := c.handle.(type) {
		case string:
			if c.source != nil && names[c.source.key] != "" {
				out[e.Name] = names[c.source.key] + "/" + h
			}
		case *jsonx.OrderedMap:
			if handle, _ := h.Get("pack"); handle != nil {
				out[e.Name], _ = handle.(string)
			}
		}
	}
	return out
}

// loopholePack is the pack that ships the loophole name: the shipped pack whose manifest
// contributes it, else, for a loophole no shipped pack ships, the name itself.
func (v *userView) loopholePack(name string) string {
	if pack, offSwitch := loopholedecl.OffSwitchPack(name); offSwitch {
		return pack
	}
	if pack := v.loopholePacks[name]; pack != "" {
		return pack
	}
	return name
}

func newUserView(user *jsonx.OrderedMap) *userView {
	v := &userView{packs: map[string]bool{}, profiles: jsonx.NewOrderedMap(), enabled: map[string]any{}}
	if user == nil {
		return v
	}
	for _, key := range config.RemovedSetupKeys {
		if value, ok := user.Get(key); ok {
			if v.removedSetupKeys == nil {
				v.removedSetupKeys = map[string]any{}
			}
			v.removedSetupKeys[key] = value
		}
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
	v.providers = config.TOMLWrittenProviders(user)
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
	if g.layer != nil {
		return layerViewOf(g)
	}
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

// layerViewOf is userViewOf for a Request.Layer migration: the user config with the old layer
// merged over it, as a launch passing the layer reads them. The layer writes every pack source
// its converted packs need itself, so it reads the same over any user config; a source the user
// config already writes with the same value keeps the user config's name, and no source takes a
// name the user config gives another one (a layer's table would replace it).
func layerViewOf(g *Graph) *userView {
	read, err := config.ReadMigrationLayer(g.Request.baseUserTop(), g.layer.Logical)
	if err != nil || read == nil {
		return newUserView(nil)
	}
	v := newUserView(read.User)
	v.selectionSwitch = config.UserConfigIsTOML(g.Request.baseUserTop())
	existing, _ := mapAt(read.User, "pack_sources")
	list, _ := read.User.Get("packs")
	items, _ := list.([]any)
	v.sources = reconcileSourceNames(v.sources, items, existing)
	return v
}

// reconcileSourceNames gives each source the packs list needs a name that agrees with the
// `pack_sources` already written: the existing key whose value is the very table the conversion
// would write, else a name no existing key holds.
func reconcileSourceNames(names map[string]string, list []any, existing *jsonx.OrderedMap) map[string]string {
	if existing == nil || existing.Len() == 0 {
		return names
	}
	out := map[string]string{}
	taken := map[string]bool{"local": true, "yolo": true}
	for _, k := range existing.Keys() {
		taken[k] = true
	}
	for _, entry := range list {
		c := classifyPack(entry)
		if c.source == nil || out[c.source.key] != "" {
			continue
		}
		want, _ := jsonx.DumpsSnapshot(c.source.value)
		for _, k := range existing.Keys() {
			have, _ := existing.Get(k)
			if text, _ := jsonx.DumpsSnapshot(have); text == want {
				out[c.source.key] = k
				break
			}
		}
		if out[c.source.key] != "" {
			continue
		}
		base := names[c.source.key]
		if base == "" {
			base = "packs"
		}
		name := base
		for i := 2; taken[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		taken[name] = true
		out[c.source.key] = name
	}
	return out
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
		enabled := view.enabled[name] == true || view.selectionSwitch
		// A configured pack's grantless loophole ran whenever its pack was selected, so the
		// settings written for it were live, not dormant (LC-D10: a legacy manifest's
		// `default_enabled: true` reads as nothing).
		if c, ok := view.configured[name]; ok && c.grantless && view.enabled[name] != false {
			enabled = true
		}
		// LC-D3: an off-switch loophole's `enabled: false` is honored, so it converts to its
		// pack's `off = true`, and the settings written beside it are configured-and-off, never
		// dormant: they convert too, so turning the loophole back on finds them.
		offPack, offSwitch := loopholedecl.OffSwitchPack(name)
		// The pack that ships the loophole, through the shipped manifests: the Claude broker is
		// claude-auth's, which the claude pack needs, and github-broker is the github pack's.
		pack := view.loopholePack(name)
		// A pack every launch selects with no `packs` line (cgroup-delegate) is selected too.
		selected := view.packs[name] || view.packs[pack] || view.needed[pack] || config.IsSelectedByDefault(pack)
		if offSwitch && view.enabled[name] == false {
			enabled = true
		}
		if d, declared := view.grants.decl(name); declared && selected {
			lowerGrantLoophole(d, entry, path, enabled, view.grants, place, skip)
			continue
		}
		for _, key := range entry.Keys() {
			v, _ := entry.Get(key)
			oldPath := append(append([]string(nil), path...), key)
			switch key {
			case "enabled":
				switch {
				case v == false && offSwitch && !selected:
					ctx.drop(oldPath, v, "no selected pack runs "+name+", so it is off without the key")
				case v == false && offSwitch:
					if !place([]string{"allow", offPack, loopholedecl.AllowOffKey}, true, oldPath) {
						skip(oldPath, v, "allow."+offPack+"."+loopholedecl.AllowOffKey+" is already written in this file")
					}
				case v == true && selected && userBrokerRead(name).ReadCapability != "":
					// LC-D2: a user switch on a selected broker is its read grant on every
					// repository, `[allow.<source>] always = ["<read capability>"]`.
					b := userBrokerRead(name)
					at := []string{"allow", b.Brokered.Source, "always"}
					if !place(at, []any{b.ReadCapability}, oldPath) {
						skip(oldPath, v, "allow."+b.Brokered.Source+".always is already written in this file, and this switch is its read grant; "+
							"remove the `\"enabled\": true` (the list already says what it gives), then run `yolo config migrate` again")
					}
				case v == true && selected:
					// A selected pack's loophole runs whenever its pack is selected
					// (docs/reference/loophole-grants.md §4.1); one that declares grants was
					// converted above, so this one is grantless or ungated: the list already says
					// it, and nothing is written (LC-D9: a selected grantless switch).
				case v == true:
					// LC-D9: a switch for a pack not in packs ran nothing in v0.15.1 either
					// (it warned and launched), so it converts to nothing, with the offer.
					ctx.drop(oldPath, v, "`packs` does not list "+pack+", so the switch runs nothing; to run it, add the pack with `yolo pack add "+pack+"`")
				default:
					skip(oldPath, v, "a user config cannot turn a listed pack's service off; remove \""+name+"\" from packs to stop it, then run `yolo config migrate` again")
				}
			case "settings":
				table := name
				if c, ok := view.configured[name]; ok {
					// A configured pack's table is keyed by its converted name (convertedPackNames).
					table = c.pack
				}
				lowerLoopholeSettings(table, v, oldPath, selected, enabled, place, skip)
			default:
				skip(oldPath, v, "only a loophole's `enabled` and `settings` convert; a loophole declared here (`"+key+"`) belongs in a pack: move it into one (`yolo pack init` starts one), then run `yolo config migrate` again")
			}
		}
	}
}

// lowerGrantLoophole converts a user file's block for a selected grant-declaring loophole (§6's
// user rows): `enabled` and a replaced setting (journal's `full`) become the pack's ceiling lines,
// placed in this file; `enabled: false` writes nothing, since absent grants nothing; any other
// setting becomes `allow.<pack>.<setting>` as before. A legacy `visible`-style list is a
// project's request now, which a user file cannot write.
func lowerGrantLoophole(d config.LoopholeGrantDeclaration, entry *jsonx.OrderedMap, path []string, enabled bool, gv *loopholeGrantView, place func([]string, any, []string) bool, skip func([]string, any, string)) {
	owns := false
	for _, key := range entry.Keys() {
		v, _ := entry.Get(key)
		oldPath := append(append([]string(nil), path...), key)
		switch key {
		case "enabled":
			owns = true
		case "settings":
			settings, ok := v.(*jsonx.OrderedMap)
			if !ok {
				skip(oldPath, v, "settings must be an object")
				continue
			}
			rest := jsonx.NewOrderedMap()
			for _, sk := range settings.Keys() {
				sv, _ := settings.Get(sk)
				setting, values := legacyGrantKey(d, sk)
				switch {
				case setting:
					owns = true
				case values && enabled && len(gv.userRequests[d.Pack]) > 0:
					// The values are the user-sourced request's, written below (LC-D16).
				case values:
					skip(append(append([]string(nil), oldPath...), sk), sv, "this list is a project's request now: run `yolo request "+d.Pack+" <kind> <values>` in each workspace that needs it, then remove it from the old file and run `yolo config migrate` again")
				default:
					rest.Set(sk, sv)
				}
			}
			if rest.Len() > 0 {
				lowerLoopholeSettings(d.Pack, rest, oldPath, true, enabled, place, skip)
			}
		default:
			skip(oldPath, v, "only a loophole's `enabled` and `settings` convert; a loophole declared here (`"+key+"`) belongs in a pack: move it into one (`yolo pack init` starts one), then run `yolo config migrate` again")
		}
	}
	if owns {
		placeLoopholeCeiling(gv, d, place, path)
		// LC-D16: a user switch on a valued kind asked every project for its values, which
		// `[allow.<pack>] <kind> = [...]` says beside the `on_request` line that admits it.
		for _, g := range d.Grants {
			if values := gv.userRequests[d.Pack][g.Kind]; g.Valued && len(values) > 0 {
				place([]string{"allow", d.Pack, g.Kind}, anyList(values), path)
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
		return profilePlacement{}, "the profile " + name + " is not declared in this user config", false
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
		if placement.variant != "" {
			out.variants = append(out.variants, [2]string{placement.provider, placement.variant})
		}
	}
}

// lowerProfileSelection converts the agent-to-profile map to active_provider. `*` is `default`,
// a null is "native" (left to the agent's own login, docs/reference/config/user-config.md#which-provider-each-agent-runs-on), a name resolves through
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
			skip(oldPath, v, "an agent named `default` would collide with active_provider.default; rename it in the old file, then run `yolo config migrate` again")
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

// selectionOf is what active_provider names for one name in a legacy `profile` selection. A
// profile the old user files declare resolves through placeProfile, since the conversion moves
// it onto its provider. Any other name (a profile a selected pack declares, such as claude-auth's
// `claude` or a local pack's, or a provider's own name) is written as it is: the launch reads
// `profile` and `active_provider` through one fold (config.ConfigActiveSelection) and resolves
// either's names against one declared set, so the same name selects the same thing, and a name
// that set does not hold is refused alike, by the launch, before and after. Only a name holding
// '/' cannot be carried, since active_provider reads it as provider/variant.
func selectionOf(view *userView, name string) (string, string) {
	if _, declared := view.profiles.Get(name); declared {
		p, reason, ok := placeProfile(view, name)
		if !ok {
			return "", reason
		}
		return p.selection, ""
	}
	if strings.Contains(name, "/") {
		return "", "the profile name " + name + " holds '/', which active_provider reads as provider/variant; " +
			"rename the profile in the pack or file that declares it, then run `yolo config migrate` again"
	}
	// A bare variant name a TOML file declares (a piecemeal mix, RC-D57) is the launch's
	// `<provider>/<variant>` unless a selected pack's profile holds the name, as the launch reads it
	// (config.SelectableProfiles).
	if variant, ok := config.LegacyProfileVariant(view.providers, name); ok {
		held, known := view.packProfile(name)
		switch {
		case !known:
			return "", "the profile name " + name + " is a variant in your TOML providers table, and the selected packs " +
				"could not be read to tell whether one of them declares a profile of that name; run `yolo check`, " +
				"then `yolo config migrate` again"
		case !held:
			return variant, ""
		}
	}
	return name, ""
}

// packProfile reports whether a pack the merged old user config selects declares a profile named
// name; known is false when those packs could not all be read.
func (v *userView) packProfile(name string) (held, known bool) {
	if v.packProfiles == nil {
		v.packProfiles = map[string]bool{}
		packs, complete := config.UserScopeSelectedPacks()
		v.packsKnown = complete
		for _, p := range packload.Embedded() {
			if v.packs[p.Name] {
				packs = append(packs, p)
			}
		}
		for _, declared := range packload.DeclaredProfileNames(packs, nil) {
			v.packProfiles[declared] = true
		}
	}
	if v.packProfiles[name] {
		return true, true
	}
	return false, v.packsKnown
}

func selectionValue(view *userView, v any) (any, string) {
	switch t := v.(type) {
	case nil:
		return "native", ""
	case string:
		selection, reason := selectionOf(view, t)
		if reason != "" {
			return nil, reason
		}
		return selection, ""
	case []any:
		out := make([]any, 0, len(t))
		var reasons []string
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				reasons = append(reasons, "a profile list holds profile names")
				continue
			}
			selection, reason := selectionOf(view, s)
			if reason != "" {
				reasons = append(reasons, reason)
				continue
			}
			out = append(out, selection)
		}
		if len(reasons) > 0 {
			sort.Strings(reasons)
			return nil, strings.Join(dedupe(reasons), "; ")
		}
		return out, ""
	}
	return nil, "a profile selection is a profile name, a list of them, or null"
}

// neededPacks is the shipped packs the listed ones' `needs` add (packload.ResolveNeeds), by name.
// userBrokerRead is the shipped broker declaration of the loophole name, a zero one when no
// shipped pack declares it a broker.
func userBrokerRead(name string) config.ReadableBrokerDeclaration {
	declarations, err := shippedBrokers()
	if err != nil {
		return config.ReadableBrokerDeclaration{}
	}
	for _, d := range declarations {
		if d.Label == name {
			return d
		}
	}
	return config.ReadableBrokerDeclaration{}
}

// shippedLoopholePacks is, by loophole name, the shipped pack whose manifest contributes it.
func shippedLoopholePacks(shipped []*packload.Pack) map[string]string {
	out := map[string]string{}
	for _, p := range shipped {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindLoophole || c.From == "" {
				continue
			}
			if name := filepath.Base(filepath.FromSlash(c.From)); out[name] == "" {
				out[name] = p.Name
			}
		}
	}
	return out
}

func neededPacks(listed map[string]bool, shipped []*packload.Pack) map[string]bool {
	out := map[string]bool{}
	byName := map[string]*packload.Pack{}
	for _, p := range shipped {
		if p != nil && p.Decl != nil {
			byName[p.Name] = p
		}
	}
	var selected []*packload.Pack
	for name := range listed {
		if p := byName[name]; p != nil {
			selected = append(selected, p)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	added, _, err := packload.ResolveNeeds(selected, func(name string) (*packload.Pack, bool) {
		p, ok := byName[name]
		return p, ok
	})
	if err != nil {
		return out
	}
	for _, p := range added {
		out[p.Name] = true
	}
	return out
}
