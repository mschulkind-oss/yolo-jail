package manifest

import (
	"strings"
	"testing"
)

// A well-formed overlay body decodes to the single layer map the engine folds.
func TestDecodeOverlayYieldsTheContributedKeys(t *testing.T) {
	got, probs := DecodeOverlay("claude/settings",
		[]byte(`{"managed":{"fileSuggestion":{"type":"command","command":"~/x.sh"}}}`))
	if len(probs) != 0 {
		t.Fatalf("well-formed body reported problems: %v", probs)
	}
	fs, ok := got["fileSuggestion"].(map[string]any)
	if !ok || fs["type"] != "command" {
		t.Errorf("decoded layer = %#v, want the contributed nested object", got)
	}
}

// Every field an overlay may NOT set is refused BY NAME with the rule, not as an unknown
// field — a `mode` key is real, just not a contributor's to set, so "unknown field" would
// read as a typo report.
func TestDecodeOverlayRefusesSurfaceRedefinition(t *testing.T) {
	cases := map[string]string{
		"agent":               `{"agent":"claude","managed":{"k":1}}`,
		"name":                `{"name":"settings","managed":{"k":1}}`,
		"path":                `{"path":"~/x.json","managed":{"k":1}}`,
		"codec":               `{"codec":"toml","managed":{"k":1}}`,
		"mode":                `{"mode":"rmw","managed":{"k":1}}`,
		"transform":           `{"transform":"/x.lua","managed":{"k":1}}`,
		"defaults":            `{"defaults":{"k":1},"managed":{"j":1}}`,
		"retireOnFirstRender": `{"retireOnFirstRender":["x"],"managed":{"k":1}}`,
	}
	for field, body := range cases {
		t.Run(field, func(t *testing.T) {
			got, probs := DecodeOverlay("claude/settings", []byte(body))
			if got != nil {
				t.Errorf("a refused body must yield no layer, got %#v", got)
			}
			joined := strings.Join(probs, "\n")
			if !strings.Contains(joined, "may not set \""+field+"\"") {
				t.Errorf("problems %q must name the refused field %q and the rule", joined, field)
			}
			if !strings.Contains(joined, "claude/settings") {
				t.Errorf("problems %q must name the target surface", joined)
			}
		})
	}
}

// An empty or absent body is a problem, not an empty layer: a contribution that asserts
// nothing is a declaration the author meant to be load-bearing and is not.
func TestDecodeOverlayRejectsEmptyBody(t *testing.T) {
	for _, body := range []string{"", "   ", "{}", `{"managed":{}}`} {
		_, probs := DecodeOverlay("claude/settings", []byte(body))
		if len(probs) == 0 {
			t.Errorf("body %q should be refused as contributing nothing", body)
		}
	}
}

// A misspelled field is loud, for the same reason DecodeSurfaces is strict: silence would
// mean an overlay that contributes nothing with no signal at all.
func TestDecodeOverlayRejectsUnknownField(t *testing.T) {
	_, probs := DecodeOverlay("claude/settings", []byte(`{"manged":{"k":1}}`))
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "unknown field") {
		t.Errorf("a misspelled field must be an error, got %v", probs)
	}
}

// ParseSurfaceID is the ONE split both sides of the collection agree on.
func TestParseSurfaceID(t *testing.T) {
	key, err := ParseSurfaceID("claude/settings")
	if err != nil || key.Agent != "claude" || key.Name != "settings" {
		t.Fatalf("ParseSurfaceID(claude/settings) = %+v, %v", key, err)
	}
	for _, bad := range []string{"", "claude", "/settings", "claude/", "claude settings"} {
		if _, err := ParseSurfaceID(bad); err == nil {
			t.Errorf("ParseSurfaceID(%q) should be an error", bad)
		}
	}
	// A three-segment identity keeps everything after the first slash as the name, so a
	// surface name containing a slash is not silently truncated.
	if key, err := ParseSurfaceID("claude/a/b"); err != nil || key.Name != "a/b" {
		t.Errorf("ParseSurfaceID(claude/a/b) = %+v, %v; want Name a/b", key, err)
	}
}
