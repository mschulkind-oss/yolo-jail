package luahook

import (
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// GopherLuaVM is the gopher-lua-backed VM every derive.lua runs on
// (docs/reference/pack-system.md §7). It is a pure-Go, cgo-free Lua 5.1 VM
// locked down to the sandbox contract in sandbox.go:
//
//   - a FRESH *lua.LState per run (no shared mutable state between scripts —
//     determinism);
//   - the sandbox is built by allowlist-subtraction: only the safe base subset
//   - string/table/math libs are opened, then every name in ForbiddenGlobals
//     (plus the fenv/module escape hatches) is deleted, so os/io/require/
//     package/load*/dofile/collectgarbage are simply absent;
//   - any Lua error — a compile error, a runtime error, a stripped-global call,
//     or the instruction-budget timeout that catches an infinite loop —
//     surfaces as a non-nil Go error carrying file/line/message (fail-closed).
//
// Its methods are Derive and DeriveRegistrations (derive.go). This file holds
// what they are built out of: the type, the timeout, the sandbox construction,
// and the error wrapper.
type GopherLuaVM struct {
	// Timeout bounds a single run so an infinite loop (`while true do end`)
	// surfaces as a Go error instead of hanging the render. Zero means
	// DefaultTimeout. gopher-lua checks the context between VM instructions
	// (mainLoopWithContext), so a tight pure-Lua loop is interrupted.
	Timeout time.Duration
}

// DefaultTimeout is the per-run wall-clock budget when GopherLuaVM.Timeout is
// zero. A derive is expected to finish in microseconds; a multi-second budget
// only exists to convert a runaway loop into a loud error.
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
