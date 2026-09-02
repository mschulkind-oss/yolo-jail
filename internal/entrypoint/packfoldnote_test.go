package entrypoint

// packfoldnote_test.go pins the REPORT at both render paths, which is the part a packload
// unit test cannot see: a problem a fold computes and no render path prints is the finding
// again, one layer down. So these drive the production entries — the boot loop and the host
// render — and assert on what the user is told, which is what fails if the emission site is
// deleted (the unpinned-callee class this review's finding #2 is about).
//
// TWO mechanisms report a patch that names nothing, and OQ-PT8 moved the border between
// them. A posture patch naming no base surface is still a FOLD NOTE (packload's
// foldPostureManaged, reported by the render that runs the fold — the host path below). A
// profile's config half is no longer such a patch at all: since the kind shrank, it is a
// `config-overlay` gated on the profile, and a dead target there is an ORPHAN in
// packoverlay's own report, fired by reportOverlayResolution in the boot loop — the jail
// path below. The two spellings look alike on stderr and are checked by different code,
// which is exactly why both call sites are pinned.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// THE HOST PATH: `yolo host apply` passes no profile table — a profile is a launch decision
// — so the dead patch it CAN see is a posture's, and it reaches the user through the result
// (the host Env has no Stderr; see tableLosses). A row that says nothing was written, for an
// identity the pack does not declare.
func TestHostRenderReportsPatchNamingNoSurface(t *testing.T) {
	for _, c := range []struct {
		typo  bool
		want  bool
		label string
	}{
		{true, true, "a dead patch"},
		{false, false, "a patch that folds"},
	} {
		p := posturePatchPack(t, c.typo)
		results, err := RenderHostPack(p, t.TempDir(), true, nil)
		if err != nil {
			t.Fatalf("%s: RenderHostPack: %v", c.label, err)
		}
		var found bool
		for _, r := range results {
			if r.Surface != "claude/setings" {
				continue
			}
			found = true
			if !strings.HasPrefix(r.Action, "ignored:") {
				t.Errorf("%s: the row must say nothing was written, got %q", c.label, r.Action)
			}
		}
		if found != c.want {
			t.Errorf("%s: want a result row for the unmatchable identity=%v, got %+v",
				c.label, c.want, results)
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
