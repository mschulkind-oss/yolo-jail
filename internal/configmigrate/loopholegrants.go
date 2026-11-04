package configmigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loopholegrants.go is the migration's LOOPHOLE ROWS (docs/reference/loophole-grants.md §6,
// LG-D11): a legacy `loopholes.<name>` block of a grant-declaring loophole, the per-workspace
// switch file `yolo loopholes enable` wrote, and an approved workspace snapshot's
// `enabled: true` become the user's ceiling (`[allow.<pack>]`), the project's request
// (`[requests.<pack>]`) and the approval record's grants, through the same projection the gate
// reads a legacy file with (config.ProjectLegacyLoopholeGrants), so the two cannot disagree.
//
// The proof gains a layer, loopholeStandingLayer: per workspace the migration touches, per
// grant-declaring loophole, per kind, the standing a launch's gate would read (granted, pending,
// requestable, not_allowed), before and after. A grant that would vanish or appear is a printed
// difference and nothing is written.
//
// Switch files are carried only by a host migration that converts the user config, since the
// ceiling they need is the user's and the record they move into is the host's. Every switch file
// on the host is carried then, not only this workspace's, because the user config's conversion
// is what every one of them now reads through (CarriedSwitch).

// loopholeStandingLayer is the proof's layer for the grants each workspace holds.
const loopholeStandingLayer = "the loophole grants each workspace holds"

// CarriedSwitch is one workspace whose switch file (or approved snapshot) a migration carries
// into its approval record: the grants recorded and marked carried (config.CarryLoopholeGrants),
// then the grant-declaring loopholes' switches removed from the file.
type CarriedSwitch struct {
	Workspace string `json:"workspace"`
	// SwitchFile is the per-workspace file, "" when only an approved snapshot is carried.
	SwitchFile string `json:"switch_file,omitempty"`
	// Remove are the switches taken out of SwitchFile once the record holds the grants.
	Remove []string `json:"remove,omitempty"`
	// Grants are recorded per pack, `kind` or `kind=value`.
	Grants map[string][]string `json:"grants,omitempty"`
}

// ceilingLines are the kinds a pack's `[allow.<pack>]` gains.
type ceilingLines struct {
	always, onRequest []string
	// fromSwitchOnly: no user file writes the loophole, so the top-level file carries the lines.
	fromSwitchOnly bool
}

// loopholeGrantView is what the lowering of loophole blocks needs: the selected grant-declaring
// loopholes, the ceiling the user files gain, and this workspace's projected requests.
type loopholeGrantView struct {
	decls    map[string]config.LoopholeGrantDeclaration // by loophole name
	ceiling  map[string]*ceilingLines                   // by pack
	requests map[string]map[string][]string             // by pack, then kind
	// userRequests is the user-sourced request, by pack, then valued kind: the values a legacy
	// user switch on a valued kind asked every project for, written as `allow.<pack>.<kind>`
	// (LC-D16).
	userRequests map[string]map[string][]string
	// topFile is the user config's top-level file, which carries a ceiling no file writes.
	topFile string
	carries []CarriedSwitch
	// workspaces are the workspaces whose standings the proof compares, each with what the
	// after side reads that differs from the before side.
	workspaces []standingWorkspace
	// tomlCeiling is the `on_request` kinds, by pack, a user config already in TOML gains so the
	// converted workspace's requests stand where its legacy switches stood (LG-D16): the user's
	// file is rewritten in place for them (withCeilingLines). Each list is the kinds to add, in
	// order, with a new list seeded by the kinds requestable by default.
	tomlCeiling map[string][]string
	// currentProj is this workspace's projected lines.
	currentProj []config.LegacyLoopholeGrants
	// currentSwitches is this workspace's switch file's switches (an approved snapshot's enables
	// included), which outrank its config's `enabled`.
	currentSwitches map[string]bool
}

type standingWorkspace struct {
	path              string
	switches          map[string]bool
	afterSwitches     map[string]bool
	record, afterRec  map[string][]string
	ws                *jsonx.OrderedMap // the workspace config, for a workspace this plan does not convert
	convertsWorkspace bool
}

func (v *loopholeGrantView) decl(name string) (config.LoopholeGrantDeclaration, bool) {
	if v == nil {
		return config.LoopholeGrantDeclaration{}, false
	}
	d, ok := v.decls[name]
	return d, ok
}

// selectedGrantDecls is the grant-declaring loopholes of the shipped packs the user selects.
func selectedGrantDecls(packs []*packload.Pack, selected map[string]bool) []config.LoopholeGrantDeclaration {
	var chosen []*packload.Pack
	for _, p := range packs {
		if selected[p.Name] {
			chosen = append(chosen, p)
		}
	}
	return config.LoopholeGrantDeclarationsForPacks(chosen)
}

// carriesSwitches is whether this migration carries switch files: on the host, converting the
// user config itself (not a --user-layer).
func carriesSwitches(g *Graph) bool {
	if config.InJail() || g.layer != nil {
		return false
	}
	for _, f := range g.Files {
		if f.Role == RoleConvert && f.Scope == ScopeUser {
			return true
		}
	}
	return false
}

// planLoopholeGrants builds view.grants. A read that fails leaves the projection out, and the
// standing layer then shows what the converted files would change.
func planLoopholeGrants(g *Graph, view *userView, packs []*packload.Pack) {
	gv := &loopholeGrantView{decls: map[string]config.LoopholeGrantDeclaration{}, ceiling: map[string]*ceilingLines{},
		requests: map[string]map[string][]string{}, userRequests: map[string]map[string][]string{}}
	view.grants = gv
	if g.layer != nil {
		return
	}
	if top := oldTop(g); top != "" {
		gv.topFile = top
	}
	decls := selectedGrantDecls(packs, view.packs)
	if len(decls) == 0 {
		return
	}
	for _, d := range decls {
		gv.decls[d.Loophole] = d
	}
	var user *jsonx.OrderedMap
	if top := oldTop(g); top != "" {
		if read, err := config.ReadMigrationGraph(top, ""); err == nil && read != nil {
			user = read.User
		}
	}
	addCeiling := func(proj []config.LegacyLoopholeGrants, fromSwitch bool) {
		for _, p := range proj {
			c := gv.ceiling[p.Pack]
			if c == nil {
				c = &ceilingLines{fromSwitchOnly: fromSwitch}
				gv.ceiling[p.Pack] = c
			}
			if !fromSwitch && (len(p.Always) > 0 || len(p.OnRequest) > 0) {
				c.fromSwitchOnly = false
			}
			c.always = appendNew(c.always, p.Always...)
			c.onRequest = appendNew(c.onRequest, p.OnRequest...)
		}
	}
	// The user's own ceiling, the same for every workspace, and the user-sourced request its
	// switch on a valued kind makes of every project (LC-D2, LC-D16).
	userProj := config.ProjectLegacyLoopholeGrants(config.LegacyLoopholeGrantInput{User: user, Declarations: decls})
	addCeiling(userProj, false)
	for _, p := range userProj {
		for kind, values := range p.Requests {
			if len(values) == 0 {
				continue
			}
			if gv.userRequests[p.Pack] == nil {
				gv.userRequests[p.Pack] = map[string][]string{}
			}
			gv.userRequests[p.Pack][kind] = values
		}
	}

	if g.userTop != nil && g.userTop.Role == RoleConvert {
		// LC-D14: the legacy user config this plan converts admits, in every project, a request
		// for each kind a legacy workspace switch turned on (config.LegacyUserAdmission, which the
		// launch reads it through), so the converted ceiling lists each one, from whichever
		// workspace the conversion runs, or none.
		addCeiling(legacyUserAdmitted(user, decls), true)
	}

	carry := carriesSwitches(g)
	current := g.Request.Workspace
	var currentWS *jsonx.OrderedMap
	if current != "" {
		currentWS = workspaceRead(current)
	}
	seen := map[string]bool{}
	addWorkspace := func(ws, switchFile string, switches map[string]bool, wsConfig *jsonx.OrderedMap, approved []string) {
		if seen[ws] {
			return
		}
		seen[ws] = true
		sw := standingWorkspace{path: ws, switches: switches, afterSwitches: switches, ws: wsConfig,
			convertsWorkspace: ws == current && len(g.project) > 0}
		sw.record = config.RecordedLoopholeGrantPart(ws)
		sw.afterRec = sw.record
		// The approved snapshot's enables read as switches the record holds (§6's snapshot row).
		projSwitches := map[string]bool{}
		for k, v := range switches {
			projSwitches[k] = v
		}
		for _, name := range approved {
			if _, decided := projSwitches[name]; !decided {
				projSwitches[name] = true
			}
		}
		sw.switches = projSwitches
		proj := config.ProjectLegacyLoopholeGrants(config.LegacyLoopholeGrantInput{User: user, Workspace: wsConfig,
			Switches: projSwitches, Declarations: decls})
		if ws == current {
			gv.currentProj = proj
			gv.currentSwitches = projSwitches
			for _, p := range proj {
				gv.requests[p.Pack] = p.Requests
			}
		}
		if carry {
			cs := CarriedSwitch{Workspace: ws, SwitchFile: switchFile, Grants: map[string][]string{}}
			for name, on := range switches {
				// A `false` switch stays in the file: it is this workspace's withdrawal, which the
				// launch reads under any user config (LC-D18), and it records nothing to carry.
				if _, declared := gv.decls[name]; declared && on {
					cs.Remove = append(cs.Remove, name)
				}
			}
			sort.Strings(cs.Remove)
			for _, p := range proj {
				if len(p.Recorded) > 0 {
					cs.Grants[p.Pack] = append([]string(nil), p.Recorded...)
				}
			}
			addCeiling(proj, true)
			if len(cs.Remove) > 0 || len(cs.Grants) > 0 {
				gv.carries = append(gv.carries, cs)
				sw.afterSwitches = map[string]bool{}
				for k, v := range switches {
					if !inStrings(cs.Remove, k) {
						sw.afterSwitches[k] = v
					}
				}
				sw.afterRec = withCarried(sw.record, cs.Grants)
			}
		}
		gv.workspaces = append(gv.workspaces, sw)
	}
	if current != "" {
		switches, file := switchesOf(current)
		var approved []string
		if carry {
			approved = approvedEnables(current, currentWS, gv.decls)
		}
		addWorkspace(current, file, switches, currentWS, approved)
		if !carry && !config.InJail() && convertsProject(g) && g.userTop != nil && g.userTop.Role != RoleConvert &&
			g.userTop.doc.Format == configdoc.TOML {
			gv.tomlCeiling = tomlCeilingNeeded(user, gv.currentProj, gv.decls)
		}
	}
	if carry {
		for _, sf := range switchFiles() {
			addWorkspace(sf.workspace, sf.path, sf.switches, workspaceRead(sf.workspace), nil)
		}
	}
}

// convertsProject is whether this migration converts a project file.
func convertsProject(g *Graph) bool {
	for _, f := range g.project {
		if f.Role == RoleConvert {
			return true
		}
	}
	return false
}

// tomlCeilingNeeded is the `on_request` kinds, by pack, a TOML user config must gain for this
// workspace's projected lines (proj) to stand once the workspace's legacy switches are converted:
// each kind a switch admits for this launch that the user's `[allow.<pack>]` does not list. A
// pack whose table already writes `on_request` gains nothing: that list is the user's. A kind
// requestable by default needs no line. A new `on_request` list is seeded with the pack's kinds
// requestable by default, since writing one ends their default. A pack whose allow entry is not
// a table (`"off"`) gains nothing, and the proof shows what the conversion would change.
func tomlCeilingNeeded(user *jsonx.OrderedMap, proj []config.LegacyLoopholeGrants, decls map[string]config.LoopholeGrantDeclaration) map[string][]string {
	out := map[string][]string{}
	allow, _ := mapAt(user, "allow")
	for _, p := range proj {
		d, ok := decls[p.Loophole]
		if !ok || len(p.OnRequest) == 0 {
			continue
		}
		var table *jsonx.OrderedMap
		if allow != nil {
			v, has := allow.Get(p.Pack)
			if has {
				if table, ok = v.(*jsonx.OrderedMap); !ok {
					continue
				}
			}
		}
		// AN `on_request` THE USER WROTE IS THE USER'S ANSWER, and is never rewritten: `[]` means
		// no project may ask (LG-D14). The converted request then stands unadmitted, which the
		// proof shows with the user's own `yolo allow` line, and nothing is written.
		if _, writesOnRequest := tableGet(table, "on_request"); writesOnRequest {
			continue
		}
		listed := stringsAt(table, "always")
		writesOnRequest := false
		var add []string
		for _, kind := range p.OnRequest {
			if inStrings(listed, kind) {
				continue
			}
			if !writesOnRequest && requestableByDefault(d, kind) {
				continue
			}
			add = appendNew(add, kind)
		}
		if len(add) == 0 {
			continue
		}
		if !writesOnRequest {
			var seed []string
			for _, g := range d.Grants {
				if g.RequestableByDefault && !g.Valued && !inStrings(listed, g.Kind) {
					seed = append(seed, g.Kind)
				}
			}
			add = appendNew(seed, add...)
		}
		out[p.Pack] = appendNew(out[p.Pack], add...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func requestableByDefault(d config.LoopholeGrantDeclaration, kind string) bool {
	for _, g := range d.Grants {
		if g.Kind == kind {
			return g.RequestableByDefault
		}
	}
	return false
}

func tableGet(table *jsonx.OrderedMap, key string) (any, bool) {
	if table == nil {
		return nil, false
	}
	return table.Get(key)
}

// stringsAt is table's key as a list of strings, nil when it is not one.
func stringsAt(table *jsonx.OrderedMap, key string) []string {
	v, _ := tableGet(table, key)
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// workspaceRead is a workspace's config as the launch reads it, nil when it does not read.
func workspaceRead(ws string) *jsonx.OrderedMap {
	read, err := config.ReadMigrationGraph("", ws)
	if err != nil || read == nil {
		return nil
	}
	return read.Workspace
}

// switchesOf is ws's per-workspace file's switches and the file, when it applies.
func switchesOf(ws string) (map[string]bool, string) {
	wf := config.ReadWorkspaceFile(ws)
	if wf == nil || !wf.Applies {
		return nil, ""
	}
	out := map[string]bool{}
	for _, name := range wf.Loopholes.Keys() {
		on, _ := wf.Loopholes.Get(name)
		out[name] = on == true
	}
	return out, wf.Path
}

type switchFile struct {
	path, workspace string
	switches        map[string]bool
}

// switchFiles is every per-workspace file on the host that applies to the workspace it names.
func switchFiles() []switchFile {
	entries, err := os.ReadDir(paths.WorkspaceFilesDir())
	if err != nil {
		return nil
	}
	var out []switchFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonc") {
			continue
		}
		path := filepath.Join(paths.WorkspaceFilesDir(), e.Name())
		named := workspaceFileNames(path)
		if named == "" {
			continue
		}
		switches, file := switchesOf(named)
		if file != path {
			continue
		}
		out = append(out, switchFile{path: path, workspace: named, switches: switches})
	}
	return out
}

// workspaceFileNames is the `workspace` field of a per-workspace file, "" when it has none.
func workspaceFileNames(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	parsed, err := json5.Decode(data)
	if err != nil {
		return ""
	}
	m, ok := parsed.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	named, _ := m.Get("workspace")
	s, _ := named.(string)
	return s
}

// approvedEnables is the grant-declaring loopholes the workspace's approved snapshot switches on
// and its config still switches on: the human's y at that launch approved them (§6).
func approvedEnables(ws string, wsConfig *jsonx.OrderedMap, decls map[string]config.LoopholeGrantDeclaration) []string {
	text, exists, err := config.ReadApprovalRecord(ws)
	if err != nil || !exists {
		return nil
	}
	parsed, err := jsonx.Decode([]byte(text))
	if err != nil {
		return nil
	}
	snapshot, _ := parsed.(*jsonx.OrderedMap)
	var out []string
	for name := range decls {
		if legacyEnabledIn(snapshot, name) && legacyEnabledIn(wsConfig, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func legacyEnabledIn(cfg *jsonx.OrderedMap, name string) bool {
	block, ok := mapAt(cfg, "loopholes")
	if !ok {
		return false
	}
	entry, ok := mapAt(block, name)
	if !ok {
		return false
	}
	on, _ := entry.Get("enabled")
	return on == true
}

func withCarried(rec map[string][]string, grants map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range rec {
		out[k] = append([]string(nil), v...)
	}
	for pack, list := range grants {
		out[pack] = appendNew(out[pack], list...)
		out["carried:"+pack] = appendNew(out["carried:"+pack], list...)
	}
	return out
}

func appendNew(list []string, items ...string) []string {
	for _, s := range items {
		if !inStrings(list, s) {
			list = append(list, s)
		}
	}
	return list
}

func inStrings(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// placeLoopholeCeiling writes pack's ceiling lines into the lowering. Writing an `on_request`
// list ends "requestable by default", so the kinds the manifest marks so lead it, as the gate
// reads a legacy file (config.withLegacyLoopholeGrants).
func placeLoopholeCeiling(gv *loopholeGrantView, d config.LoopholeGrantDeclaration, place func([]string, any, []string) bool, oldPath []string) {
	c := gv.ceiling[d.Pack]
	if c == nil {
		return
	}
	if len(c.always) > 0 {
		place([]string{"allow", d.Pack, "always"}, anyList(c.always), oldPath)
	}
	if len(c.onRequest) > 0 {
		var list []string
		for _, g := range d.Grants {
			if g.RequestableByDefault && !g.Valued && !inStrings(c.always, g.Kind) {
				list = append(list, g.Kind)
			}
		}
		place([]string{"allow", d.Pack, "on_request"}, anyList(appendNew(list, c.onRequest...)), oldPath)
	}
}

// legacyOn is whether a legacy loophole entry says `enabled: true`.
func legacyOn(entry *jsonx.OrderedMap) bool {
	v, _ := entry.Get("enabled")
	return v == true
}

// legacyUserAdmitted is, per declaration, the `on_request` kinds a legacy user config's ceiling
// admits beyond the manifest's defaults (config.LegacyUserAdmission, LC-D14): the lines the
// converted user config must write for any project's request to stand where it stood.
func legacyUserAdmitted(user *jsonx.OrderedMap, decls []config.LoopholeGrantDeclaration) []config.LegacyLoopholeGrants {
	var out []config.LegacyLoopholeGrants
	for _, d := range decls {
		p := config.LegacyLoopholeGrants{Pack: d.Pack, Loophole: d.Loophole}
		for _, kind := range config.LegacyUserAdmission(user, d) {
			if !requestableByDefault(d, kind) {
				p.OnRequest = append(p.OnRequest, kind)
			}
		}
		if len(p.OnRequest) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// withoutKey is m without key, in a new map.
func withoutKey(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, k := range m.Keys() {
		if k == key {
			continue
		}
		v, _ := m.Get(k)
		out.Set(k, v)
	}
	return out
}

func anyList(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

// legacyGrantKey is whether key, under a grant-declaring loophole's legacy `settings`, is one a
// grant replaced: a legacy_setting (journal's `full`) or legacy_values (`visible`).
func legacyGrantKey(d config.LoopholeGrantDeclaration, key string) (setting, values bool) {
	for _, g := range d.Grants {
		if g.LegacySetting == key {
			setting = true
		}
		if g.LegacyValues == key {
			values = true
		}
	}
	return setting, values
}

// lowerProjectLoopholes converts a project file's grant-declaring loophole blocks to
// `[requests.<pack>]` (§6's project rows) and returns the rest of the table, which the generic
// lowering reports as before; nil when nothing is left.
func lowerProjectLoopholes(value any, ctx loweringContext, place func([]string, any, []string) bool, skip func([]string, any, string)) any {
	table, ok := value.(*jsonx.OrderedMap)
	gv := ctx.view().grants
	if !ok || gv == nil {
		return value
	}
	rest := jsonx.NewOrderedMap()
	for _, name := range table.Keys() {
		entryValue, _ := table.Get(name)
		d, declared := gv.decl(name)
		entry, isMap := entryValue.(*jsonx.OrderedMap)
		if offPack, offSwitch := loopholedecl.OffSwitchPack(name); isMap && offSwitch {
			if v, _ := entry.Get("enabled"); v == true && entry.Len() == 1 {
				// LC-D3: a project's `enabled: true` on an off-switch loophole turns nothing on (it
				// runs when its pack is selected, and the user's off is the user's to lift), so it
				// converts to nothing.
				ctx.drop([]string{"loopholes", name, "enabled"}, v, name+" runs whenever its pack is selected, and a project cannot lift the user's off, so the key turns nothing on")
				continue
			}
			if v, _ := entry.Get("enabled"); v == false {
				// LC-D3, LC-D15: a project's `enabled: false` keeps the loophole off for this
				// workspace's launches, which `off = true` under the pack's requests table says.
				at := []string{"loopholes", name, "enabled"}
				if !place([]string{"requests", offPack, loopholedecl.AllowOffKey}, true, at) {
					skip(at, v, "requests."+offPack+"."+loopholedecl.AllowOffKey+" is already written in this file")
				}
				if entry.Len() > 1 {
					rest.Set(name, withoutKey(entry, "enabled"))
				}
				continue
			}
		}
		if isMap && !declared && entry.Len() == 1 && legacyOn(entry) && ctx.view().packs[name] {
			v, _ := entry.Get("enabled")
			// A selected pack's grantless loophole runs whenever its pack is selected
			// (docs/reference/loophole-grants.md §4.1), so its project switch says nothing the
			// packs list does not (LC-D9's M11 row).
			ctx.drop([]string{"loopholes", name, "enabled"}, v, name+" runs whenever its pack is selected, so the key turns nothing on")
			continue
		}
		if !declared || !isMap {
			rest.Set(name, entryValue)
			continue
		}
		path := []string{"loopholes", name}
		// LC-D18: `enabled: false` is this project's withdrawal, `requests.<pack> = "off"`, which
		// takes nothing from the user's config here, an `always` line included, as the legacy
		// projection reads it. A switch file that decides the loophole outranks the line, as it
		// does at launch, so the line converts to nothing new there.
		_, switchDecides := gv.currentSwitches[name]
		withdrawn := !switchDecides && entry.Len() > 0 && func() bool { v, _ := entry.Get("enabled"); return v == false }()
		if withdrawn {
			at := append(append([]string(nil), path...), "enabled")
			if !place([]string{"requests", d.Pack}, "off", at) {
				skip(at, false, "requests."+d.Pack+" is already written in this file")
			}
		}
		asks := false
		for _, key := range entry.Keys() {
			v, _ := entry.Get(key)
			at := append(append([]string(nil), path...), key)
			if withdrawn && key == "settings" {
				// Off, the loophole read none of this project's settings, and `requests.<pack>`
				// is now the string "off", so they have no table to sit in.
				ctx.drop(at, v, name+" is off in this workspace (`\"enabled\": false`, now `requests."+d.Pack+" = \"off\"`), so its settings here reached nothing")
				continue
			}
			switch key {
			case "enabled":
				// true asks for the pack's grants; false is the withdrawal placed above, or, where a
				// switch file decides, says nothing the switch does not.
				asks = asks || v == true
			case "settings":
				settings, ok := v.(*jsonx.OrderedMap)
				if !ok {
					skip(at, v, "settings must be an object")
					continue
				}
				for _, sk := range settings.Keys() {
					sv, _ := settings.Get(sk)
					if _, values := legacyGrantKey(d, sk); values {
						asks = true
						continue
					}
					if decl, ok := loopholedecl.SettingByKey(d.Settings, sk); ok && decl.Scope == loopholedecl.SettingScopeRequest {
						// LG-D15, LC-D17: a request-scope setting is the project's to ask for, beside
						// its grants, and it replaces the user's value for this project.
						skAt := append(append([]string(nil), at...), sk)
						if !place([]string{"requests", d.Pack, sk}, sv, skAt) {
							skip(skAt, sv, "requests."+d.Pack+"."+sk+" is already written in this file")
						}
						continue
					}
					skip(append(append([]string(nil), at...), sk), sv, "a project cannot set "+name+"'s "+sk+"; it is the user's setting, allow."+d.Pack+"."+sk+" in the user config. Move it there (`yolo config set allow."+d.Pack+"."+sk+" …` on the host), then run `yolo config migrate` again")
				}
			default:
				skip(at, v, "only a loophole's `enabled` and `settings` convert; a loophole declared here (`"+key+"`) belongs in a pack: move it into one (`yolo pack init` starts one), then run `yolo config migrate` again")
			}
		}
		if !asks || withdrawn {
			continue
		}
		for _, g := range d.Grants {
			values, requested := gv.requests[d.Pack][g.Kind]
			if !requested {
				continue
			}
			var out any = true
			if g.Valued {
				out = anyList(values)
			}
			place([]string{"requests", d.Pack, g.Kind}, out, path)
		}
	}
	if rest.Len() == 0 {
		return nil
	}
	return rest
}

// loopholeStandings is, per workspace the plan touches, per pack, per kind, the standing text a
// gate reads; after picks the converted side's switches and record. user and project are the
// side's own reads; project stands in only for the workspace this plan converts.
func loopholeStandings(gv *loopholeGrantView, user, project *jsonx.OrderedMap, legacyUser, after bool) (*jsonx.OrderedMap, map[string]map[string]map[string]string) {
	out := jsonx.NewOrderedMap()
	texts := map[string]map[string]map[string]string{}
	if gv == nil || len(gv.decls) == 0 {
		return out, texts
	}
	var decls []config.LoopholeGrantDeclaration
	for _, d := range gv.decls {
		decls = append(decls, d)
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].Pack < decls[j].Pack })
	for _, sw := range gv.workspaces {
		ws := sw.ws
		if sw.convertsWorkspace {
			ws = project
		}
		in := config.LoopholeStandingInput{User: user, Workspace: ws, Switches: sw.switches, Record: sw.record, Declarations: decls, LegacyUser: legacyUser}
		if after {
			in.Switches, in.Record = sw.afterSwitches, sw.afterRec
		}
		byPack := jsonx.NewOrderedMap()
		text := map[string]map[string]string{}
		standings, err := config.LoopholeStandings(in)
		if err != nil {
			byPack.Set("error", err.Error())
		}
		for _, d := range decls {
			kinds := jsonx.NewOrderedMap()
			text[d.Pack] = map[string]string{}
			for _, g := range d.Grants {
				st, ok := standings[d.Pack][g.Kind]
				if !ok {
					continue
				}
				s := st.Standing
				if st.Standing == config.LoopholeGrantGranted || st.Standing == config.LoopholeGrantPending {
					if len(st.Values) > 0 {
						values := append([]string(nil), st.Values...)
						sort.Strings(values)
						s += ": " + strings.Join(values, ", ")
					}
				}
				kinds.Set(g.Kind, s)
				text[d.Pack][g.Kind] = s
			}
			byPack.Set(d.Pack, kinds)
		}
		out.Set(sw.path, byPack)
		texts[sw.path] = text
	}
	return out, texts
}

// standingNextSteps names, on each line of the standing layer, the step that makes it agree.
func standingNextSteps(diffs []Difference) []Difference {
	for i, d := range diffs {
		weaker := standingRank(d.After) < standingRank(d.Before)
		pack, kind := standingPackKind(d.Path)
		switch {
		case weaker && strings.Trim(d.After, `"`) == config.LoopholeGrantNotAllowed && pack != "":
			// The user's ceiling does not admit the converted request: an `on_request` the user
			// wrote, which the conversion never rewrites, or a TOML user config this conversion
			// cannot write (a jail's, LC-D16). The user's command admits it.
			diffs[i].Next = fmt.Sprintf("the converted files would take away what the old ones hold here (%s), because your user config's `[allow.%s] on_request` "+
				"does not list %q, and this conversion writes no line into a list you wrote or a user config it does not cover; to let projects ask for it, run `yolo allow %s on_request %s` on the host, "+
				"then run `yolo config migrate` again, or keep the old files (this yolo keeps reading them)", d.Before, pack, kind, pack, kind)
		case weaker:
			diffs[i].Next = fmt.Sprintf("the converted files would take away what the old ones hold here (%s); write the grant's line in the old files "+
				"(`\"enabled\": true` under loopholes.<name>, or its settings) so the projection keeps it, or keep the old files (this yolo keeps reading them), then run `yolo config migrate` again", d.Before)
		default:
			diffs[i].Next = fmt.Sprintf("the converted files would hold more here (%s) than the old ones do; to keep this project from it, write "+
				"`\"enabled\": false` under the loophole's loopholes entry in the old workspace file, which converts to `requests.<pack> = \"off\"`, "+
				"or keep the old files (this yolo keeps reading them), then run `yolo config migrate` again", d.After)
		}
	}
	return diffs
}

// standingPackKind is the pack and kind a standing-layer path ("<workspace>.<pack>.<kind>")
// ends in, or "" when it has no such tail.
func standingPackKind(path string) (pack, kind string) {
	k := strings.LastIndex(path, ".")
	if k <= 0 {
		return "", ""
	}
	p := strings.LastIndex(path[:k], ".")
	if p < 0 {
		return "", ""
	}
	return path[p+1 : k], path[k+1:]
}

func standingRank(s string) int {
	s = strings.Trim(s, `"`)
	switch {
	case strings.HasPrefix(s, config.LoopholeGrantGranted):
		return 3
	case strings.HasPrefix(s, config.LoopholeGrantPending):
		return 2
	case strings.HasPrefix(s, config.LoopholeGrantRequestable):
		return 1
	}
	return 0
}

// carryLoopholeGrants is the journal's carry step: each workspace's grants into its record, then
// its switches out of the switch file. Run again by Resume, it does the same: both halves are
// idempotent.
func carryLoopholeGrants(carries []CarriedSwitch) error {
	for _, c := range carries {
		if err := config.CarryLoopholeGrants(c.Workspace, c.Grants); err != nil {
			return fmt.Errorf("record %s's carried loophole grants: %w", c.Workspace, err)
		}
		for _, name := range c.Remove {
			if _, _, err := config.RemoveWorkspaceLoophole(c.Workspace, name); err != nil {
				return fmt.Errorf("remove the %s switch from %s, whose grant the record now holds: %w", name, c.SwitchFile, err)
			}
		}
	}
	return nil
}

func sortedDeclNames(gv *loopholeGrantView) []string {
	out := make([]string, 0, len(gv.decls))
	for name := range gv.decls {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
