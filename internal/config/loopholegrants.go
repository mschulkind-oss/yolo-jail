package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// loopholegrants.go reads the two layers of a grant-declaring loophole's approval
// (docs/reference/loophole-grants.md §4.2): the user's CEILING, `[allow.<pack>]` with its
// `always` and `on_request` lists, and the project's REQUEST, `[requests.<pack>]` with
// `<kind> = true` for an unvalued kind and `<kind> = [values]` for a valued one. From them it
// computes each declared kind's standing BEFORE the approval record: whether the gate would hold
// it (a candidate), the project could ask for it (requestable), or the ceiling leaves it out
// (not_allowed). Whether a candidate is granted or still pending is the record's to say, and is
// read by the gate, not here.
//
// A GRANT-DECLARING PACK is one a loophole of which declares `[[grants]]`. Its `allow` table's
// `always` and `on_request` are the ceiling; every other key stays a declared setting, which is
// the aws-auth reading (loopholes.ConfigBlock) for every pack. An ungated pack's `allow` table
// keeps meaning settings only.

// LoopholeCeiling is where the user's ceiling puts a kind.
type LoopholeCeiling string

const (
	// LoopholeCeilingNone: in neither list, so nothing is granted and a request is skipped.
	LoopholeCeilingNone LoopholeCeiling = ""
	// LoopholeCeilingAlways: every project gets it, once its gate is answered y.
	LoopholeCeilingAlways LoopholeCeiling = "always"
	// LoopholeCeilingOnRequest: a project may ask for it.
	LoopholeCeilingOnRequest LoopholeCeiling = "on_request"
)

// LoopholeKindStanding is a kind's standing before the record is read.
type LoopholeKindStanding string

const (
	// LoopholeKindCandidate is a kind the gate would hold for this project: under `always`, or
	// requested within `on_request`. The record makes it granted, or leaves it pending.
	LoopholeKindCandidate LoopholeKindStanding = "candidate"
	// LoopholeKindRequestable is a kind in `on_request` that the project has not asked for.
	LoopholeKindRequestable LoopholeKindStanding = "requestable"
	// LoopholeKindNotAllowed is a kind outside the ceiling; a request for it is skipped.
	LoopholeKindNotAllowed LoopholeKindStanding = "not_allowed"
)

// LoopholeGrantDeclaration is one selected grant-declaring loophole, as its manifest says.
type LoopholeGrantDeclaration struct {
	Pack     string
	Loophole string
	Access   string
	Grants   []loopholedecl.Grant
	// Settings is the loophole's settings declarations, for `yolo config migrate` to place a
	// project's request-scope setting (LG-D15) under its `[requests.<pack>]` table.
	Settings []loopholedecl.Setting
}

// LoopholeKindState is one declared kind's ceiling, request and standing.
type LoopholeKindState struct {
	Grant   loopholedecl.Grant
	Ceiling LoopholeCeiling
	// CeilingByDefault: in on_request only because the manifest marks it requestable by default
	// and the user wrote no on_request list.
	CeilingByDefault bool
	CeilingFiles     []string
	Requested        bool
	// Values are a valued kind's requested values.
	Values       []string
	RequestPath  string
	RequestFiles []string
	Standing     LoopholeKindStanding
}

// LoopholeSkippedRequest is a project's request the ceiling leaves out, named so `yolo check`
// and the gate can say it was skipped and how to allow it.
type LoopholeSkippedRequest struct {
	Pack   string
	Kind   string
	Path   string
	Files  []string
	Values []string
}

// LoopholePackGrants is one grant-declaring pack's kinds, in declaration order.
type LoopholePackGrants struct {
	Pack     string
	Loophole string
	Access   string
	Kinds    []LoopholeKindState
	Skipped  []LoopholeSkippedRequest
}

// LoopholeGrantPlan is every selected grant-declaring pack's standing before the record.
type LoopholeGrantPlan struct {
	Packs []LoopholePackGrants
}

// LoopholeGrantInput is the two layers and the selected declarations. Origins are keyed by
// dotted path ("allow.journal.on_request", "requests.journal.read"), as ResolveReadableAllowance
// keys them.
type LoopholeGrantInput struct {
	User             *jsonx.OrderedMap
	Workspace        *jsonx.OrderedMap
	Declarations     []LoopholeGrantDeclaration
	UserOrigins      map[string][]string
	WorkspaceOrigins map[string][]string
	// LegacyUser is whether User is a legacy JSONC user config, whose ceiling admits a TOML
	// project's request for a kind a legacy workspace switch turned on (LC-D14).
	LegacyUser bool
}

// ValuedKindUnderAlwaysError is the refusal of a valued kind in a ceiling's `always`: its value
// is the project's to give, so `always` has none to carry (§4.2). The message is §5's, which the
// validator prefixes with where the list was written.
type ValuedKindUnderAlwaysError struct {
	Pack string
	Kind string
}

func (e *ValuedKindUnderAlwaysError) Error() string {
	return fmt.Sprintf("allow.%s.always lists %s, which carries a value only a project can give.\n"+
		"  Move it:  yolo config unset allow.%s.always %s   then  yolo allow %s on_request %s",
		e.Pack, e.Kind, e.Pack, e.Kind, e.Pack, e.Kind)
}

// LoopholeGrantDeclarationsForPacks is the grant-declaring loopholes the loaded packs ship. A
// module whose manifest did not load declares nothing here; the pack readers report it.
func LoopholeGrantDeclarationsForPacks(packs []*packload.Pack) []LoopholeGrantDeclaration {
	var out []LoopholeGrantDeclaration
	for _, pack := range packs {
		if pack == nil {
			continue
		}
		modules, _, _, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			continue
		}
		for _, module := range modules {
			if module.Decl == nil || len(module.Decl.Grants) == 0 {
				continue
			}
			out = append(out, LoopholeGrantDeclaration{Pack: pack.Name, Loophole: module.Name,
				Access: module.Decl.Access, Grants: append([]loopholedecl.Grant(nil), module.Decl.Grants...),
				Settings: append([]loopholedecl.Setting(nil), module.Decl.Settings...)})
		}
	}
	return out
}

// ResolveLoopholeGrants reads each declaration's ceiling and request. A malformed list, an
// undeclared kind, a request of the wrong shape and a valued kind under `always` are errors; a
// well-formed request outside the ceiling is kept, skipped and named.
func ResolveLoopholeGrants(input LoopholeGrantInput) (LoopholeGrantPlan, error) {
	byPack := map[string]LoopholeGrantDeclaration{}
	var order []string
	for _, d := range input.Declarations {
		if prev, seen := byPack[d.Pack]; seen {
			return LoopholeGrantPlan{}, fmt.Errorf("pack %q declares grants in two loopholes, %q and %q; "+
				"[allow.%s] and [requests.%s] name one set of kinds, so declare them in one loophole",
				d.Pack, prev.Loophole, d.Loophole, d.Pack, d.Pack)
		}
		byPack[d.Pack] = d
		order = append(order, d.Pack)
	}
	allow, err := readableTable(input.User, "allow")
	if err != nil {
		return LoopholeGrantPlan{}, err
	}
	requests, err := readableTable(input.Workspace, "requests")
	if err != nil {
		return LoopholeGrantPlan{}, err
	}
	var plan LoopholeGrantPlan
	for _, pack := range order {
		d := byPack[pack]
		out := LoopholePackGrants{Pack: d.Pack, Loophole: d.Loophole, Access: d.Access}
		ceiling, err := loopholeCeilingOf(allow, d, input.UserOrigins)
		if err != nil {
			return LoopholeGrantPlan{}, err
		}
		requested, err := loopholeRequestsOf(requests, d)
		if err != nil {
			return LoopholeGrantPlan{}, err
		}
		for _, g := range d.Grants {
			state := LoopholeKindState{Grant: g}
			if c, ok := ceiling.kinds[g.Kind]; ok {
				state.Ceiling, state.CeilingFiles = c.ceiling, c.files
			} else if g.RequestableByDefault && !ceiling.wroteOnRequest {
				state.Ceiling, state.CeilingByDefault = LoopholeCeilingOnRequest, true
			}
			r, asked := requested[g.Kind]
			if asked {
				path := "requests." + d.Pack + "." + g.Kind
				state.Requested, state.Values = true, r
				state.RequestPath, state.RequestFiles = path, cloneOrigins(input.WorkspaceOrigins[path])
			}
			switch {
			case state.Ceiling == LoopholeCeilingAlways:
				state.Standing = LoopholeKindCandidate
			case state.Ceiling == LoopholeCeilingOnRequest && asked:
				state.Standing = LoopholeKindCandidate
			case state.Ceiling == LoopholeCeilingOnRequest:
				state.Standing = LoopholeKindRequestable
			default:
				state.Standing = LoopholeKindNotAllowed
				if asked {
					out.Skipped = append(out.Skipped, LoopholeSkippedRequest{Pack: d.Pack, Kind: g.Kind,
						Path: state.RequestPath, Files: cloneOrigins(state.RequestFiles), Values: append([]string(nil), r...)})
				}
			}
			out.Kinds = append(out.Kinds, state)
		}
		plan.Packs = append(plan.Packs, out)
	}
	return plan, nil
}

type loopholeCeilingEntry struct {
	ceiling LoopholeCeiling
	files   []string
}

type loopholeCeilingRead struct {
	kinds          map[string]loopholeCeilingEntry
	wroteOnRequest bool
}

// loopholeCeilingOf reads `allow.<pack>.always` and `.on_request`. `always` wins a kind both
// list. A table written "off" is no ceiling at all.
func loopholeCeilingOf(allow *jsonx.OrderedMap, d LoopholeGrantDeclaration, origins map[string][]string) (loopholeCeilingRead, error) {
	read := loopholeCeilingRead{kinds: map[string]loopholeCeilingEntry{}}
	value, present := allow.Get(d.Pack)
	if !present || isReadableTableOff(value) {
		return read, nil
	}
	table, ok := asMap(value)
	if !ok {
		return read, fmt.Errorf("allow.%s: expected a table", d.Pack)
	}
	for _, list := range []LoopholeCeiling{LoopholeCeilingOnRequest, LoopholeCeilingAlways} {
		raw, written := table.Get(string(list))
		if !written {
			continue
		}
		path := "allow." + d.Pack + "." + string(list)
		kinds, err := readableStringList(raw, path)
		if err != nil {
			return read, err
		}
		if list == LoopholeCeilingOnRequest {
			read.wroteOnRequest = true
		}
		for _, kind := range kinds {
			g, declared := loopholeGrantByKind(d.Grants, kind)
			if !declared {
				return read, fmt.Errorf("%s lists %s, which the %s pack does not declare; its grant kinds are %s",
					path, kind, d.Pack, strings.Join(loopholeGrantKinds(d.Grants), ", "))
			}
			if list == LoopholeCeilingAlways && g.Valued {
				return read, &ValuedKindUnderAlwaysError{Pack: d.Pack, Kind: kind}
			}
			read.kinds[kind] = loopholeCeilingEntry{ceiling: list, files: cloneOrigins(origins[path])}
		}
	}
	// A valued kind's own key is the user-sourced request (LC-D16), laid over each project by
	// withUserSourcedRequests; it is well formed only where the ceiling lets a project ask.
	onRequest, _ := table.Get(string(LoopholeCeilingOnRequest))
	for _, g := range d.Grants {
		if value, wrote := table.Get(g.Kind); wrote && g.Valued {
			if err := userSourcedRequestError(d.Pack, g, value, onRequest); err != nil {
				return read, err
			}
		}
	}
	return read, nil
}

// loopholeRequestsOf reads `requests.<pack>`'s grant kinds: true for an unvalued kind, a list of
// strings for a valued one. `false` and an empty list ask for nothing and are left out. A key
// that is not a kind is left to the settings reader.
func loopholeRequestsOf(requests *jsonx.OrderedMap, d LoopholeGrantDeclaration) (map[string][]string, error) {
	out := map[string][]string{}
	value, present := requests.Get(d.Pack)
	if !present || isReadableTableOff(value) {
		return out, nil
	}
	table, ok := asMap(value)
	if !ok {
		return out, fmt.Errorf("requests.%s: expected a table", d.Pack)
	}
	for _, key := range table.Keys() {
		g, declared := loopholeGrantByKind(d.Grants, key)
		if !declared {
			continue
		}
		path := "requests." + d.Pack + "." + key
		raw := readableValue(table, key)
		if !g.Valued {
			asked, isBool := raw.(bool)
			if !isBool {
				return out, fmt.Errorf("%s: %s carries no value; write %s = true", path, key, key)
			}
			if asked {
				out[key] = nil
			}
			continue
		}
		values, err := readableStringList(raw, path)
		if err != nil {
			noun := g.ValueNoun
			if noun == "" {
				noun = "values"
			}
			return out, fmt.Errorf("%s: %s carries %s; write %s = [\"...\"], one entry each", path, key, noun, key)
		}
		if len(values) > 0 {
			out[key] = values
		}
	}
	return out, nil
}

func loopholeGrantByKind(grants []loopholedecl.Grant, kind string) (loopholedecl.Grant, bool) {
	for _, g := range grants {
		if g.Kind == kind {
			return g, true
		}
	}
	return loopholedecl.Grant{}, false
}

func loopholeGrantKinds(grants []loopholedecl.Grant) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.Kind)
	}
	return out
}

// valuedGrantKind is d's valued grant kind named key, the kind a user config's
// `[allow.<pack>] <kind> = [...]` names the user-sourced request of (LC-D16).
func valuedGrantKind(d LoopholeGrantDeclaration, key string) (loopholedecl.Grant, bool) {
	g, ok := loopholeGrantByKind(d.Grants, key)
	return g, ok && g.Valued
}

// userSourcedRequestError is the refusal of a user config's `allow.<pack>.<kind>` that is not a
// list of values, or that names values for a kind its `on_request` list leaves out, so no project
// could ask for them. nil when it is well formed.
func userSourcedRequestError(pack string, g loopholedecl.Grant, value, onRequest any) error {
	path := "allow." + pack + "." + g.Kind
	values, err := readableStringList(value, path)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("%s is empty, so it asks for nothing; name the %s every project asks for, or remove it "+
			"(`yolo config unset %s`)", path, g.ValueNoun, path)
	}
	listed := false
	if onRequest == nil {
		listed = g.RequestableByDefault
	} else if kinds, err := readableStringList(onRequest, "allow."+pack+".on_request"); err == nil {
		listed = slices.Contains(kinds, g.Kind)
	}
	if !listed {
		return fmt.Errorf("%s names the %s every project asks for, but allow.%s.on_request does not list %s, so no "+
			"project may ask for them; run `yolo allow %s on_request %s`, or remove the values (`yolo config unset %s`)",
			path, g.ValueNoun, pack, g.Kind, pack, g.Kind, path)
	}
	return nil
}

// withUserSourcedRequests is input with each user-sourced request laid over the project
// (docs/design/legacy-config-compat.md LC-D16, the TOML spelling of a v0.15.1 user switch on a
// valued kind): a user config's `[allow.<pack>] <kind> = [...]` is the request every project
// makes that writes none of its own for that kind, so it is shown at each project's gate and its
// y grants it there, as LG-D12's `always` line is. A project's own request for the kind, or a
// `[requests.<pack>]` it writes "off", wins. The laid request's origin is the user's line.
func withUserSourcedRequests(input LoopholeGrantInput) LoopholeGrantInput {
	if input.User == nil {
		return input
	}
	allow := mapOrNil(getOr(input.User, "allow", nil))
	if allow == nil {
		return input
	}
	ws := input.Workspace // nil when no project file is read: the request is still each project's
	if ws == nil {
		ws = jsonx.NewOrderedMap()
	}
	if top := getOr(ws, "requests", nil); top != nil && mapOrNil(top) == nil {
		return input // the resolver reports a `requests` that is not a table
	}
	var requests *jsonx.OrderedMap
	for _, d := range input.Declarations {
		table := mapOrNil(getOr(allow, d.Pack, nil))
		if table == nil {
			continue
		}
		for _, g := range d.Grants {
			if !g.Valued {
				continue
			}
			value, wrote := table.Get(g.Kind)
			if !wrote {
				continue
			}
			if requests == nil {
				requests = shallowCopyMap(mapOrNil(getOr(ws, "requests", nil)))
			}
			existing, has := requests.Get(d.Pack)
			if has && mapOrNil(existing) == nil {
				continue // written "off", or wrong, which the resolver reads
			}
			packReq := shallowCopyMap(mapOrNil(existing))
			if _, own := packReq.Get(g.Kind); own {
				continue
			}
			packReq.Set(g.Kind, value)
			requests.Set(d.Pack, packReq)
			if input.WorkspaceOrigins != nil {
				input.WorkspaceOrigins = copyOrigins(input.WorkspaceOrigins)
				input.WorkspaceOrigins["requests."+d.Pack+"."+g.Kind] = cloneOrigins(input.UserOrigins["allow."+d.Pack+"."+g.Kind])
			}
		}
	}
	if requests == nil {
		return input
	}
	out := input
	out.Workspace = shallowCopyMap(ws)
	out.Workspace.Set("requests", requests)
	return out
}

// loopholeGrantDeclarationsOf is the grant-declaring loopholes among the readable pack
// declarations: every one keyed by pack (selected or not, so an unselected pack's table is still
// read as a ceiling rather than as settings), and the selected ones in order.
func loopholeGrantDeclarationsOf(packs []ReadablePackDeclaration, selected map[string]bool) (map[string]LoopholeGrantDeclaration, []LoopholeGrantDeclaration) {
	byPack := map[string]LoopholeGrantDeclaration{}
	var list []LoopholeGrantDeclaration
	for _, p := range packs {
		if len(p.Grants) == 0 {
			continue
		}
		d := LoopholeGrantDeclaration{Pack: p.Pack, Loophole: p.Loophole, Access: p.Access,
			Grants: append([]loopholedecl.Grant(nil), p.Grants...)}
		if _, seen := byPack[p.Pack]; !seen {
			byPack[p.Pack] = d
		}
		if selected[p.Pack] {
			list = append(list, d)
		}
	}
	return byPack, list
}

// --- the validator (`yolo check` and every launch) -------------------------------------------

// validateLoopholeGrants judges the selected grant-declaring packs' ceilings and this project's
// requests: a valued kind under `always`, an undeclared kind or a misshapen request is an error;
// a request outside the ceiling is a warning naming the command that allows it (§5). Each is
// located at the line it was written on. Nothing to read when no selected pack declares grants.
func validateLoopholeGrants(config *jsonx.OrderedMap, workspace string, errs, warns *[]string, edit ...*editValidationContext) {
	_, hasAllow := config.Get("allow")
	_, hasRequests := config.Get("requests")
	_, hasLegacy := config.Get("loopholes") // read through the legacy projection
	if !hasAllow && !hasRequests && !hasLegacy {
		return
	}
	var packs []*packload.Pack
	if len(edit) != 0 && edit[0] != nil {
		packs = edit[0].packs
	} else {
		packs, _ = resolveSelectedPacks()
	}
	declarations := LoopholeGrantDeclarationsForPacks(packs)
	if len(declarations) == 0 {
		return
	}
	var user, ws *jsonx.OrderedMap
	var userSrc *Sources
	var wsNode *srcNode
	if len(edit) != 0 && edit[0] != nil {
		user, userSrc = edit[0].rawUser, edit[0].userOrigins
		ws, wsNode = edit[0].rawWorkspace, edit[0].workspaceOrigins.node()
	} else {
		var err error
		user, userSrc, err = LoadReadableUserScopeConfigWithSources(false, func(string) {})
		if err != nil {
			return // the user config's own read reports it
		}
		ws, wsNode, err = loadMountsWorkspaceConfig(workspace)
		if err != nil {
			return // the workspace config's own read reports it
		}
	}
	input, _, _ := withLegacyLoopholeGrants(LoopholeGrantInput{User: user, Workspace: ws, Declarations: declarations,
		LegacyUser: UserConfigIsLegacy()},
		userSrc, sourcesOf(wsNode), workspace)
	plan, err := ResolveLoopholeGrants(input)
	if err != nil {
		var valued *ValuedKindUnderAlwaysError
		msg := err.Error()
		if errors.As(err, &valued) {
			msg = lineLocated(userSrc.node(), msg, "allow", valued.Pack, "always")
		}
		if inJail() {
			add(warns, msg+inJailSuffix)
			return
		}
		add(errs, msg)
		return
	}
	for _, p := range plan.Packs {
		for _, s := range p.Skipped {
			add(warns, lineLocated(wsNode, skippedLoopholeRequestMessage(s), "requests", s.Pack, s.Kind))
		}
	}
}

// skippedLoopholeRequestMessage is §5's `yolo check` text for a request outside the ceiling.
func skippedLoopholeRequestMessage(s LoopholeSkippedRequest) string {
	what := s.Pack + " " + s.Kind
	if len(s.Values) > 0 {
		what += " " + strings.Join(s.Values, ", ")
	}
	return fmt.Sprintf("this project requests %s.\n"+
		"  Your config does not allow %s on request, so this project runs without it.\n"+
		"  To allow it for every project you work on (in your config, not this file):\n"+
		"    yolo allow %s on_request %s", what, s.Kind, s.Pack, s.Kind)
}

// lineLocated is msg prefixed with "<file>:<line>: " for the value at keys in n's record, the
// highest-precedence writer's, the spelling §5's messages take; msg unchanged when the record
// does not locate it.
func lineLocated(n *srcNode, msg string, keys ...string) string {
	for _, k := range keys {
		if n == nil {
			return msg
		}
		n = n.key(k)
	}
	if n == nil {
		return msg
	}
	origins := n.origins()
	if len(origins) == 0 || origins[0].file == nil || origins[0].host != "" {
		return msg
	}
	o := origins[0]
	// A workspace file is named as the project knows it (yolo-jail.toml), §5's spelling; any
	// other by its label, the home written as ~.
	label := o.file.label()
	if o.file.contained && o.file.rel != "" {
		label = termsafe.Visible(o.file.rel)
	}
	span, ok := o.file.locate(o.steps, o.documentSteps)
	if !ok {
		return label + ": " + msg
	}
	line, _ := o.file.position(span.Start)
	return fmt.Sprintf("%s:%d: %s", label, line, msg)
}
