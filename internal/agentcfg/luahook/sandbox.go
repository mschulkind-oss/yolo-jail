package luahook

// The sandbox contract (docs/reference/pack-system.md §7; the shape of it is
// docs/plans/agent-settings-composition.md §3.4, §9 "Sandbox is mandatory",
// written for the config transform that no longer exists and inherited whole by
// the derive path).
//
// A derive.lua is a pack's code, not yolo's, so it runs in a locked-down pure-Go
// Lua VM with:
//
//   - NO os / io / require / package / net / filesystem access — the only
//     channel in or out is the DeriveCtx the VM marshals.
//   - NO code-loading escape hatch (load / loadstring / loadfile / dofile /
//     dostring) — those reach back into a fresh, unrestricted global env.
//   - It is a PURE FUNCTION of its inputs: given the same script and ctx it
//     returns the same object. No wall clock, no I/O.
//
//     ⚠ Not enforced for randomness: openSandboxLibs opens Lua's `math` whole
//     and nothing below names math.random (measured 2026-09-10). The fix is one
//     line in extraStrippedGlobals; see the package doc.
//
//   - A Lua error (typo, nil index, calling a stripped global) is a LOUD Go
//     error with file/line/message — never a silently partial computed layer.
//
// ForbiddenGlobals below is the concrete allowlist-by-subtraction openSandboxLibs
// applies. It used to have a companion: AllowedGlobals, the positive list, and
// ValidateSandbox, a static lint that scanned a script for a forbidden name
// before running it. Both went with the config transform
// (docs/design/lua-transform-removal.md §4.1) — the lint had no production caller
// in its whole life, and the VM's stripped environment always was the boundary.

// ForbiddenGlobals is the set of Lua globals the sandbox environment MUST NOT
// have. openSandboxLibs builds its environment by loading only the safe
// base/string/table/math libs and then deleting these names ("no os, io,
// require, network, or filesystem").
var ForbiddenGlobals = []string{
	// Process / OS surface.
	"os",
	// I/O and filesystem.
	"io",
	// Module system — reaches the real filesystem and C loaders.
	"require",
	"package",
	// Code loaders — re-enter an unrestricted global env / read files.
	"load",
	"loadstring",
	"loadfile",
	"dofile",
	// Raw byte access to disk/stdio some VMs expose.
	"dostring",
	// Coroutine-based reentry is not needed by a pure producer and keeps the
	// determinism surface small.
	"collectgarbage",
}
