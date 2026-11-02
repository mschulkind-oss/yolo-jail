package config

import (
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// loopholegrantlegacy.go is the LEGACY PROJECTION (docs/design/loophole-access-approval-plan.md
// PD-9, the design's §6 table): during the JSONC window, a legacy `loopholes.<name>` block in the
// user or workspace config, and the per-workspace switch file yolo wrote for `yolo loopholes
// enable`, read as the ceiling, request and record lines `yolo config migrate` would write for a
// grant-declaring loophole. Nothing here writes a file. The gate (WithLoopholeGrants) and `yolo
// check` (validateLoopholeGrants) read through it, so no access a legacy file holds today is lost
// or gained silently (LG-D11); T12's migration rows call ProjectLegacyLoopholeGrants for the same
// lines.
//
// The rows, for the manifest's `legacy_*` keys (loopholedecl.Grant):
//
//   - user `enabled: true` → the `legacy_enable` kind joins `allow.<pack>.always`, or
//     `on_request` for a valued kind, whose value only a project can give;
//   - user `settings.<legacy_setting>: true` (journal's `full`) → that kind joins the same list;
//   - workspace `enabled: true` → `[requests.<pack>] <kind> = true`;
//   - switch file `enabled: true` → a grant the record holds (the host-side `yolo loopholes
//     enable` was the approval), with `on_request` for the kind in the ceiling where the
//     ceiling does not already admit it;
//   - `settings.<legacy_values>` (`visible`, `allowed_devices`) → a valued kind's request values,
//     the user's list and the workspace's together, as the old merge unioned them.
//
// A loophole the legacy files leave off in this workspace (the switch file, then the workspace
// file, then the user file decides, as the old merge did; absent is off) projects nothing here:
// it ran nowhere, so a grant would be access gained.

// LegacyLoopholeGrantInput is the raw user and workspace configs, the per-workspace file's
// switches that apply (loophole name → enabled), and the selected grant-declaring loopholes.
type LegacyLoopholeGrantInput struct {
	User, Workspace *jsonx.OrderedMap
	Switches        map[string]bool
	Declarations    []LoopholeGrantDeclaration
}

// LegacyLoopholeGrants is one grant-declaring loophole's projected lines.
type LegacyLoopholeGrants struct {
	Pack, Loophole string
	// Always and OnRequest are the kinds the user's ceiling gains.
	Always, OnRequest []string
	// Requests is this workspace's projected request per kind: nil values for an unvalued kind.
	Requests map[string][]string
	// Recorded is the grants the approval record holds by the switch file: `kind` or
	// `kind=value`, the record's own spelling.
	Recorded []string
}

func (l LegacyLoopholeGrants) empty() bool {
	return len(l.Always) == 0 && len(l.OnRequest) == 0 && len(l.Requests) == 0 && len(l.Recorded) == 0
}

// legacyLoopholeBlock is cfg's `loopholes.<name>` table, nil when absent or not a table.
func legacyLoopholeBlock(cfg *jsonx.OrderedMap, name string) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	block, ok := asMap(getOr(cfg, "loopholes", nil))
	if !ok {
		return nil
	}
	entry, ok := asMap(getOr(block, name, nil))
	if !ok {
		return nil
	}
	return entry
}

// legacyEnabled is a block's `enabled`, and whether it is a boolean.
func legacyEnabled(block *jsonx.OrderedMap) (on, set bool) {
	if block == nil {
		return false, false
	}
	on, set = getOr(block, "enabled", nil).(bool)
	return on, set
}

// legacySetting is a block's `settings.<key>`, nil when absent.
func legacySetting(block *jsonx.OrderedMap, key string) any {
	if block == nil || key == "" {
		return nil
	}
	settings, ok := asMap(getOr(block, "settings", nil))
	if !ok {
		return nil
	}
	return getOr(settings, key, nil)
}

// ProjectLegacyLoopholeGrants is the legacy projection, one entry per declaration the legacy
// inputs say anything about, in declaration order.
func ProjectLegacyLoopholeGrants(in LegacyLoopholeGrantInput) []LegacyLoopholeGrants {
	var out []LegacyLoopholeGrants
	for _, d := range in.Declarations {
		user := legacyLoopholeBlock(in.User, d.Loophole)
		ws := legacyLoopholeBlock(in.Workspace, d.Loophole)
		switchOn, switchSet := in.Switches[d.Loophole]
		if user == nil && ws == nil && !switchSet {
			continue
		}
		userOn, userSet := legacyEnabled(user)
		wsOn, wsSet := legacyEnabled(ws)
		effective := false
		switch {
		case switchSet:
			effective = switchOn
		case wsSet:
			effective = wsOn
		case userSet:
			effective = userOn
		}
		p := LegacyLoopholeGrants{Pack: d.Pack, Loophole: d.Loophole, Requests: map[string][]string{}}
		if !effective {
			continue
		}
		switched := switchSet && switchOn
		// The legacy_enable kind first: a legacy_setting kind is requested where it is.
		enableRequested := false
		for _, g := range d.Grants {
			if !g.LegacyEnable && g.LegacyValues == "" {
				continue
			}
			if g.LegacyEnable && userOn {
				if g.Valued {
					p.OnRequest = appendUnique(p.OnRequest, g.Kind)
				} else {
					p.Always = appendUnique(p.Always, g.Kind)
				}
			}
			var values []string
			if g.Valued {
				values = appendUnique(values, stringList(legacySetting(user, g.LegacyValues))...)
				values = appendUnique(values, stringList(legacySetting(ws, g.LegacyValues))...)
				if len(values) == 0 {
					continue // a valued kind with no value asks for nothing
				}
			} else if !(g.LegacyEnable && (wsOn && wsSet || switched)) {
				continue
			}
			p.Requests[g.Kind] = values
			enableRequested = enableRequested || g.LegacyEnable
			if switched {
				p.Recorded = append(p.Recorded, recordSpelling(g.Kind, values)...)
				if !g.RequestableByDefault && !slices.Contains(p.Always, g.Kind) {
					p.OnRequest = appendUnique(p.OnRequest, g.Kind)
				}
			}
		}
		// legacy_setting is user-scope, as the setting it replaces was: a workspace file's value
		// was refused, so only the user's is read.
		for _, g := range d.Grants {
			if g.LegacySetting == "" || legacySetting(user, g.LegacySetting) != true {
				continue
			}
			if userOn && !g.Valued {
				p.Always = appendUnique(p.Always, g.Kind)
			} else {
				p.OnRequest = appendUnique(p.OnRequest, g.Kind)
			}
			if !enableRequested {
				continue
			}
			p.Requests[g.Kind] = nil
			if switched {
				p.Recorded = append(p.Recorded, g.Kind)
			}
		}
		if !p.empty() {
			out = append(out, p)
		}
	}
	return out
}

func recordSpelling(kind string, values []string) []string {
	if len(values) == 0 {
		return []string{kind}
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, kind+"="+v)
	}
	return out
}

func appendUnique(list []string, items ...string) []string {
	for _, s := range items {
		if !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}

// withLegacyLoopholeGrants is input with the legacy projection laid over it: the ceiling's lists
// gain the projected kinds, the workspace's requests gain the projected requests a file did not
// already write, and the record-equivalent grants come back by pack. A readable `allow.<pack>`
// or `requests.<pack>.<kind>` the user or project wrote stands: the projection only adds, and an
// `allow.<pack> = "off"` takes nothing from it. Origins name the legacy key the line came from.
func withLegacyLoopholeGrants(input LoopholeGrantInput, userSrc, wsSrc *Sources, workspace string) (LoopholeGrantInput, map[string][]string) {
	switches := map[string]bool{}
	wf := ReadWorkspaceFile(workspace)
	if wf != nil && wf.Applies {
		for _, name := range wf.Loopholes.Keys() {
			on, _ := wf.Loopholes.Get(name)
			switches[name] = on == true
		}
	}
	proj := ProjectLegacyLoopholeGrants(LegacyLoopholeGrantInput{User: input.User, Workspace: input.Workspace,
		Switches: switches, Declarations: input.Declarations})
	if len(proj) == 0 {
		return input, nil
	}
	// A top-level `allow` or `requests` that is not a table is the resolver's error to report.
	for _, top := range []any{getOr(input.User, "allow", nil), getOr(input.Workspace, "requests", nil)} {
		if top != nil && mapOrNil(top) == nil {
			return input, nil
		}
	}
	decls := map[string]LoopholeGrantDeclaration{}
	for _, d := range input.Declarations {
		decls[d.Pack] = d
	}
	out := input
	out.User = shallowCopyMap(input.User)
	out.Workspace = shallowCopyMap(input.Workspace)
	out.UserOrigins = copyOrigins(input.UserOrigins)
	out.WorkspaceOrigins = copyOrigins(input.WorkspaceOrigins)
	allow := shallowCopyMap(mapOrNil(getOr(out.User, "allow", nil)))
	requests := shallowCopyMap(mapOrNil(getOr(out.Workspace, "requests", nil)))
	recorded := map[string][]string{}
	for _, p := range proj {
		d := decls[p.Pack]
		userFrom := scopeSourceFiles(userSrc, "loopholes", p.Loophole)
		wsFrom := scopeSourceFiles(wsSrc, "loopholes", p.Loophole)
		if len(wsFrom) == 0 && wf != nil {
			wsFrom = []string{wf.Path}
		}
		if len(wsFrom) == 0 {
			wsFrom = userFrom
		}
		if existing, has := allow.Get(p.Pack); !has || mapOrNil(existing) != nil {
			table := shallowCopyMap(mapOrNil(existing))
			addToList(table, string(LoopholeCeilingAlways), p.Always)
			onRequest := p.OnRequest
			// A recorded grant stands only where the ceiling admits its kind; where the user's
			// own on_request list leaves out a kind requestable by default, it is added.
			if _, wrote := table.Get(string(LoopholeCeilingOnRequest)); wrote {
				for _, g := range d.Grants {
					if g.RequestableByDefault && hasRecordedKind(p.Recorded, g.Kind) {
						onRequest = appendUnique(onRequest, g.Kind)
					}
				}
			}
			if len(onRequest) > 0 {
				if _, wrote := table.Get(string(LoopholeCeilingOnRequest)); !wrote {
					// Writing a list ends "requestable by default"; keep those kinds in it.
					var seed []string
					for _, g := range d.Grants {
						if g.RequestableByDefault && !g.Valued {
							seed = append(seed, g.Kind)
						}
					}
					addToList(table, string(LoopholeCeilingOnRequest), seed)
				}
				addToList(table, string(LoopholeCeilingOnRequest), onRequest)
			}
			allow.Set(p.Pack, table)
			for _, list := range []LoopholeCeiling{LoopholeCeilingAlways, LoopholeCeilingOnRequest} {
				key := "allow." + p.Pack + "." + string(list)
				if len(out.UserOrigins[key]) == 0 {
					out.UserOrigins[key] = cloneOrigins(userFrom)
				}
			}
		}
		// A table the project wrote "off", or wrote wrong, is left for the resolver to read.
		var packReq *jsonx.OrderedMap
		if existing, has := requests.Get(p.Pack); !has || mapOrNil(existing) != nil {
			packReq = shallowCopyMap(mapOrNil(existing))
		}
		if packReq != nil {
			for _, g := range d.Grants {
				values, asked := p.Requests[g.Kind]
				if !asked {
					continue
				}
				if _, wrote := packReq.Get(g.Kind); wrote {
					continue
				}
				if g.Valued {
					list := make([]any, len(values))
					for i, v := range values {
						list[i] = v
					}
					packReq.Set(g.Kind, list)
				} else {
					packReq.Set(g.Kind, true)
				}
				out.WorkspaceOrigins["requests."+p.Pack+"."+g.Kind] = cloneOrigins(wsFrom)
			}
			requests.Set(p.Pack, packReq)
		}
		if len(p.Recorded) > 0 {
			recorded[p.Pack] = append([]string(nil), p.Recorded...)
		}
	}
	out.User.Set("allow", allow)
	out.Workspace.Set("requests", requests)
	return out, recorded
}

func hasRecordedKind(recorded []string, kind string) bool {
	for _, r := range recorded {
		if r == kind || len(r) > len(kind) && r[:len(kind)+1] == kind+"=" {
			return true
		}
	}
	return false
}

// addToList appends items missing from table[key]'s string list, creating it.
func addToList(table *jsonx.OrderedMap, key string, items []string) {
	if len(items) == 0 {
		return
	}
	var list []any
	if raw, ok := getOr(table, key, nil).([]any); ok {
		list = append(list, raw...)
	}
	for _, item := range items {
		if !slices.Contains(list, any(item)) {
			list = append(list, item)
		}
	}
	table.Set(key, list)
}

func mapOrNil(v any) *jsonx.OrderedMap {
	m, _ := asMap(v)
	return m
}

// shallowCopyMap is m's keys and values in a new map (a new empty one for nil), so an overlay
// never writes into the config a caller holds.
func shallowCopyMap(m *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if m == nil {
		return out
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out.Set(k, v)
	}
	return out
}

func copyOrigins(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = cloneOrigins(v)
	}
	return out
}

// legacyRecordHolds is whether the record-equivalent grants hold grant.
func legacyRecordHolds(recorded []string, grant string) bool {
	return slices.Contains(recorded, grant)
}
