package packoverlay

// postureoverlay_test.go pins the collection of a POSTURE OVERLAY — an autonomy posture's
// `config` patch naming a surface its own pack does not declare (OQ-3's ruling, "do it now.
// extension point.", docs/design/notch-scoped-config-contributions.md NS-D19 to NS-D24). It
// takes the config-overlay path: the same owner check, the same fold slot below the owner's
// managed, the same later-wins order, gated on the posture the bit selects. Each test runs at
// both values of the bit.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// piOwnerPack owns pi/settings, the surface the motivating host-only scalar lands in.
func piOwnerPack() *packload.Pack {
	surface, _ := json.Marshal([]map[string]any{{
		"agent": "pi", "name": "settings", "codec": "json", "path": "~/.pi/agent/settings.json",
		"managed": map[string]any{"owned": "by-pi"},
	}})
	return &packload.Pack{Name: "pi", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: surface}},
	}}
}

// posturePatch is one posture `config` entry on pi/settings carrying managed.
func posturePatch(managed map[string]any, extra map[string]any) json.RawMessage {
	entry := map[string]any{"agent": "pi", "name": "settings", "codec": "json",
		"path": "~/.pi/agent/settings.json", "managed": managed}
	for k, v := range extra {
		entry[k] = v
	}
	raw, _ := json.Marshal([]any{entry})
	return raw
}

// postureConfigPack is a contributor owning no surface: its autonomy postures carry `config`.
func postureConfigPack(name string, autonomous, guarded json.RawMessage) *packload.Pack {
	c := packdecl.Contribution{Kind: packdecl.KindAutonomy}
	if autonomous != nil {
		c.Autonomous = &packdecl.AutonomyPosture{Config: autonomous}
	}
	if guarded != nil {
		c.Guarded = &packdecl.AutonomyPosture{Config: guarded}
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{c}}}
}

// THE RULING. A guarded posture's patch on pi's surface — a scalar and an object — is placed
// at the guarded posture (the host) and not at the autonomous one (a jail), and the reverse
// for the autonomous posture's patch. Once placed it is an agentcfg.Overlay from its pack,
// the config-overlay kind's own shape, so the fold, the provenance label and every reader of
// For treat it as one. Delete the posture overlays from Collect's overlay pass, or the gate,
// and one of the two answers is wrong.
func TestCollectPlacesAPostureOverlayAtTheSelectedPostureOnly(t *testing.T) {
	packs := []*packload.Pack{piOwnerPack(), postureConfigPack("matt",
		posturePatch(map[string]any{"jailOnly": "yes"}, nil),
		posturePatch(map[string]any{"hostOnly": true, "gate": map[string]any{"mode": "ask"}}, nil))}

	for _, c := range []struct {
		autonomy bool
		want     map[string]any
	}{
		{false, map[string]any{"hostOnly": true, "gate": map[string]any{"mode": "ask"}}},
		{true, map[string]any{"jailOnly": "yes"}},
	} {
		set := Collect(packs, c.autonomy, nil)
		if len(set.Problems) != 0 || len(set.Orphans) != 0 {
			t.Fatalf("autonomy=%v: problems=%v orphans=%+v", c.autonomy, set.Problems, set.Orphans)
		}
		got := set.For("pi", "settings")
		if len(got) != 1 || got[0].Pack != "matt" || !reflect.DeepEqual(got[0].Data, c.want) {
			t.Errorf("autonomy=%v: overlays on pi/settings = %+v, want matt's %v", c.autonomy, got, c.want)
		}
		if !set.PlacesPostureConfigFrom("matt") {
			t.Errorf("autonomy=%v: the placed posture overlay is not reported as placed", c.autonomy)
		}
		if applied := set.Applied(); len(applied) != 1 || !reflect.DeepEqual(applied[0].Packs, []string{"matt"}) {
			t.Errorf("autonomy=%v: Applied() = %+v, want pi/settings from matt — a posture "+
				"overlay is named at the moment it applies, like any overlay (R3)", c.autonomy, applied)
		}
	}
}

// The OWN-SURFACE half is untouched: a posture patch on a surface its pack declares still folds
// into that pack's managed layer (packload.SurfacesFor), never into the overlays — or the
// posture's keys would drop below the owner's managed and lose to the very keys they tighten.
func TestAnOwnSurfacePosturePatchIsNotAnOverlay(t *testing.T) {
	pi := piOwnerPack()
	pi.Decl.Contributes = append(pi.Decl.Contributes, packdecl.Contribution{
		Kind: packdecl.KindAutonomy, Guarded: &packdecl.AutonomyPosture{
			Config: posturePatch(map[string]any{"defaultProjectTrust": "ask"}, nil)}})
	for _, autonomy := range []bool{true, false} {
		set := Collect([]*packload.Pack{pi}, autonomy, nil)
		if got := set.For("pi", "settings"); got != nil {
			t.Errorf("autonomy=%v: an own-surface posture patch became an overlay: %+v", autonomy, got)
		}
		if set.PlacesPostureConfigFrom("pi") || len(set.Orphans) != 0 || len(set.Problems) != 0 {
			t.Errorf("autonomy=%v: orphans=%+v problems=%v", autonomy, set.Orphans, set.Problems)
		}
	}
	surfaces, _ := pi.SurfacesFor(false)
	if m := surfaces[0].ManagedMap(); m["defaultProjectTrust"] != "ask" || m["owned"] != "by-pi" {
		t.Errorf("the own-surface patch no longer folds into the managed layer: %+v", m)
	}
}

// LATER WINS, config-overlay's rule. A posture overlay stands at its autonomy contribution's
// position in the pack's contributes, and a later pack's overlay follows it, so the fold
// order a key's winner is read from is pack order, then declaration order.
func TestAPostureOverlayFoldsAtItsDeclarationPosition(t *testing.T) {
	overlay := func(v string) packdecl.Contribution {
		body, _ := json.Marshal(map[string]any{"managed": map[string]any{"k": v}})
		return packdecl.Contribution{Kind: packdecl.KindConfigOverlay, Surface: "pi/settings", Raw: body}
	}
	matt := &packload.Pack{Name: "matt", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		overlay("before"),
		{Kind: packdecl.KindAutonomy,
			Autonomous: &packdecl.AutonomyPosture{Config: posturePatch(map[string]any{"k": "jail-posture"}, nil)},
			Guarded:    &packdecl.AutonomyPosture{Config: posturePatch(map[string]any{"k": "host-posture"}, nil)}},
		overlay("after"),
	}}}
	later := &packload.Pack{Name: "later", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{overlay("later-pack")}}}
	for autonomy, middle := range map[bool]string{true: "jail-posture", false: "host-posture"} {
		var order []string
		for _, ov := range Collect([]*packload.Pack{piOwnerPack(), matt, later}, autonomy, nil).For("pi", "settings") {
			order = append(order, ov.Data.(map[string]any)["k"].(string))
		}
		if want := []string{"before", middle, "after", "later-pack"}; !reflect.DeepEqual(order, want) {
			t.Errorf("autonomy=%v: fold order = %v, want %v", autonomy, order, want)
		}
	}
}

// AN OWNERLESS posture overlay is inert and reported (R2) where its posture is selected, led by
// the kind the author wrote, and a clean skip where it is not — the posture list's rule, and
// what replaces the "folded nowhere" note the same declaration used to get.
func TestAnOwnerlessPostureOverlayIsAnOrphanOnlyWhereItsPostureIsSelected(t *testing.T) {
	packs := []*packload.Pack{postureConfigPack("matt", nil,
		posturePatch(map[string]any{"hostOnly": true}, nil))}
	host := Collect(packs, false, nil)
	if len(host.Orphans) != 1 {
		t.Fatalf("at the guarded posture: orphans = %+v, want the one posture overlay", host.Orphans)
	}
	o := host.Orphans[0]
	if o.KindName() != string(packdecl.KindAutonomy) || o.Pack != "matt" || o.Target != "pi/settings" {
		t.Errorf("orphan = %+v, want kind autonomy, pack matt, target pi/settings", o)
	}
	if !strings.Contains(o.Reason(), "`pi` pack is not selected") {
		t.Errorf("reason = %q, want the shipped owner named", o.Reason())
	}
	if host.PlacesPostureConfigFrom("matt") {
		t.Error("an orphaned posture overlay is reported as placed")
	}
	if jail := Collect(packs, true, nil); len(jail.Orphans) != 0 || len(jail.Problems) != 0 {
		t.Errorf("at the autonomous posture a guarded patch is a clean skip: orphans=%+v problems=%v",
			jail.Orphans, jail.Problems)
	}

	// A typo of the pack's OWN surface is the same orphan, which is the OQ-Z5 case the note
	// existed for: "check the identity" is still the message.
	typo := piOwnerPack()
	typo.Decl.Contributes = append(typo.Decl.Contributes, packdecl.Contribution{
		Kind: packdecl.KindAutonomy, Guarded: &packdecl.AutonomyPosture{Config: json.RawMessage(
			`[{"agent":"pi","name":"setings","codec":"json","path":"~/.pi/agent/settings.json",` +
				`"managed":{"k":1}}]`)}})
	set := Collect([]*packload.Pack{typo}, false, nil)
	if len(set.Orphans) != 1 || !strings.Contains(set.Orphans[0].Reason(), "check the identity") {
		t.Errorf("a posture patch on a misspelled identity: orphans = %+v", set.Orphans)
	}
}

// Onto a CORE surface: inert, and the sentence names the declaration — a posture's config
// patch — rather than the autonomy kind.
func TestAPostureOverlayOntoACoreSurfaceSaysWhatItIs(t *testing.T) {
	set := Collect([]*packload.Pack{postureConfigPack("matt", nil, json.RawMessage(
		`[{"agent":"mise","name":"config","codec":"toml","path":"~/.config/mise/config.toml",`+
			`"managed":{"k":1}}]`))}, false, nil)
	if len(set.Orphans) != 1 || !set.Orphans[0].CoreOwned {
		t.Fatalf("orphans = %+v, want one core-owned orphan", set.Orphans)
	}
	if r := set.Orphans[0].Reason(); !strings.Contains(r,
		"a posture's config patch contributes to a surface a pack owns") {
		t.Errorf("reason = %q", r)
	}
}

// CONFIG-OVERLAY'S REFUSALS, made of a posture overlay at EVERY notch: it contributes keys, so
// a `defaults`, `mode`, `retireOnFirstRender` or `readsHost` it carries is refused for
// config-overlay's reason, and an empty `managed` contributes nothing. Reported whether or not
// the posture is selected (the gate sits after the decode, as for a posture list), led by
// `autonomy <posture>.config` so apply.go's collectProblemKind names the kind the author
// wrote, and never placed.
func TestAPostureOverlayTakesConfigOverlaysRefusals(t *testing.T) {
	cases := map[string]json.RawMessage{
		"defaults": posturePatch(map[string]any{"k": 1},
			map[string]any{"defaults": map[string]any{"d": 1}}),
		"mode":                posturePatch(map[string]any{"k": 1}, map[string]any{"mode": "rmw"}),
		"retireOnFirstRender": posturePatch(map[string]any{"k": 1}, map[string]any{"retireOnFirstRender": []string{"x"}}),
		"readsHost":           posturePatch(map[string]any{"k": 1}, map[string]any{"readsHost": true}),
		"contributes no keys": json.RawMessage(`[{"agent":"pi","name":"settings","codec":"json",` +
			`"path":"~/.pi/agent/settings.json"}]`),
	}
	for field, patch := range cases {
		for _, autonomy := range []bool{true, false} {
			set := Collect([]*packload.Pack{piOwnerPack(), postureConfigPack("matt", nil, patch)}, autonomy, nil)
			if len(set.Problems) != 1 || !strings.HasPrefix(set.Problems[0], "pack matt: autonomy guarded.config") ||
				!strings.Contains(set.Problems[0], field) {
				t.Errorf("%s, autonomy=%v: problems = %v, want one naming it under the kind and "+
					"posture the author wrote", field, autonomy, set.Problems)
			}
			if set.For("pi", "settings") != nil {
				t.Errorf("%s, autonomy=%v: a refused posture overlay was placed", field, autonomy)
			}
		}
	}
}

// A MALFORMED posture entry — here one missing `codec` — is reported ONCE at every notch. A
// pack declaring no surface of its own has no posture fold (SurfacesForReport returns before
// it), so the collector is the only reader and reports it at both postures, or the motivating
// pack's typo would do nothing and say nothing. A pack that does declare a surface has its
// SELECTED posture decoded by the fold, which names the problem itself, so the collector
// reports only the unselected one.
func TestAMalformedPostureOverlayIsReportedOnceAtEveryNotch(t *testing.T) {
	missingCodec := json.RawMessage(`[{"agent":"pi","name":"settings",` +
		`"path":"~/.pi/agent/settings.json","managed":{"k":1}}]`)
	for _, autonomy := range []bool{true, false} {
		set := Collect([]*packload.Pack{piOwnerPack(), postureConfigPack("matt", nil, missingCodec)}, autonomy, nil)
		if len(set.Problems) != 1 || !strings.HasPrefix(set.Problems[0], "pack matt: autonomy guarded.config") ||
			!strings.Contains(set.Problems[0], "codec") {
			t.Errorf("surface-less contributor, autonomy=%v: problems = %v, want the one", autonomy, set.Problems)
		}
	}

	withOwn := postureConfigPack("acme", nil, missingCodec)
	withOwn.Decl.Contributes = append(withOwn.Decl.Contributes, ownerPack("acme").Decl.Contributes...)
	for autonomy, want := range map[bool]int{true: 1, false: 0} {
		set := Collect([]*packload.Pack{piOwnerPack(), withOwn}, autonomy, nil)
		if len(set.Problems) != want {
			t.Errorf("pack with its own surface, autonomy=%v: problems = %v, want %d (the fold "+
				"reports the selected posture's)", autonomy, set.Problems, want)
		}
	}
	if _, problems := withOwn.SurfacesFor(false); len(problems) != 1 {
		t.Errorf("the posture fold does not report the selected posture's malformed entry: %v", problems)
	}
}

// THE OWNER DECIDES WHERE THE FILE LANDS AND ITS FORMAT (NS-D21). Every posture patch spells
// `path` and `codec` (the surface schema requires them), and one aimed at another pack's
// surface that names a different file or codec than the owner declares would read, to its
// author, like keys written somewhere they are not. So a mismatch is a problem at the notch
// that places it; matching is the only shape that folds.
func TestAPostureOverlayMustNameTheOwnersPathAndCodec(t *testing.T) {
	for field, patch := range map[string]json.RawMessage{
		"path": json.RawMessage(`[{"agent":"pi","name":"settings","codec":"json",` +
			`"path":"~/.elsewhere.json","managed":{"k":1}}]`),
		"codec": json.RawMessage(`[{"agent":"pi","name":"settings","codec":"toml",` +
			`"path":"~/.pi/agent/settings.json","managed":{"k":1}}]`),
	} {
		set := Collect([]*packload.Pack{piOwnerPack(), postureConfigPack("matt", nil, patch)}, false, nil)
		if len(set.Problems) != 1 || !strings.Contains(set.Problems[0], field) ||
			!strings.Contains(set.Problems[0], "pack pi declares") {
			t.Errorf("%s mismatch: problems = %v, want one naming the owner's", field, set.Problems)
		}
		if set.For("pi", "settings") != nil {
			t.Errorf("%s mismatch: the patch was placed anyway", field)
		}
	}
}

// Once placed it is an ordinary overlay layer: a `config-overlay:<pack>` provenance label and
// config-overlay's precedence (below the owner's managed), because the fold keys on the pack.
func TestAPlacedPostureOverlayIsAnOrdinaryOverlayLayer(t *testing.T) {
	set := Collect([]*packload.Pack{piOwnerPack(), postureConfigPack("matt", nil,
		posturePatch(map[string]any{"owned": "by-matt", "hostOnly": true}, nil))}, false, nil)
	got := set.For("pi", "settings")
	if len(got) != 1 {
		t.Fatalf("overlays = %+v", got)
	}
	want := agentcfg.Overlay{Pack: "matt", Data: map[string]any{"owned": "by-matt", "hostOnly": true}}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("placed = %+v, want %+v", got[0], want)
	}
}
