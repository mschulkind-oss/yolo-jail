package luahook

import (
	"fmt"
	"sort"
	"strings"
)

// The sandbox contract (docs/plans/agent-settings-composition.md §3.4, §9
// "Sandbox is mandatory").
//
// A transform is arbitrary, unvalidated user code, so it runs in a locked-down
// pure-Go Lua VM with:
//
//   - NO os / io / require / package / net / filesystem access — the only
//     channel in or out is the *Ctx handles (§3.4).
//   - NO code-loading escape hatch (load / loadstring / loadfile / dofile /
//     dostring) — those reach back into a fresh, unrestricted global env.
//   - It is a PURE FUNCTION of its inputs: given the same script + ctx it
//     mutates ctx identically and returns the same result (§3.4/§9). No wall
//     clock, no randomness, no I/O — a non-deterministic transform breaks the
//     §5 overlay diff.
//   - A Lua error (typo, nil index, calling a stripped global) is a LOUD Go
//     error with file/line/message — never a silent partial config (§3.4
//     fail-closed).
//
// This file encodes the contract two ways: ForbiddenGlobals is the concrete
// allowlist-by-subtraction the gopher-lua impl must apply to the sandbox
// environment, and ValidateSandbox is a static best-effort lint that rejects an
// obviously escaping script BEFORE it runs (defense in depth; the VM env-strip
// is the real boundary, not this text scan).

// ForbiddenGlobals is the set of Lua globals a conforming LuaVM MUST remove (or
// never install) in the sandbox environment. The gopher-lua impl builds its
// environment by loading only the safe base/string/table/math libs and then
// deleting these names (§3.4: "no os, io, require, network, or filesystem
// beyond the ctx handles").
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
	// Coroutine-based reentry is not needed by a pure transform and keeps the
	// determinism surface small.
	"collectgarbage",
}

// AllowedGlobals is the positive companion: the ONLY top-level names a transform
// may rely on besides `yolo` (the registration table) and `ctx` (the bridge).
// These are the deterministic, side-effect-free stock-Lua libs the §3.2 example
// uses (string.find, ipairs, table indexing). Documented here so the gopher-lua
// impl and the fixture corpus agree on the surface.
var AllowedGlobals = []string{
	// Registration + bridge (yolo-provided, not stock Lua).
	"yolo", "ctx",
	// Pure stock-Lua the transforms use.
	"string", "table", "math",
	"ipairs", "pairs", "next", "select",
	"type", "tostring", "tonumber",
	"pcall", "error", "assert",
}

// ValidateSandbox is a static, best-effort lint over the transform source that
// rejects a script referencing a forbidden global before it is handed to the
// VM. It is DEFENSE IN DEPTH, not the boundary: the boundary is the VM's
// stripped environment (a stripped global simply errors at runtime as a loud Go
// error per §3.4). This catches the common footgun early with a clearer message
// than a runtime nil-call, and documents intent in code. It is intentionally
// conservative: it flags a forbidden name used as an identifier token and does
// not attempt to parse Lua (a false positive is cheap here — a transform has no
// legitimate reason to name `os`/`io`/etc.).
func ValidateSandbox(script string) error {
	var found []string
	seen := map[string]bool{}
	for _, name := range ForbiddenGlobals {
		if seen[name] {
			continue
		}
		if usesIdentifier(script, name) {
			found = append(found, name)
			seen[name] = true
		}
	}
	if len(found) > 0 {
		sort.Strings(found)
		return fmt.Errorf("luahook: transform references forbidden global(s) %s — the sandbox forbids os/io/require/network/filesystem access (§3.4)", strings.Join(found, ", "))
	}
	return nil
}

// usesIdentifier reports whether name appears in src as a standalone identifier
// token (not a substring of a longer identifier, not inside a comment-stripped
// context). Lua identifiers are [A-Za-z_][A-Za-z0-9_]*; we treat a match as a
// use when neither neighbor is an identifier char. This over-approximates
// (matches inside strings too), which is fine for a fail-closed lint.
func usesIdentifier(src, name string) bool {
	for i := 0; ; {
		idx := strings.Index(src[i:], name)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(name)
		leftOK := start == 0 || !isIdentByte(src[start-1])
		rightOK := end >= len(src) || !isIdentByte(src[end])
		if leftOK && rightOK {
			return true
		}
		i = end
	}
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
