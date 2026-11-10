package approvalfixtures

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Manifest is what a capture runner writes beside its images, at
// gallery/captures/<platform>/manifest.json: for each fixture id, the native captures it made.
// The capture runners never edit a fixture; Fold is the one path from a manifest into the
// fixtures' evidence, so the fixture files keep a single writer per field.
type Manifest struct {
	Schema   int                          `json:"schema"`
	Platform string                       `json:"platform"`
	Captures map[string][]ManifestCapture `json:"captures"`
}

// ManifestCapture is one capture. Provenance values may be any JSON scalar; Fold renders
// each as a string.
type ManifestCapture struct {
	File       string         `json:"file"`
	Renderer   string         `json:"renderer"`
	Tested     string         `json:"tested"`
	Provenance map[string]any `json:"provenance"`
	Note       string         `json:"note,omitempty"`
}

// ParseManifest decodes a manifest, refusing unknown fields.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, err
	}
	if m.Schema != 1 {
		return m, fmt.Errorf("manifest schema %d, want 1", m.Schema)
	}
	if m.Platform != "linux" && m.Platform != "macos" {
		return m, fmt.Errorf("manifest platform %q is not linux or macos", m.Platform)
	}
	return m, nil
}

// Fold returns f with the manifest's captures for it as native-capture evidence. Every
// existing entry for the manifest's platform with the same renderer, missing or captured
// before, is replaced; entries for other platforms and renderers are kept in order. A
// fixture the manifest does not name is returned unchanged.
func Fold(f Fixture, m Manifest) Fixture {
	caps, ok := m.Captures[f.ID]
	if !ok {
		return f
	}
	replaced := map[string]bool{}
	for _, c := range caps {
		replaced[c.Renderer] = true
	}
	var out []Evidence
	inserted := false
	insert := func() {
		for _, c := range caps {
			prov := map[string]string{}
			for k, v := range c.Provenance {
				if s, ok := v.(string); ok {
					prov[k] = s
				} else {
					b, _ := json.Marshal(v)
					prov[k] = string(b)
				}
			}
			out = append(out, Evidence{Platform: m.Platform, Kind: "native-capture", Renderer: c.Renderer,
				File: c.File, Tested: c.Tested, Provenance: prov, Note: c.Note})
		}
		inserted = true
	}
	for _, e := range f.Evidence {
		if e.Platform == m.Platform && replaced[e.Renderer] {
			if !inserted {
				insert()
			}
			continue
		}
		out = append(out, e)
	}
	if !inserted {
		insert()
	}
	f.Evidence = out
	return f
}

// Encode is a fixture's canonical file form: two-space indent, no HTML escaping, a trailing
// newline. Every writer of fixture files uses it so their diffs stay minimal.
func Encode(f Fixture) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(f)
	return b.Bytes(), err
}
