// Package manifest declares the per-surface schema behind yolo's
// generated-config composition pipeline
// (docs/plans/agent-settings-composition.md §1.1 surface inventory, §3.3
// codecs, §4 layers/scope). It is a Phase-A leaf library with NO callers: its
// job is to PIN the shape of a "surface" declaration and the manifest that
// collects them, so later Phase-A/B pieces (the codecs, the Lua VM, the
// entrypoint wiring) build against a frozen schema.
//
// # What a Surface is (§1.1, §3.3, §4)
//
// A "surface" is one config file yolo generates from an ordered stack of
// layers (§4): defaults < host < workspace < runtime-overlay < managed. A
// surface earns the pipeline when there's a host or config layer to merge and a
// reason a user might reshape it (§1.1). Each surface declares:
//
//   - which agent it belongs to and its per-agent surface name (e.g. Claude has
//     two surfaces, "settings" for settings.json and "config" for .claude.json);
//   - the user-scope file Path yolo writes inside the jail;
//   - the Codec that decodes/encodes the file — a STRING name only
//     (json/toml/yaml/lines/raw, §3.3), NOT a codec implementation. This
//     package is deliberately decoupled from internal/agentcfg/codec: the
//     string is resolved to a real codec later, outside Phase A. Keeping the
//     name loose is what lets the codec worker and this schema land in parallel;
//   - the `defaults` layer data (yolo builtin, user-overridable — §4) and the
//     `managed` layer data (yolo's asserted keys, re-applied after the Lua hook
//     — §4, §3.1);
//   - optionally, the path to a Lua transform hook (§3). Empty means identity
//     (pass-through).
//
// # Go-declared registry, not a data-loaded file (decision)
//
// The design (§1.1 "The manifest is where a surface is declared", §8 Phase A
// step 3 "the manifest schema + loader") treats manifests as yolo-shipped image
// data — builtin, not user-authored. This package therefore models a manifest
// as a Go-DECLARED registry (surfaces handed to New / a Builder), NOT parsed
// from a file at runtime. Rationale:
//
//   - it matches "a leaf library with no callers" (§8) — a Go registry needs no
//     I/O, no filesystem access, and no new dependency, so it is trivially
//     unit-testable in isolation;
//   - the defaults/managed layer DATA a surface carries is arbitrary
//     map[string]any that a real manifest will most naturally express as Go
//     literals beside the generator it replaces (§1.1 "adding a surface is
//     adding a manifest entry").
//
// A data-loaded variant (read surfaces from an embedded/on-disk JSON or TOML
// file) slots in later without changing this schema: a loader would decode into
// a []Surface and call New(surfaces...) — the validation below is the same gate
// either way. The codec-string field is exactly what such a file would carry,
// so nothing here presumes Go-literal-only origin.
package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// Surface declares one generated-config file and the layer data yolo composes
// for it. Field names follow the design's vocabulary (§4 layer names
// `defaults`/`managed`, §3.2 `agent`/`surface`, §3.3 `codec`); where the doc
// does not name a Go field (the file path, the transform path) the names Path
// and Transform are chosen for clarity.
type Surface struct {
	// Agent is the owning agent id (§3.2 ctx.agent): "claude", "pi", "codex",
	// "copilot", "gemini", "opencode" — or a non-agent surface owner such as
	// "mcp", "lsp", "mise", "identity" (§1.1 lists these alongside the agents).
	Agent string

	// Name is the per-agent surface name (§3.2 ctx.surface), e.g. "settings" /
	// "config". (Agent, Name) is the unique key of a surface within a Manifest.
	Name string

	// Path is the user-scope file yolo writes inside the jail (§2 principle 2,
	// e.g. "~/.pi/agent/settings.json"). Required — an empty Path is a loud
	// validation error.
	Path string

	// Codec is the STRING name of the decode/encode round-trip (§3.3):
	// "json" | "toml" | "yaml" | "lines" | "raw". This is intentionally a name,
	// not an imported codec — see the package doc. Required and must be one of
	// the known names.
	Codec string

	// Defaults is the `defaults` layer (§4): yolo's builtin base data, lowest
	// precedence and user-overridable. Optional (nil == no defaults).
	Defaults map[string]any

	// Managed is the `managed` layer (§4, §3.1): yolo's asserted keys, applied
	// AFTER the Lua hook so they win the merge in the generated file. Optional
	// (nil == nothing enforced).
	Managed map[string]any

	// Transform is the optional path to the Lua transform hook for this surface
	// (§3.4). Empty means identity / pass-through. This package does not read or
	// execute the file; it only carries the path.
	Transform string
}

// Key returns the (Agent, Name) identity used to detect duplicates.
func (s Surface) Key() SurfaceKey { return SurfaceKey{Agent: s.Agent, Name: s.Name} }

// SurfaceKey is the unique identity of a surface within a manifest.
type SurfaceKey struct {
	Agent string
	Name  string
}

func (k SurfaceKey) String() string { return k.Agent + "/" + k.Name }

// knownCodecs is the closed set of codec names a surface may declare (§3.3).
// This package validates the NAME only; resolving a name to a Codec
// implementation happens later, outside Phase A (see the package doc).
var knownCodecs = map[string]struct{}{
	"json":  {},
	"toml":  {},
	"yaml":  {},
	"lines": {},
	"raw":   {},
}

// CodecNames returns the sorted set of accepted codec names — handy for error
// messages and for a future data-loader that wants to validate ahead of New.
func CodecNames() []string {
	names := make([]string, 0, len(knownCodecs))
	for n := range knownCodecs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Manifest is a validated collection of Surfaces keyed by (Agent, Name). It is
// constructed via New (Go-declared registry — see the package doc); it is
// immutable once built.
type Manifest struct {
	surfaces []Surface
	byKey    map[SurfaceKey]int // key -> index into surfaces
}

// New builds and validates a Manifest from the given surfaces. It returns a
// loud error if any surface is invalid (empty Path, unknown Codec name, empty
// Agent/Name) or if two surfaces share the same (Agent, Name) key. On error the
// returned *Manifest is nil.
func New(surfaces ...Surface) (*Manifest, error) {
	m := &Manifest{
		surfaces: make([]Surface, 0, len(surfaces)),
		byKey:    make(map[SurfaceKey]int, len(surfaces)),
	}
	for i, s := range surfaces {
		if err := s.validate(); err != nil {
			return nil, fmt.Errorf("manifest: surface[%d] (%s): %w", i, s.Key(), err)
		}
		key := s.Key()
		if _, dup := m.byKey[key]; dup {
			return nil, fmt.Errorf("manifest: duplicate surface key %q", key)
		}
		m.byKey[key] = len(m.surfaces)
		m.surfaces = append(m.surfaces, s)
	}
	return m, nil
}

// validate enforces the per-surface invariants (§3.3 codec, §2 user-scope path).
func (s Surface) validate() error {
	if strings.TrimSpace(s.Agent) == "" {
		return fmt.Errorf("empty Agent")
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("empty Name")
	}
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("empty Path")
	}
	if s.Codec == "" {
		return fmt.Errorf("empty Codec (want one of %s)", strings.Join(CodecNames(), ", "))
	}
	if _, ok := knownCodecs[s.Codec]; !ok {
		return fmt.Errorf("unknown Codec %q (want one of %s)", s.Codec, strings.Join(CodecNames(), ", "))
	}
	return nil
}

// Len reports how many surfaces the manifest holds.
func (m *Manifest) Len() int { return len(m.surfaces) }

// Surfaces returns a copy of the surfaces in declaration order, so a caller
// cannot mutate the manifest's backing slice.
func (m *Manifest) Surfaces() []Surface {
	out := make([]Surface, len(m.surfaces))
	copy(out, m.surfaces)
	return out
}

// Lookup returns the surface for (agent, name) and whether it exists.
func (m *Manifest) Lookup(agent, name string) (Surface, bool) {
	i, ok := m.byKey[SurfaceKey{Agent: agent, Name: name}]
	if !ok {
		return Surface{}, false
	}
	return m.surfaces[i], true
}

// ForAgent returns the surfaces owned by the given agent, in declaration order.
func (m *Manifest) ForAgent(agent string) []Surface {
	var out []Surface
	for _, s := range m.surfaces {
		if s.Agent == agent {
			out = append(out, s)
		}
	}
	return out
}
