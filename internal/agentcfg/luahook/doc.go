// Package luahook is the sandboxed Lua interpreter yolo runs a pack's
// derive.lua in — a PRODUCER of config values, never an effect
// (docs/reference/pack-system.md §7, docs/reference/providers.md).
//
// A derive runs PRE-merge: it receives the live config tables (mcp_servers,
// lsp_servers), the active profile and the pack's own declarations, and RETURNS
// a fresh object that becomes the composed surface's `computed` layer
// (agentcfg.Inputs.Computed, filled by entrypoint/packsurfaces.go). Six shipped
// packs use it — agy, claude, codex, copilot, opencode, pi. What a derive
// registers, what it may read, and the tombstone sentinel are all in derive.go;
// this doc covers what the package is and the boundary it runs behind.
//
// The package provides:
//
//  1. GopherLuaVM (vm.go) — a github.com/yuin/gopher-lua-backed Lua 5.1 VM.
//     Pure Go and cgo-free, vendored so the hermetic nix image build works
//     offline. Derive and DeriveRegistrations are its methods.
//  2. the sandbox contract (sandbox.go, openSandboxLibs) — the guarantees a
//     script runs under: no os/io/require/network/filesystem, a pure function of
//     its inputs, and a Lua error surfacing as a loud Go error with file and
//     line.
//  3. the marshallers (marshal.go) — the decoded-value model in and out.
//
// # It used to be two halves
//
// Until docs/design/lua-transform-removal.md this package also held the CONFIG
// TRANSFORM: a user-authored ~/.config/yolo-jail/config.lua or
// <workspace>/yolo-jail.config.lua that ran POST-merge and mutated the composed
// surface, reached through an LuaVM interface, a Ctx bridge, a Stage handle and
// Apply. That feature had no user, its motivating case was met declaratively by
// the `autonomy` contribution kind, and its determinism requirement was stated
// in a doc and enforced by nothing. It is gone, and the derive half is now the
// package's whole identity.
//
// Two things survived the split and are worth knowing, because they read as
// transform leftovers and are not:
//
//   - wrapLuaErr still prefixes "lua transform error:". The word is wrong and
//     the string is quoted by derive's own tests; renaming it is a wording
//     choice, not a behaviour one.
//   - the sandbox opens Lua's `math` library whole, and neither ForbiddenGlobals
//     nor extraStrippedGlobals names math.random. A derive.lua is required to be
//     a pure function and can still call it (measured 2026-09-10). That is a real
//     finding, named as out of scope by the removal doc (§11), and the fix is one
//     line in extraStrippedGlobals plus a test.
package luahook
