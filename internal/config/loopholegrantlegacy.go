package config

import (
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// loopholegrantlegacy.go is the LEGACY PROJECTION (docs/reference/loophole-grants.md
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
//   - workspace `enabled: true` → `[requests.<pack>] <kind> = true`, and the kind in this
//     launch's ceiling as `on_request`: the switch is the request and its admission (LG-D16),
//     so the gate's y grants it as the config-change gate's y switched it on;
//   - a user config already in TOML: its ceiling listing the switch's kind is the old user
//     switch, so a legacy workspace block's values still request under it (LC-D2);
//   - switch file `enabled: true` → a grant the record holds (the host-side `yolo loopholes
//     enable` was the approval), with `on_request` for the kind in the ceiling where the
//     ceiling does not already admit it;
//   - `settings.<legacy_values>` (`visible`, `allowed_devices`) → a valued kind's request values,
//     the user's list and the workspace's together, as the old merge unioned them; where
//     neither wrote one, the setting's old default, which the manifest declares as
//     `legacy_default_values` (serial's five device globs), since that is what the loophole
//     ran on.
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
		default:
			// A user config already in TOML spells the old user switch as its ceiling (the
			// migration's row: `enabled: true` → `[allow.<pack>]`), so a legacy workspace block
			// that only writes values (`visible`, `allowed_devices`) is on where that ceiling
			// admits the switch's kind, as it was on under the user's switch (LC-D2).
			effective = userCeilingAdmitsSwitch(in.User, d)
		}
		p := LegacyLoopholeGrants{Pack: d.Pack, Loophole: d.Loophole, Requests: map[string][]string{}}
		if !effective {
			continue
		}
		switched := switchSet && switchOn
		// A workspace `enabled: true` is the request AND its admission (LG-D16): the old switch
		// needed no ceiling, so its kind joins this launch's ceiling as on_request, and the gate's
		// y grants it as the config-change gate's y switched it on.
		wsSwitched := !switchSet && wsSet && wsOn
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
				wsValues := stringList(legacySetting(ws, g.LegacyValues))
				// A user config already in TOML spells its old list as the user-sourced request
				// (LC-D16), which a legacy workspace's list still unions with, as the old merge did.
				// The conversion spells an old user switch that wrote no list as the setting's
				// default, which the old merge never unioned: a workspace's own list replaced it.
				// So a TOML list that is exactly the default reads as the default here.
				userValues := userSourcedValues(in.User, d.Pack, g.Kind)
				if len(wsValues) == 0 || !sameStringSet(userValues, g.LegacyDefaultValues) {
					values = appendUnique(values, userValues...)
				}
				values = appendUnique(values, wsValues...)
				if len(values) == 0 {
					// No list written: the loophole ran on the setting's default, which the
					// manifest that retired it declares (serial's five device globs).
					values = appendUnique(values, g.LegacyDefaultValues...)
				}
				if len(values) == 0 {
					continue // a valued kind with no value asks for nothing
				}
			} else if !(g.LegacyEnable && (wsOn && wsSet || switched)) {
				continue
			}
			p.Requests[g.Kind] = values
			enableRequested = enableRequested || g.LegacyEnable
			if wsSwitched && g.LegacyEnable && !slices.Contains(p.Always, g.Kind) {
				p.OnRequest = appendUnique(p.OnRequest, g.Kind)
			}
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

// userCeilingAdmitsSwitch is whether the user's readable ceiling, `[allow.<pack>]`'s `always` or
// `on_request`, lists a kind the legacy switch maps to: the TOML spelling of a user
// `enabled: true`. A table written "off" admits nothing.
func userCeilingAdmitsSwitch(user *jsonx.OrderedMap, d LoopholeGrantDeclaration) bool {
	allow := mapOrNil(getOr(user, "allow", nil))
	if allow == nil {
		return false
	}
	table := mapOrNil(getOr(allow, d.Pack, nil))
	if table == nil {
		return false
	}
	for _, g := range d.Grants {
		if !g.LegacyEnable {
			continue
		}
		for _, list := range []LoopholeCeiling{LoopholeCeilingAlways, LoopholeCeilingOnRequest} {
			if slices.Contains(stringList(getOr(table, string(list), nil)), g.Kind) {
				return true
			}
		}
	}
	return false
}

// userSourcedValues is a user config's `[allow.<pack>] <kind> = [...]`, the user-sourced request
// of a valued kind (LC-D16); nil when the user writes none.
func userSourcedValues(user *jsonx.OrderedMap, pack, kind string) []string {
	if user == nil {
		return nil
	}
	allow := mapOrNil(getOr(user, "allow", nil))
	if allow == nil {
		return nil
	}
	table := mapOrNil(getOr(allow, pack, nil))
	if table == nil {
		return nil
	}
	return stringList(getOr(table, kind, nil))
}

// sameStringSet is whether a and b hold the same strings, order and repeats aside; false when
// either is empty.
func sameStringSet(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			return false
		}
	}
	return true
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
// The workspace's switch file and the approval record's carried grants (carriedLoopholeGrants)
// are read from disk; withLegacyLoopholeGrantsFrom takes them as arguments.
func withLegacyLoopholeGrants(input LoopholeGrantInput, userSrc, wsSrc *Sources, workspace string) (LoopholeGrantInput, map[string][]string, map[string][]string) {
	switches := map[string]bool{}
	wfPath := ""
	wf := ReadWorkspaceFile(workspace)
	if wf != nil {
		wfPath = wf.Path
	}
	if wf != nil && wf.Applies {
		for _, name := range wf.Loopholes.Keys() {
			on, _ := wf.Loopholes.Get(name)
			switches[name] = on == true
		}
	}
	rec, _, _ := readLoopholeGrantRecord(workspace)
	out, recorded, open := withLegacyLoopholeGrantsFrom(input, userSrc, wsSrc, switches, wfPath, carriedLoopholeGrants(rec))
	return out, withApprovedLegacyGrants(recorded, input, switches, approvedWorkspaceSnapshot(workspace)), open
}

// approvedWorkspaceSnapshot is the approval record's config part for workspace, decoded: the
// workspace config a human's y last approved. nil when there is none or it does not read.
func approvedWorkspaceSnapshot(workspace string) *jsonx.OrderedMap {
	text, exists, err := ReadApprovalRecord(workspace)
	if err != nil || !exists {
		return nil
	}
	parsed, err := jsonx.Decode([]byte(text))
	if err != nil {
		return nil
	}
	snapshot, _ := parsed.(*jsonx.OrderedMap)
	return snapshot
}

// withApprovedLegacyGrants is recorded with the grants an APPROVED legacy workspace switch holds
// (LG-D16, the §6 row for an approved snapshot): where the workspace config the record approved
// switches a grant-declaring loophole on, and the workspace still does, the human's y at that
// launch was the approval, as the config-change gate's y was the switch in v0.15.1. So the
// requests that switch projects, in the approved file and in the current one alike, read as
// recorded and the gate asks nothing about them; a value the file added since is still asked.
// It is the same carry `yolo config migrate` writes into the record (approvedEnables).
func withApprovedLegacyGrants(recorded map[string][]string, input LoopholeGrantInput, switches map[string]bool, snapshot *jsonx.OrderedMap) map[string][]string {
	if snapshot == nil {
		return recorded
	}
	var decls []LoopholeGrantDeclaration
	for _, d := range input.Declarations {
		if _, decided := switches[d.Loophole]; decided {
			continue // the switch file decides this loophole, as it outranks the workspace
		}
		approvedOn, approvedSet := legacyEnabled(legacyLoopholeBlock(snapshot, d.Loophole))
		nowOn, nowSet := legacyEnabled(legacyLoopholeBlock(input.Workspace, d.Loophole))
		if approvedOn && approvedSet && nowOn && nowSet {
			decls = append(decls, d)
		}
	}
	if len(decls) == 0 {
		return recorded
	}
	then := ProjectLegacyLoopholeGrants(LegacyLoopholeGrantInput{User: input.User, Workspace: snapshot, Declarations: decls})
	now := ProjectLegacyLoopholeGrants(LegacyLoopholeGrantInput{User: input.User, Workspace: input.Workspace, Declarations: decls})
	approved := map[string]map[string][]string{}
	for _, p := range then {
		approved[p.Pack] = p.Requests
	}
	out := map[string][]string{}
	for k, v := range recorded {
		out[k] = append([]string(nil), v...)
	}
	for _, p := range now {
		for _, d := range decls {
			if d.Pack != p.Pack {
				continue
			}
			for _, g := range d.Grants {
				if !g.LegacyEnable {
					continue
				}
				values, asked := p.Requests[g.Kind]
				was, approvedAsk := approved[p.Pack][g.Kind]
				if !asked || !approvedAsk {
					continue
				}
				var kept []string
				for _, v := range values {
					if slices.Contains(was, v) {
						kept = append(kept, v)
					}
				}
				if g.Valued && len(kept) == 0 {
					continue
				}
				out[p.Pack] = appendUnique(out[p.Pack], recordSpelling(g.Kind, kept)...)
			}
		}
	}
	return out
}

// withLegacyLoopholeGrantsFrom is withLegacyLoopholeGrants over the given switches (the switch
// file at wfPath, "" for none) and carried grants. Its third result is, per pack, the carried
// grants this project's files do not state: the ones laid over as requests.
//
// The user-sourced requests (withUserSourcedRequests) are laid last, so a project's own request,
// legacy or carried, wins over the user's default for it.
func withLegacyLoopholeGrantsFrom(input LoopholeGrantInput, userSrc, wsSrc *Sources, switches map[string]bool, wfPath string, carried map[string][]string) (LoopholeGrantInput, map[string][]string, map[string][]string) {
	out, recorded, open := withLegacyProjection(withLegacyUserAdmission(input), userSrc, wsSrc, switches, wfPath, carried)
	return withUserSourcedRequests(out), recorded, open
}

func withLegacyProjection(input LoopholeGrantInput, userSrc, wsSrc *Sources, switches map[string]bool, wfPath string, carried map[string][]string) (LoopholeGrantInput, map[string][]string, map[string][]string) {
	proj := ProjectLegacyLoopholeGrants(LegacyLoopholeGrantInput{User: input.User, Workspace: input.Workspace,
		Switches: switches, Declarations: input.Declarations})
	hasCarried := false
	for _, d := range input.Declarations {
		hasCarried = hasCarried || len(carried[d.Pack]) > 0
	}
	withdrawn := legacyWithdrawals(input.Workspace, switches, input.Declarations)
	if len(proj) == 0 && !hasCarried && len(withdrawn) == 0 {
		return input, nil, nil
	}
	// A top-level `allow` or `requests` that is not a table is the resolver's error to report.
	for _, top := range []any{getOr(input.User, "allow", nil), getOr(input.Workspace, "requests", nil)} {
		if top != nil && mapOrNil(top) == nil {
			return input, nil, nil
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
		if len(wsFrom) == 0 && wfPath != "" {
			wsFrom = []string{wfPath}
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
	open := layCarriedRequests(input.Declarations, requests, carried)
	// A legacy workspace's `enabled: false` (or its switch file's) is this project's withdrawal,
	// `requests.<pack> = "off"` (LC-D18): v0.15.1 ran the loophole nowhere in this workspace,
	// whatever the user's config said, so a user config converted since gives it nothing here,
	// neither an `always` line nor a user-sourced request.
	for _, pack := range withdrawn {
		if _, has := requests.Get(pack); !has {
			requests.Set(pack, "off")
		}
	}
	out.User.Set("allow", allow)
	out.Workspace.Set("requests", requests)
	return out, recorded, open
}

// legacyWithdrawals is the packs whose loophole this workspace's legacy files switch off: the
// switch file's `false`, or, where the switch file does not decide it, the workspace config's
// `loopholes.<name>.enabled: false`. The switch file outranks the workspace file, as in
// ProjectLegacyLoopholeGrants.
func legacyWithdrawals(ws *jsonx.OrderedMap, switches map[string]bool, decls []LoopholeGrantDeclaration) []string {
	var out []string
	for _, d := range decls {
		on, set := switches[d.Loophole]
		if !set {
			on, set = legacyEnabled(legacyLoopholeBlock(ws, d.Loophole))
		}
		if set && !on {
			out = append(out, d.Pack)
		}
	}
	return out
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

// withLegacyUserAdmission is input with a legacy JSONC user config's ceiling as v0.15.1 held it
// (docs/design/legacy-config-compat.md LC-D14). A legacy user config never limited which loopholes
// a workspace switched on, so every kind a workspace `enabled: true` turned on (`legacy_enable`)
// was admitted for any project that asked. So the legacy user's ceiling reads as `on_request` for
// each such kind (LegacyUserAdmission) in every project, whether or not it asks: a TOML project's
// request, the converted spelling of that switch, is admitted, the gate shows it and the human's y
// grants it, as the config-change gate's y switched it on. It is the line the user config's
// conversion writes, from whichever workspace it runs, so no order of piecemeal conversions takes
// it away. A user config in TOML is never read this way, since its `[allow.<pack>]` is the user's
// own answer (LG-D14).
func withLegacyUserAdmission(input LoopholeGrantInput) LoopholeGrantInput {
	if !input.LegacyUser {
		return input
	}
	user := input.User
	if user == nil {
		user = jsonx.NewOrderedMap()
	}
	if top := getOr(user, "allow", nil); top != nil && mapOrNil(top) == nil {
		return input // the resolver reports an `allow` that is not a table
	}
	allow := shallowCopyMap(mapOrNil(getOr(user, "allow", nil)))
	changed := false
	for _, d := range input.Declarations {
		onRequest := LegacyUserAdmission(user, d)
		if len(onRequest) == 0 {
			continue
		}
		table := jsonx.NewOrderedMap()
		addToList(table, string(LoopholeCeilingOnRequest), onRequest)
		allow.Set(d.Pack, table)
		changed = true
	}
	if !changed {
		return input
	}
	out := input
	out.User = shallowCopyMap(user)
	out.User.Set("allow", allow)
	return out
}

// LegacyUserAdmission is the `on_request` list a legacy JSONC user config's ceiling holds for d
// (LC-D14), nil when it admits nothing beyond the manifest's defaults: d's `legacy_enable` kinds
// that are not requestable by default, led by the unvalued kinds that are (writing a list ends
// their default). nil too when the user config already writes `allow.<pack>`, the user's own
// answer. The gate reads a legacy user config through it and `yolo config migrate` writes it into
// the converted user config, so the two cannot disagree.
func LegacyUserAdmission(user *jsonx.OrderedMap, d LoopholeGrantDeclaration) []string {
	if user != nil {
		if allow := mapOrNil(getOr(user, "allow", nil)); allow != nil {
			if _, wrote := allow.Get(d.Pack); wrote {
				return nil
			}
		}
	}
	var admit []string
	for _, g := range d.Grants {
		if g.LegacyEnable && !g.RequestableByDefault {
			admit = append(admit, g.Kind)
		}
	}
	if len(admit) == 0 {
		return nil
	}
	var seed []string
	for _, g := range d.Grants {
		if g.RequestableByDefault && !g.Valued {
			seed = append(seed, g.Kind)
		}
	}
	return append(seed, admit...)
}
