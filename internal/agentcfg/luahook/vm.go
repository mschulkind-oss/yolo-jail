package luahook

import (
	"context"
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// GopherLuaVM is the real, gopher-lua-backed implementation of the LuaVM
// interface (docs/plans/agent-settings-composition.md §3.4, §8 A.2). It is a
// pure-Go, cgo-free Lua 5.1 VM locked down to the sandbox contract in
// sandbox.go:
//
//   - a FRESH *lua.LState per Run (no shared mutable state between transforms —
//     determinism, §3.4);
//   - the sandbox is built by allowlist-subtraction: only the safe base subset
//   - string/table/math libs are opened, then every name in ForbiddenGlobals
//     (plus the fenv/module escape hatches) is deleted, so os/io/require/
//     package/load*/dofile/collectgarbage are simply absent;
//   - ctx is exposed as a Lua table per §3.2 (ctx.config mutable, ctx.managed a
//     read-only proxy, ctx.stage with exclude(), ctx.agent, ctx.surface);
//   - after the registered hook for ctx.Agent runs, ctx.config is marshalled
//     back into ctx.Config (map[string]any);
//   - any Lua error — a compile error, a runtime error, a stripped-global call,
//     or the instruction-budget timeout that catches an infinite loop —
//     surfaces as a non-nil Go error carrying file/line/message (fail-closed,
//     §3.4).
type GopherLuaVM struct {
	// Timeout bounds a single Run so an infinite loop (`while true do end`)
	// surfaces as a Go error instead of hanging the render. Zero means
	// DefaultTimeout. gopher-lua checks the context between VM instructions
	// (mainLoopWithContext), so a tight pure-Lua loop is interrupted.
	Timeout time.Duration
}

// DefaultTimeout is the per-Run wall-clock budget when GopherLuaVM.Timeout is
// zero. A config transform is expected to finish in microseconds; a multi-second
// budget only exists to convert a runaway loop into a loud error.
const DefaultTimeout = 5 * time.Second

// extraStrippedGlobals are names not in ForbiddenGlobals that the base library
// still installs and that could weaken the sandbox (function-environment
// manipulation, the module loader, the debug-ish proxy). Removed alongside
// ForbiddenGlobals so the environment matches AllowedGlobals.
var extraStrippedGlobals = []string{
	"require", "module", // loadlib entries planted in _G by OpenBase
	"getfenv", "setfenv", // reassign a function's environment → escape
	"newproxy",   // hidden proxy/userdata builder
	"_printregs", // gopher-lua debug hook
	"print",      // side-effecting I/O; a pure transform has no console
	"dostring",   // belt-and-suspenders (not a stock name, but listed forbidden)
}

// Run implements LuaVM. It builds a fresh sandboxed LState, registers the
// transform hooks the script declares via yolo.transform(agent, fn), invokes
// the hook matching ctx.Agent with the ctx table, and marshals the mutated
// ctx.config back into ctx.Config. Any Lua error is returned as a Go error and
// ctx.Config is left as the caller passed it (Apply discards ctx on error).
func (vm GopherLuaVM) Run(script string, ctx *Ctx) error {
	if ctx == nil {
		return fmt.Errorf("luahook: nil ctx")
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
		return err
	}

	// hooks accumulates the per-agent transforms the script registers.
	hooks := map[string]*lua.LFunction{}
	registerYoloTable(L, hooks)

	ctxTable, err := buildCtxTable(L, ctx)
	if err != nil {
		return err
	}
	L.SetGlobal("ctx", ctxTable)

	// Run the registration script (defines yolo.transform(...) calls).
	if err := L.DoString(script); err != nil {
		return wrapLuaErr(err)
	}

	// Invoke the hook for this surface's agent, if the script registered one.
	// A script that registers only other agents is a no-op here (§3.4: another
	// agent's registration simply doesn't fire).
	hook, ok := hooks[ctx.Agent]
	if !ok {
		return nil
	}
	if err := L.CallByParam(lua.P{Fn: hook, NRet: 0, Protect: true}, ctxTable); err != nil {
		return wrapLuaErr(err)
	}

	// Marshal the (possibly mutated or wholesale-replaced) ctx.config back to
	// Go. The hook may mutate the table in place or reassign ctx.config, so we
	// re-read the field rather than trusting the original table identity.
	cfgLV := L.GetField(ctxTable, "config")
	cfg, err := luaToGo(cfgLV)
	if err != nil {
		return err
	}
	cfgMap, ok := cfg.(map[string]any)
	if !ok {
		return fmt.Errorf("luahook: transform left ctx.config as %T, want an object/table", cfg)
	}
	ctx.Config = cfgMap
	return nil
}

// openSandboxLibs opens only the safe libraries (base, string, table, math)
// and then strips every forbidden global, realizing the AllowedGlobals surface
// by subtraction. Base must load before the others (gopher-lua requires base/
// load first) and is where the dangerous names live, so it is opened then
// pruned.
func openSandboxLibs(L *lua.LState) error {
	// Order matters: base first. package/os/io/debug/coroutine/channel are
	// intentionally NOT opened.
	safeLibs := []struct {
		name string
		open lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	}
	for _, lib := range safeLibs {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		if err := L.PCall(1, 0, nil); err != nil {
			return fmt.Errorf("luahook: opening sandbox lib %q: %w", lib.name, err)
		}
	}
	for _, name := range ForbiddenGlobals {
		L.SetGlobal(name, lua.LNil)
	}
	for _, name := range extraStrippedGlobals {
		L.SetGlobal(name, lua.LNil)
	}
	return nil
}

// registerYoloTable installs the global `yolo` table with its single method,
// transform(agent, fn), which records fn keyed by agent into hooks. This is the
// only yolo-provided entry point (§3.2: "No helper library").
func registerYoloTable(L *lua.LState, hooks map[string]*lua.LFunction) {
	yolo := L.NewTable()
	L.SetField(yolo, "transform", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		fn := L.CheckFunction(2)
		hooks[agent] = fn
		return 0
	}))
	L.SetGlobal("yolo", yolo)
}

// buildCtxTable constructs the Lua-side ctx table (§3.2): config (mutable),
// managed (read-only proxy), stage (exclude closure), agent, surface.
func buildCtxTable(L *lua.LState, ctx *Ctx) (*lua.LTable, error) {
	t := L.NewTable()

	configLV, err := goToLua(L, mapAsAny(ctx.Config))
	if err != nil {
		return nil, fmt.Errorf("luahook: marshalling ctx.config: %w", err)
	}
	L.SetField(t, "config", configLV)

	managed, err := readOnlyManaged(L, ctx.Managed)
	if err != nil {
		return nil, fmt.Errorf("luahook: marshalling ctx.managed: %w", err)
	}
	L.SetField(t, "managed", managed)

	L.SetField(t, "stage", buildStageTable(L, ctx.Stage))
	L.SetField(t, "agent", lua.LString(ctx.Agent))
	L.SetField(t, "surface", lua.LString(ctx.Surface))
	return t, nil
}

// mapAsAny normalizes a nil map to an empty one so ctx.config is always a
// table on the Lua side.
func mapAsAny(m map[string]any) any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// readOnlyManaged builds a read-only proxy over the managed layer (§3.2:
// "read-only view of the keys the jail will enforce"). The real data lives in a
// backing table exposed via a metatable __index; __newindex raises, so any
// attempt to mutate ctx.managed is a loud Lua error rather than a silent write
// that would (harmlessly, since managed is never marshalled back) mislead the
// author. This is the VM-native form of the deep-copy guarantee documented on
// Ctx.Managed.
func readOnlyManaged(L *lua.LState, managed map[string]any) (*lua.LTable, error) {
	backingLV, err := goToLua(L, mapAsAny(managed))
	if err != nil {
		return nil, err
	}
	backing := backingLV.(*lua.LTable)

	proxy := L.NewTable()
	mt := L.NewTable()
	L.SetField(mt, "__index", backing)
	L.SetField(mt, "__newindex", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("ctx.managed is read-only (yolo enforces these keys after the transform)")
		return 0
	}))
	// Lock the metatable so a transform can't swap it out (getmetatable
	// returns this string; setmetatable errors).
	L.SetField(mt, "__metatable", lua.LString("locked"))
	L.SetMetatable(proxy, mt)
	return proxy, nil
}

// buildStageTable exposes ctx.stage.exclude(glob) (§3.2 / §6.5). Each call
// records a glob on the Go-side Stage; there is no other stage surface for a
// structured codec. The Go Stage is captured in the closure, so excludes accrue
// on the caller's ctx.Stage and are readable after Run via Stage.Excluded().
func buildStageTable(L *lua.LState, stage *Stage) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "exclude", L.NewFunction(func(L *lua.LState) int {
		glob := L.CheckString(1)
		stage.Exclude(glob)
		return 0
	}))
	return t
}

// wrapLuaErr converts a gopher-lua error into a Go error. A *lua.ApiError
// already carries the "<source>:<line>: <message>" string in its Object, which
// is what §3.4 requires (file/line/message); we surface it as-is so callers see
// the location. Other errors (context timeout wrapped as ApiError, plain Go
// errors) pass through unchanged.
func wrapLuaErr(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := err.(*lua.ApiError); ok {
		return fmt.Errorf("lua transform error: %s", apiErr.Object.String())
	}
	return fmt.Errorf("lua transform error: %w", err)
}
