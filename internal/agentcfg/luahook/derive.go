package luahook

// Derive registrations and ctx: docs/reference/providers.md

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
	"strings"

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
	// with the profile's own values over them — docs/reference/providers.md),
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

	// UnknownAPI, when non-nil, makes an unknown `yolo.<name>` member TOLERATED
	// instead of fatal, and is the callback that reports each one (once per name per
	// Derive call, whatever a script does with it). Nil — the zero value — is STRICT:
	// reading an unknown member raises a Lua error that names it.
	//
	// The field carries the switch and the reporting channel together on purpose: a
	// tolerated skip that nobody hears is the one outcome the skew rules forbid
	// (packdecl.DecodeTolerant), so there is no way to ask for tolerance without also
	// saying where the note goes.
	//
	// WHY THIS EXISTS. The set of `yolo.*` functions is a VERSION BOUNDARY exactly like
	// the manifest vocabulary is, and it went un-noticed as one until it broke a jail.
	// The in-jail entrypoint executes a derive.lua the HOST staged, and the two halves
	// deploy on different cadences (AGENTS.md, "Build & deploy"), so a host newer than
	// the baked image stages a script calling an API that build has never registered.
	// Adding yolo.env (f55f2109) did precisely that: every jail on a pre-f55f2109 image
	// died at boot with
	//
	//	surface claude/config: derive: lua transform error:
	//	    <string>:51: attempt to call a non-function object
	//
	// — line 51 being packs/claude/derive.lua's `yolo.env("claude", …)`. The failure is
	// worse than it looks in two ways. The whole script is executed to REGISTER its
	// producers, so one unknown call at the top level takes down every surface the
	// script serves, not just the one it belongs to (both claude/config and
	// claude/settings failed, at lines 4 and 26, over a call at line 51). And in the
	// jail the yolo.env registration is INERT anyway — the entrypoint never invokes the
	// env producer (packload/deriveenv.go: host-side only) — so the boot died over an
	// API surface it must EXPOSE but does not USE. Tolerating the read costs the jail
	// nothing it was going to render.
	//
	// STRICT STAYS THE ZERO VALUE so tolerance is something a caller ASKS for at a
	// boundary it can name, the way packdecl.Decode stays strict beside DecodeTolerant.
	// Of the three production readers, one takes it: the host-side env composition
	// (packload.AgentEnv) refuses an unknown member, and both paths that render a
	// SURFACE — the jail's boot loop and `yolo check`'s dry run, which share
	// deriveComputedLayer — tolerate and report it.
	UnknownAPI func(name string)
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
	guardUnknownAPI(L, yolo, ctx.UnknownAPI)
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

// guardUnknownAPI installs the `yolo` table's __index, which decides what reading a
// member this build never registered means. It is the ONLY thing standing between a
// pack script written for a newer yolo and gopher-lua's "attempt to call a non-function
// object" — see DeriveCtx.UnknownAPI for the boot that error cost.
//
// __index fires only for MISSING keys: every registered API is a raw field on the table,
// so the guard cannot shadow, intercept or slow down a call that resolves normally.
//
// report == nil is STRICT: raise, naming the member and everything this build does have.
// The list is READ OFF THE TABLE rather than written out here, so an API added tomorrow
// appears in the message without anyone remembering to add it — a hardcoded list would be
// wrong in exactly the situation this message is read in.
//
// report != nil is TOLERANT: the read yields a stub that accepts any arguments and does
// nothing, which is the right answer for a REGISTRATION (the producer is simply never
// registered, and an unregistered producer is already the identity — see Derive's !ok
// branch). Each name is reported at most once however many times the script touches it;
// a script calling an unknown API in a loop is one finding, not a thousand lines.
func guardUnknownAPI(L *lua.LState, yolo *lua.LTable, report func(name string)) {
	var known []string
	yolo.ForEach(func(k, _ lua.LValue) {
		if ks, ok := k.(lua.LString); ok {
			known = append(known, "yolo."+string(ks))
		}
	})
	sort.Strings(known)

	seen := map[string]bool{}
	mt := L.NewTable()
	L.SetField(mt, "__index", L.NewFunction(func(L *lua.LState) int {
		name := L.Get(2).String()
		if report == nil {
			L.RaiseError("yolo.%s is not an API this build of yolo provides (it has: %s) — "+
				"either this derive.lua was written for a newer yolo than the one running it "+
				"(version skew), or the name is a typo", name, strings.Join(known, ", "))
			return 0
		}
		if !seen[name] {
			seen[name] = true
			report(name)
		}
		L.Push(L.NewFunction(func(*lua.LState) int { return 0 }))
		return 1
	}))
	// Locked, like ctx.managed's: a script must not be able to swap the guard out and
	// turn a reported skip back into the opaque failure it replaces.
	L.SetField(mt, "__metatable", lua.LString("locked"))
	L.SetMetatable(yolo, mt)
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
