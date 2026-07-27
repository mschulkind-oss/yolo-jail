package manifest

// load.go decodes surfaces from DATA (D3), so a surface definition can live outside
// Go — in an embedded official pack, or in a fetched third-party one.
//
// This is the seam the manifest was designed for: "a data-loaded variant … slots in
// later without changing this schema: a loader would decode into a []Surface and call
// New(surfaces...)". It goes through the identical New() validation as the Go
// literals, so a data-defined surface cannot be less checked than a built-in one.
//
// WHY A SEPARATE DTO rather than json tags on Surface: Surface's layer fields are
// `any` holding the engine's plain value model, and its Mode is a string constant
// set. A tagged DTO lets the wire format be validated and normalized on the way in —
// an unknown key rejected, a bad mode named — instead of silently decoding into a
// zero value that behaves like a default. `internal/config.HostFileEntry` is the same
// pattern for the same reason.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// SurfaceDTO is the wire form of one surface.
type SurfaceDTO struct {
	Agent     string         `json:"agent"`
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	Codec     string         `json:"codec"`
	Mode      string         `json:"mode,omitempty"`
	Defaults  map[string]any `json:"defaults,omitempty"`
	Managed   map[string]any `json:"managed,omitempty"`
	Transform string         `json:"transform,omitempty"`
	Computed  []Computed     `json:"computed,omitempty"`
	Retire    []string       `json:"retireOnFirstRender,omitempty"`
}

// knownModes is the closed set a DTO may name. A mode outside it is an error rather
// than a silent fallback to stateful: silently capturing edits on a surface whose
// author asked for overwrite semantics is a data-loss bug, not a formatting nit.
var knownModes = map[string]bool{
	ModeStateful: true, ModeComputed: true, ModeRMW: true, ModeUnrendered: true,
}

// Surface converts a DTO, reporting every problem it can rather than the first, so a
// pack author fixing a surface definition gets the whole list.
func (d SurfaceDTO) Surface() (Surface, []string) {
	var problems []string
	label := d.Agent + "/" + d.Name
	if d.Agent == "" {
		problems = append(problems, "missing \"agent\"")
	}
	if d.Name == "" {
		problems = append(problems, "missing \"name\"")
	}
	if d.Path == "" {
		problems = append(problems, label+": missing \"path\"")
	}
	if d.Codec == "" {
		problems = append(problems, label+": missing \"codec\"")
	}
	if d.Mode != "" && !knownModes[d.Mode] {
		problems = append(problems, fmt.Sprintf("%s: unknown mode %q (expected %s)",
			label, d.Mode, joinSortedKeys(knownModes)))
	}
	for i, c := range d.Computed {
		for _, prob := range c.Validate() {
			problems = append(problems, fmt.Sprintf("%s: computed[%d]: %s", label, i, prob))
		}
	}
	s := Surface{
		Agent: d.Agent, Name: d.Name, Path: d.Path, Codec: d.Codec,
		Mode: d.Mode, Transform: d.Transform, Computed: d.Computed,
		RetireOnFirstRender: d.Retire,
	}
	// Assign layers only when non-nil. An empty map is NOT the same as absent: on a
	// keyless surface an empty-map layer hard-errors, and on an object surface it
	// would claim "yolo asserts nothing here" rather than "yolo has no opinion".
	if d.Defaults != nil {
		s.Defaults = anyMap(d.Defaults)
	}
	if d.Managed != nil {
		s.Managed = anyMap(d.Managed)
	}
	return s, problems
}

// anyMap re-types a decoded map into the engine's plain value model.
func anyMap(m map[string]any) any {
	if m == nil {
		return nil
	}
	return map[string]any(m)
}

// DecodeSurfaces decodes a JSON array of surface DTOs.
//
// Strict: an unknown FIELD is an error. A pack author who misspells "managed" as
// "manged" would otherwise get a surface that silently asserts nothing, and would
// have no way to tell from the output that anything was wrong.
func DecodeSurfaces(data []byte) ([]Surface, []string) {
	var dtos []SurfaceDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dtos); err != nil {
		return nil, []string{"decoding surfaces: " + err.Error()}
	}
	var out []Surface
	var problems []string
	for i, d := range dtos {
		s, probs := d.Surface()
		if len(probs) > 0 {
			for _, p := range probs {
				problems = append(problems, fmt.Sprintf("surfaces[%d]: %s", i, p))
			}
			continue
		}
		out = append(out, s)
	}
	return out, problems
}

// Merge builds a manifest from a base set plus additional (data-loaded) surfaces.
//
// A later surface REPLACES an earlier one with the same (agent, name) — that is what
// lets an official pack's definition be overridden, and it matches the "later entries
// win" rule packs already use for skills. Everything then goes through New(), so an
// override is validated exactly as strictly as the thing it replaced.
func Merge(base []Surface, extra ...Surface) (*Manifest, error) {
	byKey := map[SurfaceKey]Surface{}
	var order []SurfaceKey
	for _, s := range append(append([]Surface{}, base...), extra...) {
		k := s.Key()
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = s
	}
	merged := make([]Surface, 0, len(order))
	for _, k := range order {
		merged = append(merged, byKey[k])
	}
	return New(merged...)
}

func joinSortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
