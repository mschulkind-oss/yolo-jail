package luahook

// derive.go is the PRODUCER half of the Lua slot, distinct from the mutator
// (transform) half in vm.go.
//
// A `transform` runs POST-merge: it receives ctx.config (the composed surface)
// and mutates it. A `derive` runs PRE-merge: it receives the live config tables
// (mcp_servers, lsp_servers) and RETURNS a fresh object — the computed layer that
// feeds Inputs.Computed (packsurfaces.go). It is the one place a pack runs Lua:
// a sandboxed producer of a config value, never an effect
// (docs/design/pack-system.md §7).
//
// Two facilities a derive needs that a transform does not, both added here:
//
//   - the live tables, exposed read-only as ctx.mcp_servers / ctx.lsp_servers.
//     A derive is a pure function of these; it may not mutate them.
//   - a TOMBSTONE sentinel, ctx.tombstone. The computed layer uses Go nil as an
//     RFC-7386 "delete this key" marker (compose.go), and BuildComputed emitted
//     Go nil for a tombstone declaration, a false flag, and a dropped key. But
//     Lua tables CANNOT hold nil as a value — assigning nil deletes the key
//     (marshal.go). So `out.x = nil` in Lua would OMIT x, not tombstone it — a
//     silent behavior change from the DSL. ctx.tombstone is a unique sentinel
//     userdata that survives the round-trip and is decoded back to Go nil ONLY on
//     the derive path, reproducing the DSL's tombstone semantics exactly.

import (
	"context"
	"fmt"
	"sort"

	lua "github.com/yuin/gopher-lua"
)

// DeriveCtx is what a derive function receives. It is deliberately NOT the
// transform Ctx: a derive does not see the composed config or the stage handle,
// only the inputs it is a pure function of.
type DeriveCtx struct {
	// Agent / Surface identify which registered derive fn to invoke, matching the
	// (agent, name) surface identity — a derive registers with BOTH, because one
	// agent has several surfaces (claude: config + settings) with different
	// derivations.
	Agent   string
	Surface string

	// Env selects the ENV producer instead of a surface derive: when true, Derive
	// invokes the yolo.env(agent, fn) registration — a THIRD registration with a key
	// space of its own, so a pack declaring a real surface named "env" can never
	// collide with the environment composition (the surface loop never sets this, and
	// never sees the env registration at all). The producer returns a flat table of
	// string values; ctx.tombstone in a value is a REMOVAL, decoded to Go nil here
	// exactly as on the surface path.
	Env bool

	// SelectedProvider is the provider the active profile resolved to at this agent's
	// CLI name — the key the env producer reads ctx.providers with. Exposed as
	// ctx.selected_provider; "" when no variant is active.
	SelectedProvider string

	// ProfileName is the variant active at this agent's CLI name, exposed as
	// ctx.profile_name.
	ProfileName string

	// Profile is the ACTIVE profile's resolved option map (the provider-declared defaults
	// with the profile's own values over them — provider-catalog-and-selection.md §5.2),
	// exposed as ctx.profile. ALWAYS a table, empty when no profile is active, so a derive
	// can read ctx.profile.model without a nil guard and "no profile" is the same world to
	// it as "a profile with no options". What the options MEAN — which one is the
	// selection surface — is the derive's business (OQ-CS4); the empty map is what carries
	// OQ-CS2's ruling (§5.1) to it: no active profile means the derive writes nothing, so
	// the agent's own choice of model stays untouched.
	Profile map[string]string

	// Tables are the live config tables a derive may read, keyed by source name
	// (manifest.SourceMCPServers / SourceLSPServers). Exposed read-only as
	// ctx.<name>. Absent source => an empty table (a jail with no MCP configured
	// and one with an empty table are the same world — matches BuildComputed).
	Tables map[string]map[string]any
}

// DeriveVM is the boundary for running a derive producer, mirroring LuaVM. The
// production impl is GopherLuaVM (it satisfies both).
type DeriveVM interface {
	// Derive runs script, invokes the derive fn registered for (ctx.Agent,
	// ctx.Surface), and returns the object it produced — the computed layer.
	// Returns (nil, nil) when the script registers no derive for this surface
	// (the identity: no computed layer). Any Lua error is a non-nil Go error.
	Derive(script string, ctx *DeriveCtx) (map[string]any, error)
}

// tombstoneName / emptyArrayName are the globals under which the two derive
// sentinels are exposed (as ctx.tombstone / ctx.empty_array), recognized by
// identity when marshalling the derive's return back to Go.
const (
	tombstoneName  = "yolo_tombstone_sentinel"
	emptyArrayName = "yolo_empty_array_sentinel"
)

// Derive implements DeriveVM on the same gopher-lua VM as Run. It builds the
// sandbox identically (openSandboxLibs), exposes the live tables + tombstone as
// ctx, runs the registration script, invokes the (agent, surface) derive, and
// marshals the returned table back — converting the tombstone sentinel to Go nil.
func (vm GopherLuaVM) Derive(script string, ctx *DeriveCtx) (map[string]any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("luahook: nil derive ctx")
	}

	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()

	timeout := vm.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	goCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	L.SetContext(goCtx)

	if err := openSandboxLibs(L); err != nil {
		return nil, err
	}

	// The tombstone sentinel: a unique userdata, exposed as ctx.tombstone and
	// recognized by identity when marshalling back to Go nil.
	sentinel := L.NewUserData()
	L.SetGlobal(tombstoneName, sentinel)

	// The empty-array sentinel. Lua cannot distinguish {} the array from {} the
	// object, and the marshaller decodes an empty table to an empty OBJECT
	// (map[string]any) — so a derive that wants an empty ARRAY (e.g. a defaulted
	// `args = []`, which must render as JSON `[]`, not `{}`) needs a distinct
	// marker. ctx.empty_array is that marker, decoded back to []any{}. A NON-empty
	// array is unambiguous (1..n integer keys) and needs no sentinel.
	emptyArr := L.NewUserData()
	L.SetGlobal(emptyArrayName, emptyArr)

	// yolo.derive(agent, surface, fn) records fn keyed by (agent, surface).
	type key struct{ agent, surface string }
	derives := map[key]*lua.LFunction{}
	yolo := L.NewTable()
	L.SetField(yolo, "derive", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		surface := L.CheckString(2)
		fn := L.CheckFunction(3)
		derives[key{agent, surface}] = fn
		return 0
	}))
	// yolo.env(agent, fn) records fn keyed by agent ALONE. A separate table, not a
	// surface name: the environment composition is not a surface (nothing renders it
	// to a file), so keying it by (agent, "env") would let a pack's REAL surface named
	// "env" collide with it. Distinct storage is what makes the collision
	// unrepresentable — see DeriveCtx.Env.
	envs := map[string]*lua.LFunction{}
	L.SetField(yolo, "env", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		fn := L.CheckFunction(2)
		envs[agent] = fn
		return 0
	}))
	L.SetGlobal("yolo", yolo)

	ctxTable, err := buildDeriveCtxTable(L, ctx, sentinel, emptyArr)
	if err != nil {
		return nil, err
	}
	L.SetGlobal("ctx", ctxTable)

	if err := L.DoString(script); err != nil {
		return nil, wrapLuaErr(err)
	}

	fn, ok := derives[key{ctx.Agent, ctx.Surface}]
	who := fmt.Sprintf("derive for %s/%s", ctx.Agent, ctx.Surface)
	if ctx.Env {
		fn, ok = envs[ctx.Agent]
		who = "env producer for " + ctx.Agent
	}
	if !ok {
		// No derive registered for this surface — the identity (no computed layer).
		// The env spelling lands here the same way: an agent whose pack registered no
		// yolo.env composes no environment.
		return nil, nil
	}
	if err := L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, ctxTable); err != nil {
		return nil, wrapLuaErr(err)
	}
	ret := L.Get(-1)
	L.Pop(1)

	tbl, isTable := ret.(*lua.LTable)
	if !isTable {
		return nil, fmt.Errorf("luahook: %s returned %s, want a table (the computed layer)",
			who, ret.Type())
	}
	out, err := deriveTableToGo(tbl, sentinel, emptyArr)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildDeriveCtxTable exposes the derive inputs: the two sentinels
// (ctx.tombstone, ctx.empty_array), ctx.agent / ctx.surface, the resolved selection
// (ctx.selected_provider, ctx.profile_name), and one read-only table per live source
// (ctx.mcp_servers, ctx.lsp_servers).
func buildDeriveCtxTable(L *lua.LState, ctx *DeriveCtx, sentinel, emptyArr *lua.LUserData) (*lua.LTable, error) {
	t := L.NewTable()
	L.SetField(t, "tombstone", sentinel)
	L.SetField(t, "empty_array", emptyArr)
	L.SetField(t, "agent", lua.LString(ctx.Agent))
	L.SetField(t, "surface", lua.LString(ctx.Surface))
	L.SetField(t, "selected_provider", lua.LString(ctx.SelectedProvider))
	L.SetField(t, "profile_name", lua.LString(ctx.ProfileName))
	// ctx.profile, always a table. Keys are sorted because a Go map has no order and a
	// derive that iterates it must not see a different order between runs.
	profile := L.NewTable()
	for _, k := range sortedStringKeys(ctx.Profile) {
		L.SetField(profile, k, lua.LString(ctx.Profile[k]))
	}
	L.SetField(t, "profile", profile)
	for _, src := range knownDeriveSources {
		table := ctx.Tables[src]
		if table == nil {
			table = map[string]any{}
		}
		lv, err := goToLua(L, table)
		if err != nil {
			return nil, fmt.Errorf("luahook: marshalling ctx.%s: %w", src, err)
		}
		L.SetField(t, src, lv)
	}
	return t, nil
}

// sortedStringKeys returns a string map's keys sorted — a Go map has no order, and the
// ctx table a derive reads must not reshuffle between runs.
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// knownDeriveSources are the live-table names exposed to a derive as ctx.<name>.
// Kept in step with manifest's Source* constants by the caller (which builds the
// Tables map from exactly these); listed here so a derive always sees every
// source as at least an empty table, never a nil index.
var knownDeriveSources = []string{"mcp_servers", "lsp_servers", "providers", "use_profiles"}

// deriveTableToGo is luaTableToGo specialized for the derive return: a value
// equal to the tombstone sentinel decodes to Go nil (the RFC-7386 delete marker
// the computed layer uses) instead of being dropped, and the empty-array sentinel
// decodes to []any{} instead of the ambiguous empty {}.
func deriveTableToGo(tbl *lua.LTable, sentinel, emptyArr *lua.LUserData) (map[string]any, error) {
	out := map[string]any{}
	var iterErr error
	tbl.ForEach(func(k, v lua.LValue) {
		if iterErr != nil {
			return
		}
		ks, ok := k.(lua.LString)
		if !ok {
			// The computed layer is always object-rooted; a non-string top-level key
			// is a derive bug worth surfacing rather than silently coercing.
			iterErr = fmt.Errorf("luahook: derive produced a non-string top-level key %s", k.Type())
			return
		}
		gv, err := deriveValueToGo(v, sentinel, emptyArr)
		if err != nil {
			iterErr = err
			return
		}
		out[string(ks)] = gv
	})
	return out, iterErr
}

// deriveValueToGo converts one Lua value, mapping the tombstone sentinel to Go
// nil and the empty-array sentinel to []any{}, and recursing into tables so a
// nested sentinel (a false flag under enabledPlugins; a defaulted args=[]) is
// preserved.
func deriveValueToGo(v lua.LValue, sentinel, emptyArr *lua.LUserData) (any, error) {
	if ud, ok := v.(*lua.LUserData); ok {
		switch ud {
		case sentinel:
			return nil, nil // the tombstone: an explicit RFC-7386 delete
		case emptyArr:
			return []any{}, nil // a distinctly-typed empty array (JSON [], not {})
		default:
			return nil, fmt.Errorf("luahook: derive produced unexpected userdata")
		}
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return luaToGo(v)
	}
	return deriveNestedTableToGo(tbl, sentinel, emptyArr)
}

// deriveNestedTableToGo mirrors luaTableToGo's array/object discrimination while
// honoring the sentinels in values. A sentinel cannot appear as an array ELEMENT
// (the config model never puts nulls/empty-arrays inside arrays — marshal.go), so
// array elements go through the normal luaToGo; object values go through
// deriveValueToGo.
func deriveNestedTableToGo(tbl *lua.LTable, sentinel, emptyArr *lua.LUserData) (any, error) {
	strKeys := map[string]lua.LValue{}
	intKeys := map[int]lua.LValue{}
	otherKey := false
	tbl.ForEach(func(k, val lua.LValue) {
		switch key := k.(type) {
		case lua.LString:
			strKeys[string(key)] = val
		case lua.LNumber:
			f := float64(key)
			if i := int(f); float64(i) == f {
				intKeys[i] = val
			} else {
				otherKey = true
			}
		default:
			otherKey = true
		}
	})
	if len(strKeys) == 0 && !otherKey && len(intKeys) > 0 && contiguousFrom1(intKeys) {
		outArr := make([]any, len(intKeys))
		for i := 1; i <= len(intKeys); i++ {
			gv, err := luaToGo(intKeys[i]) // array elements: no sentinel
			if err != nil {
				return nil, err
			}
			outArr[i-1] = gv
		}
		return outArr, nil
	}
	outObj := map[string]any{}
	for k, val := range strKeys {
		gv, err := deriveValueToGo(val, sentinel, emptyArr)
		if err != nil {
			return nil, err
		}
		outObj[k] = gv
	}
	return outObj, nil
}
