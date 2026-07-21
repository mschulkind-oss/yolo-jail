// Package luahook implements the config-composition Lua transform sandbox
// described in docs/plans/agent-settings-composition.md §3 (the "Lua transform
// — the abstraction"). It is a leaf library with NO callers yet (Phase A of the
// build in §8); the surfaces in Phase B call it. It provides:
//
//  1. the ctx bridge — the decoded value + handles a transform sees
//     (§3.2: ctx.config / ctx.stage / ctx.managed / ctx.agent / ctx.surface);
//  2. the sandbox contract — the guarantees a transform runs under
//     (§3.4 / §9: no os/io/require/network/filesystem, pure function of its
//     inputs, deterministic, and a Lua error surfaces as a loud Go error); and
//  3. GopherLuaVM (vm.go) — the real, github.com/yuin/gopher-lua-backed
//     implementation of the LuaVM interface. gopher-lua is a pure-Go, cgo-free
//     Lua 5.1 VM (§3.4, §8 A.2), vendored so the hermetic nix image build works
//     offline.
//
// # The LuaVM interface
//
// The VM boundary is a one-method interface (LuaVM) so the pipeline and its
// tests can depend on the contract rather than gopher-lua directly. GopherLuaVM
// is the production implementation; the tests also keep a hand-rolled fakeVM
// that exercises the same contract without real Lua, and add GopherLuaVM tests
// (vm_test.go) that prove the sandbox end to end (forbidden globals absent,
// fail-closed on error/timeout, ctx.managed read-only, list/nested round-trip).
package luahook

import "fmt"

// LuaVM is the boundary between yolo and the sandboxed Lua interpreter.
//
// A conforming implementation MUST honor the sandbox contract (see sandbox.go
// and the package doc): the script runs in a locked-down environment with no
// access to os, io, require, package, load/loadfile/dofile, network, or the
// filesystem; the ONLY channel in or out is the *Ctx handed to Run. The script
// registers one hook per surface via `yolo.transform(agent, fn)`; Run selects
// and invokes the hook whose agent matches ctx.Agent, passing ctx as a Lua
// table (ctx.config mutable, ctx.managed read-only, ctx.stage a handle,
// ctx.agent/ctx.surface strings). A Lua runtime error MUST be returned as a
// non-nil Go error — never a silent partial transform (§3.4 fail-closed).
//
// Run mutates ctx.Config in place (the transform's edits are read back by
// Apply). Implementations MUST be deterministic: given the same script and the
// same ctx inputs they produce the same ctx.Config and the same stage excludes,
// so the overlay diff in §5 stays stable.
type LuaVM interface {
	// Run executes script against ctx, invoking the registered transform for
	// ctx.Agent. It returns a non-nil error on any Lua error (compile or
	// runtime) and leaves ctx.Config untouched-or-partial only when it also
	// returns an error (callers discard ctx on error — see Apply).
	Run(script string, ctx *Ctx) error
}

// Transform pairs a LuaVM with the script text that registers the per-surface
// hooks (the two auto-loaded config.lua files in §3.4, already concatenated in
// user-then-workspace order by the caller). It is the unit Apply runs.
type Transform struct {
	// VM is the sandboxed interpreter. Required.
	VM LuaVM
	// Script is the Lua source that calls yolo.transform(agent, fn). An empty
	// script is the identity transform (§3.4: neither config.lua present →
	// pass-through).
	Script string
}

// Ctx is the bridge value a transform receives (§3.2). yolo does the decode and
// hands the hook a plain decoded table plus a few handles; the transform reads
// and mutates it, and yolo re-encodes the result. The field mapping to the Lua
// side is 1:1 with §3.2:
//
//	Config  -> ctx.config   (the composed, decoded config; read + MUTATE)
//	Stage   -> ctx.stage    (file-tree staging handle; exclude by glob)
//	Managed -> ctx.managed  (READ-ONLY view of yolo's enforced keys)
//	Agent   -> ctx.agent    ("pi" | "claude" | …)
//	Surface -> ctx.surface  ("settings" | "config" | …)
type Ctx struct {
	// Config is the fully-composed config decoded to a generic map
	// (defaults+host+workspace+overlay already deep-merged — §3.1). The
	// transform mutates this in place; the mutated value is what Apply returns
	// and what yolo re-encodes. Structured codecs (json/toml/yaml) decode to
	// map[string]any here; the raw codec is out of scope for this spike.
	Config map[string]any

	// Managed is a READ-ONLY view of the keys yolo enforces regardless (§3.1,
	// §4 "managed" layer). The transform may INSPECT it (e.g. to avoid
	// clobbering a key yolo will overwrite anyway) but any write to it is a
	// no-op against the enforced layer: yolo re-applies the enforced keys AFTER
	// the hook (see Enforce). To make that guarantee concrete on the Go side —
	// where maps are references — Managed is a defensive DEEP COPY of the
	// enforced layer, so a transform that assigns into it cannot reach the
	// bytes Enforce writes. (The gopher-lua impl instead exposes it via a
	// read-only metatable; same guarantee, VM-native.)
	Managed map[string]any

	// Stage is the file-tree staging handle (§3.2/§3.3 tree surfaces). The
	// transform calls Stage.Exclude(glob) to keep files out of the jail tree.
	Stage *Stage

	// Agent is the surface's agent identifier ("pi", "claude", …). (ctx.agent)
	Agent string
	// Surface is the file identifier within the agent ("settings", "config",
	// …). (ctx.surface)
	Surface string

	// enforced is the ORIGINAL enforced layer, never exposed to the transform.
	// Enforce applies it over Config after the hook runs (§3.1 "managed keys
	// win, applied AFTER Lua"). Kept private so the read-only guarantee on
	// Managed cannot be defeated from Lua.
	enforced map[string]any
}

// NewCtx builds a Ctx for one surface. config is the merged, decoded config the
// transform will mutate (taken by reference — the caller's map is the one
// mutated and returned). managed is the enforced layer; NewCtx keeps the
// original privately for Enforce and exposes only a deep copy as ctx.Managed,
// so the read-only contract holds even though Go maps are references.
func NewCtx(agent, surface string, config, managed map[string]any) *Ctx {
	if config == nil {
		config = map[string]any{}
	}
	return &Ctx{
		Config:   config,
		Managed:  deepCopyMap(managed),
		Stage:    &Stage{},
		Agent:    agent,
		Surface:  surface,
		enforced: managed,
	}
}

// Enforce re-applies the enforced (managed) layer over Config, managed keys
// winning (§3.1 enforce step, run AFTER the Lua hook). It uses the ORIGINAL
// enforced layer captured in NewCtx, not the Managed view the transform could
// have scribbled on — that is what makes ctx.managed effectively read-only.
//
// The merge is DEEP: a managed OBJECT merges key-by-key into the existing
// Config object rather than replacing it wholesale, so host/transform siblings
// under the same top-level key survive (e.g. a host `permissions.ask` is kept
// while yolo forces `permissions.allow`). A managed scalar/array still replaces.
// This closes the "shallow-Enforce subtree clobber" fidelity gap the Phase B
// surfaces documented (claude/gemini managed nested objects). Managed values are
// deep-copied in, so Config never shares mutable structure with the enforced
// layer.
func (c *Ctx) Enforce() {
	for k, v := range c.enforced {
		c.Config[k] = enforceValue(c.Config[k], v)
	}
}

// enforceValue merges an enforced value over the current one, managed winning.
// Two objects merge recursively (so siblings survive); anything else — a scalar,
// an array, or a type mismatch — is replaced by a deep copy of the managed value.
func enforceValue(cur, managed any) any {
	mMap, mIsObj := managed.(map[string]any)
	cMap, cIsObj := cur.(map[string]any)
	if !mIsObj || !cIsObj {
		return deepCopyValue(managed)
	}
	out := make(map[string]any, len(cMap)+len(mMap))
	for k, v := range cMap {
		out[k] = v
	}
	for k, v := range mMap {
		out[k] = enforceValue(cMap[k], v)
	}
	return out
}

// Apply runs one transform over ctx and returns the mutated config, or an
// error. It is the §3.1 pipeline's transform step. On a VM error it returns a
// nil map and a wrapped error (fail-closed, §3.4 "loud failure") — callers keep
// the last good render rather than shipping a half-transformed file. Apply does
// NOT run Enforce; the caller applies the managed layer after (§3.1), which the
// tests exercise explicitly.
func Apply(t Transform, ctx *Ctx) (map[string]any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("luahook: nil ctx")
	}
	if t.Script == "" {
		// Identity transform — no config.lua registered (§3.4 pass-through).
		return ctx.Config, nil
	}
	if t.VM == nil {
		return nil, fmt.Errorf("luahook: transform has a script but no LuaVM")
	}
	if err := t.VM.Run(t.Script, ctx); err != nil {
		return nil, fmt.Errorf("luahook: transform failed for agent %q surface %q: %w", ctx.Agent, ctx.Surface, err)
	}
	return ctx.Config, nil
}

// Stage is the file-tree staging handle (ctx.stage in §3.2). For this spike it
// records exclude globs; the engine consumes Excluded() to prune the staged
// tree. Include-by-default, exclude-by-glob matches §3.3 tree surfaces and the
// §6.5 `ctx.stage.exclude("extensions/permission-gate.ts")` call.
type Stage struct {
	excluded []string
}

// Exclude keeps files matching the relative-path glob out of the jail tree
// (§3.2). Called by the transform; order-preserving, dedupe left to the engine.
func (s *Stage) Exclude(glob string) {
	s.excluded = append(s.excluded, glob)
}

// Excluded returns the globs the transform asked to drop, in call order.
func (s *Stage) Excluded() []string {
	return s.excluded
}

// deepCopyMap returns a deep copy of m (maps/slices cloned, scalars copied), so
// the returned value shares no mutable structure with m. nil in -> nil out.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue deep-copies the decoded-config value shapes: map[string]any,
// []any, and scalars. Unknown types are returned as-is (treated as immutable).
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
