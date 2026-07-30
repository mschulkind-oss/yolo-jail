package luahook

// derive.go is the PRODUCER half of the Lua slot, distinct from the mutator
// (transform) half in vm.go.
//
// A `transform` runs POST-merge: it receives ctx.config (the composed surface)
// and mutates it. A `derive` runs PRE-merge: it receives the live config tables
// (mcp_servers, lsp_servers) and RETURNS a fresh object — the computed layer that
// feeds Inputs.Computed, exactly where manifest.BuildComputed's output flowed
// before (packsurfaces.go). Same pipeline slot, so no merge-order change; the
// only new thing is that the reshape is now sandboxed Lua instead of the
// computed[]/project op DSL (pack-declaration-reform §3.3, OQ1).
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

// tombstoneName is the registry key under which the sentinel userdata is stored,
// so goToLua/luaToGo on the derive path can recognize it by identity.
const tombstoneName = "yolo_tombstone_sentinel"

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
	// recognized by identity when marshalling back.
	sentinel := L.NewUserData()
	L.SetGlobal(tombstoneName, sentinel)

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
	L.SetGlobal("yolo", yolo)

	ctxTable, err := buildDeriveCtxTable(L, ctx, sentinel)
	if err != nil {
		return nil, err
	}
	L.SetGlobal("ctx", ctxTable)

	if err := L.DoString(script); err != nil {
		return nil, wrapLuaErr(err)
	}

	fn, ok := derives[key{ctx.Agent, ctx.Surface}]
	if !ok {
		// No derive registered for this surface — the identity (no computed layer),
		// exactly like a surface with no computed[] declarations.
		return nil, nil
	}
	if err := L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, ctxTable); err != nil {
		return nil, wrapLuaErr(err)
	}
	ret := L.Get(-1)
	L.Pop(1)

	tbl, isTable := ret.(*lua.LTable)
	if !isTable {
		return nil, fmt.Errorf("luahook: derive for %s/%s returned %s, want a table (the computed layer)",
			ctx.Agent, ctx.Surface, ret.Type())
	}
	out, err := deriveTableToGo(tbl, sentinel)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildDeriveCtxTable exposes the derive inputs: ctx.tombstone (the sentinel),
// ctx.agent / ctx.surface, and one read-only table per live source
// (ctx.mcp_servers, ctx.lsp_servers).
func buildDeriveCtxTable(L *lua.LState, ctx *DeriveCtx, sentinel *lua.LUserData) (*lua.LTable, error) {
	t := L.NewTable()
	L.SetField(t, "tombstone", sentinel)
	L.SetField(t, "agent", lua.LString(ctx.Agent))
	L.SetField(t, "surface", lua.LString(ctx.Surface))
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

// knownDeriveSources are the live-table names exposed to a derive as ctx.<name>.
// Kept in step with manifest's Source* constants by the caller (which builds the
// Tables map from exactly these); listed here so a derive always sees every
// source as at least an empty table, never a nil index.
var knownDeriveSources = []string{"mcp_servers", "lsp_servers"}

// deriveTableToGo is luaTableToGo specialized for the derive return: a value
// equal to the tombstone sentinel decodes to Go nil (the RFC-7386 delete marker
// the computed layer uses), instead of being dropped as Lua nil would be.
func deriveTableToGo(tbl *lua.LTable, sentinel *lua.LUserData) (map[string]any, error) {
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
		gv, err := deriveValueToGo(v, sentinel)
		if err != nil {
			iterErr = err
			return
		}
		out[string(ks)] = gv
	})
	return out, iterErr
}

// deriveValueToGo converts one Lua value, mapping the tombstone sentinel to Go
// nil and recursing into tables so a nested tombstone (a false flag under an
// object key, e.g. enabledPlugins) is preserved.
func deriveValueToGo(v lua.LValue, sentinel *lua.LUserData) (any, error) {
	if ud, ok := v.(*lua.LUserData); ok {
		if ud == sentinel {
			return nil, nil // the tombstone: an explicit RFC-7386 delete
		}
		return nil, fmt.Errorf("luahook: derive produced unexpected userdata")
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return luaToGo(v)
	}
	// A nested table may itself be an array or an object; reuse the array/object
	// discrimination from luaTableToGo, but with tombstone-aware values. The
	// simplest faithful approach: detect a pure 1..n array, else object, mirroring
	// luaTableToGo, converting each element through deriveValueToGo.
	return deriveNestedTableToGo(tbl, sentinel)
}

// deriveNestedTableToGo mirrors luaTableToGo's array/object discrimination while
// honoring the tombstone sentinel in values. A tombstone cannot appear as an
// array ELEMENT (Lua arrays cannot hold the sentinel meaningfully and the config
// model never puts nulls in arrays — marshal.go), so array elements go through
// the normal luaToGo; object values go through deriveValueToGo.
func deriveNestedTableToGo(tbl *lua.LTable, sentinel *lua.LUserData) (any, error) {
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
			gv, err := luaToGo(intKeys[i]) // array elements: no tombstone
			if err != nil {
				return nil, err
			}
			outArr[i-1] = gv
		}
		return outArr, nil
	}
	outObj := map[string]any{}
	for k, val := range strKeys {
		gv, err := deriveValueToGo(val, sentinel)
		if err != nil {
			return nil, err
		}
		outObj[k] = gv
	}
	return outObj, nil
}
