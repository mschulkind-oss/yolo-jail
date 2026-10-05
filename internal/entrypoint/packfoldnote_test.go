package entrypoint

// packfoldnote_test.go pins the REPORT at both render paths, which is the part a packload
// unit test cannot see: a problem a fold computes and no render path prints is the finding
// again, one layer down. So these drive the production entries — the boot loop and the host
// render — and assert on what the user is told, which is what fails if the emission site is
// deleted (the unpinned-callee class this review's finding #2 is about).
//
// ONE mechanism reports a patch that names nothing now: packoverlay's ORPHAN. OQ-PT8 moved a
// profile's config half there (a `config-overlay` gated on the profile, reported by
// reportOverlayResolution in the boot loop — the jail path below), and OQ-3 moved a posture's
// patch on a surface its pack does not declare there too (a posture overlay — the host path
// below). The fold note that used to report the second is gone.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// gatedOverlayPack is a pack that installs `claude`, declares ONE surface, and carries the
// profile's config half in its shrunken home: a `config-overlay` gated on the profile,
// targeting `claude/setings` when typo is true (the review's verification, letter for
// letter) and the surface the pack really declares when it is false.
func gatedOverlayPack(t *testing.T, typo bool) *packload.Pack {
	t.Helper()
	name := "settings"
	if typo {
		name = "setings"
	}
	base, err := json.Marshal([]any{map[string]any{
		"agent": "claude", "name": "settings", "codec": "json",
		"path": "~/.claude/settings.json", "managed": map[string]any{"base": "surface"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "@acme/claude"},
			{Kind: packdecl.KindConfig, Raw: base},
			{Kind: packdecl.KindProfile, Name: "bedrock", Provider: "bedrock"},
			{
				Kind:    packdecl.KindConfigOverlay,
				Surface: "claude/" + name,
				Profile: "bedrock",
				Raw:     json.RawMessage(`{"managed":{"profile":"yes"}}`),
			},
		},
	}}
}

// THE JAIL PATH: a selected profile whose overlay names nothing the pack declares is
// reported on the boot's stderr, by target — and never as a generator failure, because the
// render succeeded and the overlay was merely inert.
//
// The third case is the shrink's other half: with the profile NOT active the gate is a clean
// skip, and the same typo'd target says nothing at all — the orphan report fires for the
// reason that actually stopped the contribution, and an unselected profile is not a reason.
func TestJailRenderWarnsOnGatedOverlayNamingNoSurface(t *testing.T) {
	for _, c := range []struct {
		profiles string
		typo     bool
		want     bool
		label    string
	}{
		{`{"claude":"bedrock"}`, true, true, "a dead overlay under a selected profile"},
		{`{"claude":"bedrock"}`, false, false, "an overlay that lands"},
		{`{"claude":"nobody"}`, true, false, "the same dead overlay with the profile unselected"},
		{``, true, false, "the same dead overlay with no selection at all"},
	} {
		e, errw := overlayRenderEnv(t)
		e.Vars["YOLO_USE_PROFILES"] = c.profiles
		ConfigurePackSurfaces(e, []*packload.Pack{gatedOverlayPack(t, c.typo)})
		if fails := e.GenFailures(); len(fails) != 0 {
			t.Fatalf("%s: an inert overlay is not a render failure: %v", c.label, fails)
		}
		if got := errw.String(); strings.Contains(got, "claude/setings") != c.want {
			t.Errorf("%s (profiles %s): want warning=%v, boot said:\n%s",
				c.label, c.profiles, c.want, got)
		}
	}
}

// …and the report names the remedy, not just the target: the typo has no owner anywhere, so
// the line says to check the identity rather than to select a pack that does not exist.
func TestJailRenderSaysWhyTheGatedOverlayIsDead(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	e.Vars["YOLO_USE_PROFILES"] = `{"claude":"bedrock"}`
	ConfigurePackSurfaces(e, []*packload.Pack{gatedOverlayPack(t, true)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("render failed: %v", fails)
	}
	got := errw.String()
	for _, want := range []string{"no effect", "check the identity", "pack acme"} {
		if !strings.Contains(got, want) {
			t.Errorf("the orphan report should name %q, boot said:\n%s", want, got)
		}
	}
}

// THE HOST PATH, since OQ-3: a guarded posture patch naming an identity its pack does not
// declare is a POSTURE OVERLAY, so the collector the apply runs reports it — an `autonomy`
// orphan that says to check the identity, which apply.go prints — and the host render writes
// nothing for it and no longer adds a row of its own. A patch on the pack's own surface still
// folds into that surface.
func TestHostRenderLeavesAPatchNamingNoSurfaceToTheCollector(t *testing.T) {
	for _, c := range []struct {
		typo  bool
		label string
	}{
		{true, "a dead patch"},
		{false, "a patch that folds"},
	} {
		p := posturePatchPack(t, c.typo)
		set := packoverlay.Collect([]*packload.Pack{p}, false, nil)
		orphaned := len(set.Orphans) == 1 && set.Orphans[0].KindName() == "autonomy" &&
			set.Orphans[0].Target == "claude/setings" &&
			strings.Contains(set.Orphans[0].Reason(), "check the identity")
		if orphaned != c.typo {
			t.Errorf("%s: want the collector's autonomy orphan=%v, got %+v", c.label, c.typo, set.Orphans)
		}
		home := t.TempDir()
		results, err := RenderHostPack(p, home, render.OwnershipOwn, false, set, nil)
		if err != nil {
			t.Fatalf("%s: RenderHostPack: %v", c.label, err)
		}
		for _, r := range results {
			if r.Surface == "claude/setings" {
				t.Errorf("%s: the host render grew a row for the unmatchable identity: %+v", c.label, r)
			}
		}
		if got := set.For("claude", "setings"); got != nil {
			t.Errorf("%s: a patch on an identity nothing owns was placed: %+v", c.label, got)
		}
	}
}

// posturePatchPack is a pack whose GUARDED posture patches `claude/setings` (typo) or the
// surface it really declares. The host notch renders the guarded posture, which is what makes
// this the case a host apply can see.
func posturePatchPack(t *testing.T, typo bool) *packload.Pack {
	t.Helper()
	name := "settings"
	if typo {
		name = "setings"
	}
	patch := func(agent, name string) json.RawMessage {
		raw, err := json.Marshal([]any{map[string]any{
			"agent": agent, "name": name, "codec": "json",
			"path": "~/.claude/settings.json", "managed": map[string]any{"auto": "yes"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfig, Raw: patch("claude", "settings")},
			{Kind: packdecl.KindAutonomy,
				Autonomous: &packdecl.AutonomyPosture{Config: patch("claude", "settings")},
				Guarded:    &packdecl.AutonomyPosture{Config: patch("claude", name)}},
		},
	}}
}
