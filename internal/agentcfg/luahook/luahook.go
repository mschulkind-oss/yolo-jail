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

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

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
	// Config is the fully-composed config decoded to the generic value model
	// (defaults+host+workspace+overlay already merged — §3.1). The transform
	// mutates it and the mutated value is what Apply returns and yolo re-encodes.
	//
	// Its Go type follows the surface's codec, which is why this is `any` and not
	// map[string]any: json/toml surfaces arrive as map[string]any, `lines` as
	// []any, and `raw` as a string. A transform on a raw surface is a perfectly
	// reasonable thing to write —
	//
	//	ctx.config = ctx.config:gsub("^#!/bin/sh", "#!/usr/bin/env bash")
	//
	// — and the marshaller and the sandbox always supported it (goToLua/luaToGo
	// round-trip scalars, and the sandbox opens Lua's `string` library). The only
	// thing that blocked it was this field's type plus a post-hook assertion in
	// vm.go that demanded a table back. Both now key off Kind instead.
	//
	// A hook must return the SAME kind it was handed; a raw transform returning a
	// table is a loud error, not a coercion (see Kind and vm.go).
	Config any

	// Kind is the shape Config must have — derived from the surface's codec by
	// the caller (see codec.KindOf). It is what makes the shape contract
	// checkable at the VM boundary: without it, "the hook returned a table" is
	// indistinguishable from "the hook returned the right thing" for a raw
	// surface. The zero value is KindObject, so an unset Kind behaves exactly like
	// the old object-only contract.
	Kind codec.Kind

	// Managed is a READ-ONLY view of the keys yolo enforces regardless (§3.1,
	// §4 "managed" layer). The transform may INSPECT it (e.g. to avoid
	// clobbering a key yolo will overwrite anyway) but any write to it is a
	// no-op against the enforced layer: yolo re-applies the enforced keys AFTER
	// the hook (see Enforce). To make that guarantee concrete on the Go side —
	// where maps are references — Managed is a defensive DEEP COPY of the
	// enforced layer, so a transform that assigns into it cannot reach the
	// bytes Enforce writes. (The gopher-lua impl instead exposes it via a
	// read-only metatable; same guarantee, VM-native.)
	//
	// Typed `any` for the same reason as Config: on a keyless surface (raw/lines)
	// there are no individual keys to enforce, so the managed layer is the
	// whole-file value. For an object surface this is always a map[string]any —
	// use ManagedMap to read it without an assertion at every site.
	Managed any

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
	enforced any
}

// ConfigMap returns Config as an object, or nil when the surface is keyless. A
// convenience for the object-surface callers and tests that dominate: it keeps
// `ctx.ConfigMap()["k"]` readable now that Config is `any`.
//
// Note it returns the LIVE map, not a copy, so mutating it mutates Config — that
// is what the object-surface transform code expects.
func (c *Ctx) ConfigMap() map[string]any {
	m, _ := c.Config.(map[string]any)
	return m
}

// ManagedMap returns Managed as an object, or nil when the surface is keyless.
// See ConfigMap.
func (c *Ctx) ManagedMap() map[string]any {
	m, _ := c.Managed.(map[string]any)
	return m
}

// NewCtx builds a Ctx for an OBJECT surface (json/toml) — the common case, kept
// as a map-typed convenience so the many existing call sites and tests read
// unchanged. config is taken by reference: the caller's map is the one mutated
// and returned. managed is the enforced layer; NewCtx keeps the original
// privately for Enforce and exposes only a deep copy as ctx.Managed, so the
// read-only contract holds even though Go maps are references.
//
// For a non-object surface use NewCtxKind.
func NewCtx(agent, surface string, config, managed map[string]any) *Ctx {
	if config == nil {
		config = map[string]any{}
	}
	return newCtx(agent, surface, codec.KindObject, config, managed)
}

// NewCtxKind builds a Ctx for a surface of any kind. config and managed are the
// generic value model for that kind (map[string]any for KindObject, []any for
// KindArray, string for KindScalar); a nil config becomes the kind's zero value,
// so "no layers at all" is an empty object / empty list / empty string rather
// than a nil that the VM would have to special-case.
//
// managed for a non-object surface is the WHOLE-FILE value: there are no keys to
// enforce individually, so a non-nil managed replaces the rendered value outright
// (see Enforce). That coarseness is inherent to a keyless format, not a
// limitation of this function.
func NewCtxKind(agent, surface string, kind codec.Kind, config, managed any) *Ctx {
	if config == nil {
		config = kind.ZeroValue()
	}
	return newCtx(agent, surface, kind, config, managed)
}

// newCtx is the shared constructor body. The managed deep copy goes through
// deepCopyValue (not deepCopyMap) so a non-object managed layer is copied too.
func newCtx(agent, surface string, kind codec.Kind, config, managed any) *Ctx {
	return &Ctx{
		Config:   config,
		Kind:     kind,
		Managed:  managedView(managed),
		Stage:    &Stage{},
		Agent:    agent,
		Surface:  surface,
		enforced: managed,
	}
}

// managedView returns the ctx.managed value the transform may inspect: a deep
// copy, so writes to it cannot reach the bytes Enforce writes. For an object
// surface it keeps the map[string]any type the existing Managed field promises;
// for other kinds it is the copied whole-file value.
func managedView(managed any) any {
	if m, ok := managed.(map[string]any); ok {
		return deepCopyMap(m)
	}
	return deepCopyValue(managed)
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
// surfaces documented (claude/copilot managed nested objects). Managed values are
// deep-copied in, so Config never shares mutable structure with the enforced
// layer.
// For a KEYLESS surface (raw/lines) there is nothing to merge key-by-key: a
// non-nil enforced layer replaces the whole rendered value. `managed` on a raw
// surface therefore means "this file is exactly these bytes", which is coarse but
// is the only thing "enforce" can mean without keys. A nil enforced layer leaves
// Config alone, so a raw surface with no managed value is untouched.
func (c *Ctx) Enforce() {
	cfgMap, cfgIsObj := c.Config.(map[string]any)
	encMap, encIsObj := c.enforced.(map[string]any)
	if !cfgIsObj || !encIsObj {
		if c.enforced != nil {
			c.Config = deepCopyValue(c.enforced)
		}
		return
	}
	for k, v := range encMap {
		cfgMap[k] = enforceValue(cfgMap[k], v)
	}
	c.Config = cfgMap
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
// nil value and a wrapped error (fail-closed, §3.4 "loud failure") — callers keep
// the last good render rather than shipping a half-transformed file. Apply does
// NOT run Enforce; the caller applies the managed layer after (§3.1), which the
// tests exercise explicitly.
// Apply returns the config as `any` because the surface's codec decides its
// shape (map for json/toml, []any for lines, string for raw) — see Ctx.Config.
func Apply(t Transform, ctx *Ctx) (any, error) {
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
