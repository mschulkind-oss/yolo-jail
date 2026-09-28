package packdecl

// postureoverlay_test.go pins the declaration half of a POSTURE OVERLAY — a term coined by
// docs/design/notch-scoped-config-contributions.md (OQ-3's build, NS-D19) for one entry of an
// autonomy posture's `config` that names a surface its own pack does not declare. Such an
// entry decodes exactly as every posture patch does, and the one projection that carries it to
// the collector, OverlayContributions, walks it at its autonomy contribution's position among
// the pack's config-overlays, tagged with its posture. Which entries are another pack's, and
// which notch places them, is packoverlay's, and is tested there.

import (
	"encoding/json"
	"testing"
)

// A host-only scalar and a jail-only one on a surface the pack does not own decode clean, and
// reach OverlayContributions in declaration order — a config-overlay declared before the
// autonomy contribution, the autonomous posture's entries, the guarded one's, then a
// config-overlay declared after — each entry its own row naming its surface. A field that
// decodes but never leaves the Contribution is a key no fold can write, and a projection that
// lost the position would let the wrong one win a key both set ("later wins").
func TestPostureConfigEntriesTravelAsOverlaysInDeclarationOrder(t *testing.T) {
	m, problems := Decode([]byte(`{"name": "matt", "contributes": [
		{"kind": "config-overlay", "surface": "pi/settings", "config": {"managed": {"first": 1}}},
		{"kind": "autonomy",
		 "autonomous": {"config": [
		   {"agent": "pi", "name": "settings", "codec": "json", "path": "~/.pi/agent/settings.json",
		    "managed": {"jailOnly": true}}]},
		 "guarded": {"config": [
		   {"agent": "pi", "name": "settings", "codec": "json", "path": "~/.pi/agent/settings.json",
		    "managed": {"hostOnly": true}},
		   {"agent": "claude", "name": "settings", "codec": "json", "path": "~/.claude/settings.json",
		    "managed": {"theme": "dark"}}]}},
		{"kind": "config-overlay", "surface": "pi/settings", "config": {"managed": {"last": 1}}}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("a posture config patch on another pack's surface was refused: %v", problems)
	}

	type row struct {
		posture Posture
		surface string
		key     string
	}
	var got []row
	for _, ov := range m.OverlayContributions() {
		var body struct {
			Managed map[string]any `json:"managed"`
		}
		if err := json.Unmarshal(ov.Config, &body); err != nil {
			t.Fatalf("OverlayContributions() carried an undecodable body %s: %v", ov.Config, err)
		}
		key := ""
		for k := range body.Managed {
			key = k
		}
		got = append(got, row{ov.Posture, ov.Surface, key})
	}
	want := []row{
		{"", "pi/settings", "first"},
		{PostureAutonomous, "pi/settings", "jailOnly"},
		{PostureGuarded, "pi/settings", "hostOnly"},
		{PostureGuarded, "claude/settings", "theme"},
		{"", "pi/settings", "last"},
	}
	if len(got) != len(want) {
		t.Fatalf("OverlayContributions() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("OverlayContributions()[%d] = %+v, want %+v — declaration order is the "+
				"fold's later-wins order, and a posture's entries stand at the autonomy "+
				"contribution's position", i, got[i], want[i])
		}
	}

	// ConfigOverlayContributions stays the config-overlay KIND's projection: the footprint's
	// config-overlay claims read it, and a posture's patch is declared under kind autonomy.
	for _, ov := range m.ConfigOverlayContributions() {
		if ov.Posture != "" {
			t.Errorf("a posture patch leaked into ConfigOverlayContributions: %+v", ov)
		}
	}
	if n := len(m.ConfigOverlayContributions()); n != 2 {
		t.Errorf("ConfigOverlayContributions() = %d entries, want the 2 config-overlays", n)
	}
}

// Only the FIRST autonomy contribution is walked, the one PostureFor reads, so a hand-built
// manifest cannot place a posture patch no other posture reader sees (NS-D5's rule, for the
// config half). And a posture `config` that is not an array of objects yields nothing here:
// the engine's decode reports it, and a projection guessing at it would place keys the render
// path refuses.
func TestOverlayContributionsReadsTheFirstAutonomyAndSkipsWhatItCannotSplit(t *testing.T) {
	entry := json.RawMessage(`[{"agent":"pi","name":"settings","codec":"json",` +
		`"path":"~/.pi/agent/settings.json","managed":{"k":1}}]`)
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindAutonomy, Guarded: &AutonomyPosture{Config: entry}},
		{Kind: KindAutonomy, Guarded: &AutonomyPosture{Config: entry}},
		{Kind: KindAutonomy, Autonomous: &AutonomyPosture{Config: json.RawMessage(`{"not":"an array"}`)}},
	}}
	if got := m.OverlayContributions(); len(got) != 1 || got[0].Posture != PostureGuarded ||
		got[0].Surface != "pi/settings" {
		t.Errorf("OverlayContributions() = %+v, want the first autonomy contribution's one entry", got)
	}

	bad := &Manifest{Contributes: []Contribution{
		{Kind: KindAutonomy, Guarded: &AutonomyPosture{Config: json.RawMessage(`{"not":"an array"}`)}},
	}}
	if got := bad.OverlayContributions(); len(got) != 0 {
		t.Errorf("an unsplittable posture config yielded %+v", got)
	}
}
