package packload_test

// profileequivalence_test.go is THE EQUIVALENCE PIN for OQ-PT8's shrink, and it lives in
// an EXTERNAL test package on purpose: the old kind:profile body delivered one profile's
// keys through two channels owned by two packages — its `env` map through packload's pack
// env fold, its `config` patch through packoverlay's overlay slot — so a pin in either
// package alone could not see the other channel lose its delivery. Importing both from
// outside is the only shape that asserts the pair at once.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// bedrockFixture is the shape packs/claude ships, reduced to the keys the pin checks: one
// `kind: profile` selection, and the body the kind used to carry split across the two
// contributions that own the two channels.
func bedrockFixture(t *testing.T) []*packload.Pack {
	t.Helper()
	m, probs := packdecl.Decode([]byte(`{"name":"claude","contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"env","profile":"bedrock","vars":{"CLAUDE_CODE_USE_BEDROCK":"1"}},
	  {"kind":"config-overlay","profile":"bedrock","surface":"claude/settings",
	   "config":{"managed":{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}}}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture manifest invalid: %v", probs)
	}
	return []*packload.Pack{{Name: "claude", Decl: m}}
}

// Before the shrink, `-p bedrock` delivered CLAUDE_CODE_USE_BEDROCK=1 twice: once as a
// pack env var, once as a managed key on claude/settings. The split has to reproduce BOTH,
// gated by the same selection, or one of the two channels the old body served has silently
// lost its consumer — which is exactly the failure class a mechanism swap risks and a
// per-package test cannot see.
func TestShrunkenProfileDeliversBothChannelsTheOldBodyDid(t *testing.T) {
	packs := bedrockFixture(t)
	bedrock := map[string]string{"claude": "bedrock"}

	// Channel 1: the pack env fold, as the jail notch consumes it.
	env := packload.EnvVarsFor(packs, bedrock)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("channel 1 (pack env fold) must deliver CLAUDE_CODE_USE_BEDROCK=1, got %v", env)
	}

	// Channel 2: the overlay slot, folded by the same collector every other overlay uses.
	set := packoverlay.Collect(packs, true, bedrock)
	if len(set.Problems) != 0 {
		t.Fatalf("overlay problems: %v", set.Problems)
	}
	if len(set.Orphans) != 0 {
		t.Fatalf("the gated overlay must find its owner: %+v", set.Orphans)
	}
	placed := set.For("claude", "settings")
	if len(placed) != 1 {
		t.Fatalf("channel 2 (claude/settings overlay) must be placed exactly once, got %+v", placed)
	}
	data, ok := placed[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("the overlay body must decode to an object, got %T", placed[0].Data)
	}
	managedEnv, ok := data["env"].(map[string]any)
	if !ok || managedEnv["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Fatalf("channel 2 must fold managed.env.CLAUDE_CODE_USE_BEDROCK=1, got %v", data["env"])
	}

	// The gate is one selection for both channels: nothing selected, neither delivers.
	if got := packload.EnvVarsFor(packs, nil); got["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Errorf("no profile selected: the env half must not deliver, got %v", got)
	}
	if got := packoverlay.Collect(packs, true, nil).For("claude", "settings"); len(got) != 0 {
		t.Errorf("no profile selected: the overlay half must not be placed, got %+v", got)
	}
	if got := packload.EnvVarsFor(packs, map[string]string{"claude": "nobody"}); got["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Errorf("an undeclared profile selected: the env half must not deliver, got %v", got)
	}
	if got := packoverlay.Collect(packs, true, map[string]string{"claude": "nobody"}).
		For("claude", "settings"); len(got) != 0 {
		t.Errorf("an undeclared profile selected: the overlay half must not be placed, got %+v", got)
	}
}

// The typo'd target of an ACTIVE gated overlay is the orphan report — the one channel the
// old variant patch's dead target never had. Pinned here because the assertion spans both
// folds: this fold (packload) must not report it, and the overlay fold must.
func TestGatedOverlayMissSurfacesAsAnOrphanNotAFoldNote(t *testing.T) {
	m, probs := packdecl.Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"config-overlay","profile":"bedrock","surface":"claude/setings",
	   "config":{"managed":{"profile":"yes"}}}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture manifest invalid: %v", probs)
	}
	packs := []*packload.Pack{{Name: "acme", Decl: m}}

	for _, p := range packs {
		if _, problems, notes := p.SurfacesForReport(true); len(problems) != 0 || len(notes) != 0 {
			t.Fatalf("the posture fold must not report an overlay's miss: problems=%v notes=%v",
				problems, notes)
		}
	}
	set := packoverlay.Collect(packs, true, map[string]string{"claude": "bedrock"})
	if len(set.Orphans) != 1 || set.Orphans[0].Target != "claude/setings" {
		t.Fatalf("the typo'd target must surface as an orphan once the profile is active, got %+v", set.Orphans)
	}
	if got := packoverlay.Collect(packs, true, nil); len(got.Orphans) != 0 {
		t.Errorf("an inactive profile is a CLEAN skip, not an orphan: %+v", got.Orphans)
	}
}

// The gated overlay folds BELOW the owner's managed layer, which is the one observable
// difference the move made: the old fold merged into Managed itself, so a conflicting key
// read the profile's value; the slot reads the owner's. Pinned so the precedence change is
// a recorded consequence of the shrink rather than a surprise a reader rediscovers.
func TestGatedOverlaySitsBelowTheOwnersManagedLayer(t *testing.T) {
	packs := bedrockFixture(t)

	var surfaces []manifest.Surface
	for _, p := range packs {
		got, probs := p.SurfacesFor(true)
		if len(probs) != 0 {
			t.Fatalf("surface problems: %v", probs)
		}
		surfaces = append(surfaces, got...)
	}
	if len(surfaces) != 1 {
		t.Fatalf("want the pack's one surface, got %+v", surfaces)
	}
	if surfaces[0].ManagedMap()["CLAUDE_CODE_USE_BEDROCK"] != nil {
		t.Errorf("the gated overlay must NOT merge into the owner's managed layer — it composes "+
			"one precedence below, so the owner keeps the last word: %+v", surfaces[0].ManagedMap())
	}
	if surfaces[0].ManagedMap()["base"] != "surface" {
		t.Errorf("the surface's own managed keys are untouched by the shrink: %+v", surfaces[0].ManagedMap())
	}
}
