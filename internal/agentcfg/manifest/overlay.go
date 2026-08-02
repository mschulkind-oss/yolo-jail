package manifest

// overlay.go decodes a `config-overlay` contribution's BODY — the keys one pack
// asserts onto a surface another pack owns (docs/design/pack-system.md §3,
// docs/design/pack-config-collaboration.md §1 Layout C).
//
// It lives beside load.go's SurfaceDTO because it is the same wire vocabulary minus
// everything an overlay is not allowed to say. That subtraction is the whole point of
// the kind: where a `config` contribution declares a surface's identity, path, codec
// and mode, an overlay only contributes KEYS — "the fzf pack contributes one key and
// cannot change the file's mode, path, or codec". Making the body a DTO with a closed
// field set is what turns that promise into a decode-time error instead of a
// convention; a bare key-map body could not tell a config key named "mode" from a mode
// declaration, so the guarantee would be unstateable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// OverlayDTO is the wire form of a config-overlay body.
//
// The refused fields are declared rather than left to DisallowUnknownFields so the
// diagnostic names the RULE ("an overlay may not redefine the surface it targets")
// instead of claiming `mode` is an unknown field — which would read as a typo report
// for a field that is real, just not an overlay's to set.
type OverlayDTO struct {
	// Managed is the keys this pack asserts onto the target surface.
	//
	// The name is the pack author's vocabulary for "the keys I insist on", matching the
	// `config` surface field it mirrors — NOT a claim about the managed LAYER. An
	// overlay folds at the single `config-overlay` slot, which is below the owner's
	// managed, so the surface's owner still wins a genuine conflict (§5 precedence).
	Managed map[string]any `json:"managed,omitempty"`

	// The fields below are REFUSED, each with its own reason. They are here to be
	// rejected by name; none is ever read.
	Defaults  map[string]any `json:"defaults,omitempty"`
	Agent     string         `json:"agent,omitempty"`
	Name      string         `json:"name,omitempty"`
	Path      string         `json:"path,omitempty"`
	Codec     string         `json:"codec,omitempty"`
	Mode      string         `json:"mode,omitempty"`
	Transform string         `json:"transform,omitempty"`
	Retire    []string       `json:"retireOnFirstRender,omitempty"`
}

// overlayRefusals is the reason each non-contributable field is refused, so the
// diagnostics stay in one table rather than a chain of ifs that can disagree.
var overlayRefusals = []struct {
	field  string
	reason string
	set    func(OverlayDTO) bool
}{
	{"defaults", "an overlay folds at ONE precedence slot (below the owner's managed), " +
		"so a contributor has no separate defaults position — declare the keys under \"managed\"",
		func(d OverlayDTO) bool { return d.Defaults != nil }},
	{"agent", "the target surface is named by \"surface\", not redeclared in the body",
		func(d OverlayDTO) bool { return d.Agent != "" }},
	{"name", "the target surface is named by \"surface\", not redeclared in the body",
		func(d OverlayDTO) bool { return d.Name != "" }},
	{"path", "an overlay contributes keys; the surface's OWNER decides where the file lands",
		func(d OverlayDTO) bool { return d.Path != "" }},
	{"codec", "an overlay contributes keys; the surface's OWNER decides the file format",
		func(d OverlayDTO) bool { return d.Codec != "" }},
	{"mode", "an overlay contributes keys; the surface's OWNER decides how the file is " +
		"maintained across boots (silently flipping it is the hazard this kind exists to remove)",
		func(d OverlayDTO) bool { return d.Mode != "" }},
	{"transform", "a transform reshapes the WHOLE file, which is the owner's call, not a " +
		"contributor's", func(d OverlayDTO) bool { return d.Transform != "" }},
	{"retireOnFirstRender", "sidecar cleanup belongs to the surface's owner",
		func(d OverlayDTO) bool { return len(d.Retire) > 0 }},
}

// DecodeOverlay decodes a config-overlay body into the single layer map the compose
// engine folds (agentcfg.Overlay.Data), reporting every problem rather than the first.
//
// Strict about unknown fields for the same reason DecodeSurfaces is: a misspelled
// "manged" would otherwise be an overlay that silently contributes nothing, and a pack
// author would have no signal at all — the exact silent-non-delivery this kind's whole
// justification (a legible override) rules out.
//
// An EMPTY body is a problem, not an empty layer: a contribution that asserts no key is
// a declaration the author meant to be load-bearing and is not.
func DecodeOverlay(target string, data []byte) (map[string]any, []string) {
	label := "config-overlay " + target
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, []string{label + ": missing \"config\" body"}
	}
	var d OverlayDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, []string{label + ": " + err.Error()}
	}
	var problems []string
	for _, r := range overlayRefusals {
		if r.set(d) {
			problems = append(problems, fmt.Sprintf("%s: may not set %q — %s", label, r.field, r.reason))
		}
	}
	if len(d.Managed) == 0 {
		problems = append(problems, label+": contributes no keys (an overlay body needs a "+
			"non-empty \"managed\" object)")
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return d.Managed, nil
}

// ParseSurfaceID splits a `config-overlay` target ("agent/name") into a SurfaceKey.
//
// One definition, because both sides of the collection have to agree on it: the pack
// declares the identity as one string and the render looks the surface up by (Agent,
// Name). A second copy of the split is how an overlay silently targets nothing.
func ParseSurfaceID(id string) (SurfaceKey, error) {
	agent, name, found := strings.Cut(id, "/")
	if !found || agent == "" || name == "" {
		return SurfaceKey{}, fmt.Errorf("surface %q is not an \"agent/name\" identity", id)
	}
	return SurfaceKey{Agent: agent, Name: name}, nil
}
