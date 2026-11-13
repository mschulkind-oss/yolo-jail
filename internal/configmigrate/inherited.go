package configmigrate

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// LowerInheritedUserScope is the lowering an inherited user scope crosses into a jail with when
// it mixes a value a TOML file wrote with a legacy value the jail's TOML reader cannot take as
// it is (config.RenderInheritedDelivery, LC-D4 in docs/reference/config/rulings.md): the very
// conversion `yolo config migrate` writes for a user file (lower), over the scope's merged read,
// applied to the values a legacy file wrote and to no other (LC-D5). It never declines; what it
// leaves out it returns, for the launch to disclose once.
//
//   - A `packs` entry a TOML file wrote crosses exactly as written, in its place: a handle such as
//     `sample-packs/house-rules` is a source-qualified name, never a relative path. A legacy entry
//     is respelled in place by the respell the migration's proof uses (RC-D58), so a mixed list
//     keeps its order; one with no TOML spelling is left out and disclosed.
//   - A legacy `loopholes` block crosses as its readable reading, which is nothing (LC-D6):
//     selection is `packs`, which crosses; settings and the user's ceiling are `allow`, and a
//     project's asks are `requests`, which a jail inherits into neither file. This jail's loophole
//     daemons are the host's, started from the host's gate, which reads the block through the
//     legacy projection, so the host-side meaning is untouched. The block is left out silently,
//     except for the two shapes that would change a launch inside the jail: an inline declaration
//     (`loopholes.<name>.command`), and a switch whose removal changes whether a selected pack's
//     loophole runs there (an `enabled: false` beside the selection). Each is disclosed.
//   - Every other value goes through lower; what it reports as not converted is left out and
//     disclosed (the launch also leaves out, and discloses, anything the readable schema still
//     refuses), and the provider table it resolves is proved the same as the host's.
func LowerInheritedUserScope(values *jsonx.OrderedMap, tomlWritten func(path string) bool) (*jsonx.OrderedMap, []config.InheritOmission) {
	values = jsonx.DeepCopyMap(values)
	if tomlWritten == nil {
		tomlWritten = func(string) bool { return false }
	}
	view := newUserView(values)
	// The jail reads the scope as a TOML user config, where a selected pack's loophole runs on
	// its selection (layerViewOf's switch); on a host whose user config is TOML too, so it does
	// there.
	readable := config.UserConfigIsTOML(paths.UserConfigPath())
	view.selectionSwitch = readable
	packs := packload.Embedded()
	var omissions []config.InheritOmission

	omissions = append(omissions, dropLegacyLoopholes(values, packs, tomlWritten)...)

	// The packs list is respelled here, entry by entry, and kept out of lower, whose lowerPacks
	// reads every entry as a legacy one.
	list, hasPacks := values.Get("packs")
	if hasPacks {
		var packOmissions []config.InheritOmission
		list, packOmissions = respellLegacyPacks(values, list, view, tomlWritten)
		omissions = append(omissions, packOmissions...)
		values.Delete("packs")
	}
	l := lower(values, loweringContext{scope: ScopeUser, user: view}, "")
	if hasPacks {
		l.values.Set("packs", list)
	}
	for _, nc := range l.skipped {
		omissions = append(omissions, config.InheritOmission{Path: "config." + nc.Path,
			Reason: nc.Reason + "; the jail reads its user scope without it, and the host's own launch reads it as before"})
	}
	if hasPacks {
		values.Set("packs", list)
	}
	before, _ := providerTable(withMovedProviderKeys(values, l.values), packs)
	after, _ := providerTable(l.values, packs)
	if d := firstDifference(before, after); d != "" {
		omissions = append(omissions, config.InheritOmission{Path: "config.providers",
			Reason: fmt.Sprintf("%s would differ at %s in the TOML spelling of it, so a launch inside the jail may "+
				"resolve that provider differently from the host; `yolo config migrate` names what it cannot convert",
				providerTableLayer, d)})
	}
	return l.values, omissions
}

// dropLegacyLoopholes takes the legacy-written `loopholes` block out of values (LC-D6) and
// returns the disclosures for the shapes that change a launch inside the jail.
func dropLegacyLoopholes(values *jsonx.OrderedMap, packs []*packload.Pack, tomlWritten func(string) bool) []config.InheritOmission {
	table, ok := mapAt(values, "loopholes")
	if !ok {
		if _, present := values.Get("loopholes"); present && !tomlWritten("loopholes") {
			values.Delete("loopholes")
		}
		return nil
	}
	switched := switchedLoopholes(newUserView(values).packs, packs)
	var out []config.InheritOmission
	for _, name := range append([]string(nil), table.Keys()...) {
		if tomlWritten("loopholes." + name) {
			continue
		}
		if entry, ok := mapAt(table, name); ok {
			_, declared := entry.Get("command")
			off, _ := entry.Get("enabled")
			switch {
			case declared:
				out = append(out, config.InheritOmission{Path: "config.loopholes." + name,
					Reason: "an inline loophole declaration has no spelling in the TOML user scope the jail reads, so a " +
						"launch inside the jail does not declare it; the host's own launch runs it as before, and " +
						"`yolo pack init` makes it a pack, which crosses"})
			case off == false && switched[name]:
				out = append(out, config.InheritOmission{Path: "config.loopholes." + name + ".enabled",
					Reason: "`enabled: false` beside the pack's selection has no spelling in the TOML user scope the jail " +
						"reads, so a launch inside the jail runs that loophole; the host's own launch keeps it off as before"})
			}
		}
		table.Delete(name)
	}
	if table.Len() == 0 {
		values.Delete("loopholes")
	}
	return out
}

// switchedLoopholes is every loophole whose running a switch decides, of the packs selected
// names and the packs they need: a loophole that declares no grants, whose access is its running
// (a grant-declaring loophole's access is its grants, which a jail's own gate gives).
func switchedLoopholes(selected map[string]bool, packs []*packload.Pack) map[string]bool {
	byName := map[string]*packload.Pack{}
	for _, p := range packs {
		byName[p.Name] = p
	}
	chosen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		p := byName[name]
		if p == nil || p.Decl == nil || chosen[name] {
			return
		}
		chosen[name] = true
		for _, need := range p.Decl.DeclaredNeeds() {
			visit(need.Pack)
		}
	}
	for name := range selected {
		visit(name)
	}
	out := map[string]bool{}
	for name := range chosen {
		modules, _, _, err := packload.ReadableLoopholeModules(byName[name])
		if err != nil {
			continue
		}
		for _, m := range modules {
			if m.Decl == nil || len(m.Decl.Grants) == 0 {
				out[m.Name] = true
			}
		}
	}
	return out
}

// respellLegacyPacks is values' `packs` list with each legacy-written entry in a spelling only a
// legacy file writes (a path, a file:// or git+ address, a `{ source }` object) respelled in
// place as the migration converts it (normalizeLegacyPacks's respell, RC-D58), writing the
// `pack_sources` entry it needs into values when none of that name is there. A TOML-written
// entry and a bare name cross as written. A legacy entry with no TOML spelling is left out.
func respellLegacyPacks(values *jsonx.OrderedMap, raw any, view *userView, tomlWritten func(string) bool) (any, []config.InheritOmission) {
	list, ok := raw.([]any)
	if !ok {
		return raw, nil
	}
	names := view.sources
	if names == nil {
		names = sourceNames(list)
	}
	var omissions []config.InheritOmission
	out := make([]any, 0, len(list))
	for i, entry := range list {
		if tomlWritten(fmt.Sprintf("packs[%d]", i)) || !legacyPackEntry(entry) {
			out = append(out, entry)
			continue
		}
		c := classifyPack(entry)
		if c.reason != "" {
			omissions = append(omissions, config.InheritOmission{Path: fmt.Sprintf("config.packs[%d]", i),
				Reason: c.reason + "; a launch inside the jail does not select it, and the host's own launch does as before"})
			continue
		}
		if c.source != nil {
			name := names[c.source.key]
			if name == "" {
				name = sourceNames([]any{entry})[c.source.key]
			}
			sources, ok := mapAt(values, "pack_sources")
			if !ok {
				sources = jsonx.NewOrderedMap()
				values.Set("pack_sources", sources)
			}
			if _, has := sources.Get(name); !has {
				sources.Set(name, c.source.value)
			}
			c.handle = name + "/" + c.handle.(string)
		}
		out = append(out, c.handle)
	}
	return out, omissions
}

// firstDifference names the first path at which a and b differ, or "" when they are the same.
func firstDifference(a, b *jsonx.OrderedMap) string {
	diffs := diffValues("", a, b)
	if len(diffs) == 0 {
		return ""
	}
	return strings.TrimPrefix(diffs[0].Path, ".")
}
