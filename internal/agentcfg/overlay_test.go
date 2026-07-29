package agentcfg

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// overlaySurface is a minimal object surface owned by one pack: a defaults layer
// and a managed floor. Config-overlays from OTHER packs (Inputs.Overlays) layer
// onto it.
func overlaySurface() manifest.Surface {
	return manifest.Surface{
		Agent:    "claude",
		Name:     "settings",
		Path:     "~/.claude/settings.json",
		Codec:    "json",
		Defaults: map[string]any{"theme": "system", "editor": "vim"},
		Managed:  map[string]any{"telemetry": false},
	}
}

// A config-overlay overrides the owner's DEFAULTS (later-wins), and provenance
// attributes the overridden key to the contributing pack.
func TestConfigOverlayOverridesDefaults(t *testing.T) {
	res, err := Compose(Inputs{
		Surface: overlaySurface(),
		Overlays: []Overlay{
			{Pack: "house-rules", Data: map[string]any{"theme": "dark"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	cfg := res.ConfigMap()
	if cfg["theme"] != "dark" {
		t.Errorf("overlay did not override owner default: theme=%v, want dark", cfg["theme"])
	}
	if cfg["editor"] != "vim" {
		t.Errorf("overlay clobbered an untouched default: editor=%v, want vim", cfg["editor"])
	}
	// Provenance names the pack that won the key.
	if got := res.Provenance["theme"]; got != "config-overlay:house-rules" {
		t.Errorf("provenance for overridden key = %q, want config-overlay:house-rules", got)
	}
	if got := res.Provenance["editor"]; got != "defaults" {
		t.Errorf("provenance for untouched key = %q, want defaults", got)
	}
}

// The managed floor still wins over a config-overlay — an overlay cannot weaken
// a yolo-enforced key.
func TestConfigOverlayCannotOverrideManaged(t *testing.T) {
	res, err := Compose(Inputs{
		Surface: overlaySurface(),
		Overlays: []Overlay{
			{Pack: "house-rules", Data: map[string]any{"telemetry": true}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.ConfigMap()["telemetry"] != false {
		t.Errorf("config-overlay overrode a managed key: telemetry=%v, want false", res.ConfigMap()["telemetry"])
	}
	if got := res.Provenance["telemetry"]; got != "managed" {
		t.Errorf("managed key provenance = %q, want managed (overlay must not win it)", got)
	}
}

// The user's capture overlay (in-jail edit) still wins over a config-overlay —
// the pack overlay folds BELOW the capture overlay.
func TestCaptureOverlayWinsOverConfigOverlay(t *testing.T) {
	res, err := Compose(Inputs{
		Surface:  overlaySurface(),
		Overlays: []Overlay{{Pack: "house-rules", Data: map[string]any{"theme": "dark"}}},
		Overlay:  map[string]any{"theme": "solarized"}, // the §5 capture overlay
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.ConfigMap()["theme"] != "solarized" {
		t.Errorf("capture overlay should win over config-overlay: theme=%v, want solarized", res.ConfigMap()["theme"])
	}
	if got := res.Provenance["theme"]; got != "overlay" {
		t.Errorf("provenance = %q, want overlay (capture wins)", got)
	}
}

// Later config-overlays win over earlier ones (the "later pack wins" rule).
func TestConfigOverlayLaterWins(t *testing.T) {
	res, err := Compose(Inputs{
		Surface: overlaySurface(),
		Overlays: []Overlay{
			{Pack: "team", Data: map[string]any{"theme": "dark"}},
			{Pack: "personal", Data: map[string]any{"theme": "gruvbox"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if res.ConfigMap()["theme"] != "gruvbox" {
		t.Errorf("later overlay should win: theme=%v, want gruvbox", res.ConfigMap()["theme"])
	}
	if got := res.Provenance["theme"]; got != "config-overlay:personal" {
		t.Errorf("provenance = %q, want config-overlay:personal (later pack)", got)
	}
}

// No overlays → byte-identical to composing without the field (the A12 guarantee
// at the compose layer: adding the Overlays field changes nothing when unused).
func TestNoOverlaysUnchanged(t *testing.T) {
	base, err := Compose(Inputs{Surface: overlaySurface()})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	withEmpty, err := Compose(Inputs{Surface: overlaySurface(), Overlays: nil})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !reflect.DeepEqual(base.Config, withEmpty.Config) {
		t.Errorf("empty Overlays changed the config: %#v vs %#v", base.Config, withEmpty.Config)
	}
	if !reflect.DeepEqual(base.Provenance, withEmpty.Provenance) {
		t.Errorf("empty Overlays changed provenance: %#v vs %#v", base.Provenance, withEmpty.Provenance)
	}
}
