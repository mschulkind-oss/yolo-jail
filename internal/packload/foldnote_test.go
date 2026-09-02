package packload

// foldnote_test.go pins the one thing the fold used to do silently: drop a config patch
// that names no surface its own pack declares.
//
// That drop is the OQ-Z5 shape (docs/design/zai-plumbing.md): a patch written for a claude
// surface, moved into a pack that owns no claude surface, merges into nothing and — before
// the note existed — looked to its author exactly like a patch that had folded. The review
// that found it verified the silence directly: a `setings` typo produced no problem, no
// warning, and no key.
//
// What these pin is the DISPOSITION as much as the report: the fold still adds no surface
// and raises no problem (an inert declaration is not a broken one), so a reader who sees a
// note knows nothing was written and nothing refused — only that the declaration goes
// nowhere.

import (
	"strings"
	"testing"
)

// deadPatchFixture is a pack that installs `claude` and declares ONE surface, plus a
// posture whose config patch targets `claude/setings` — the typo, letter for letter.
func deadPatchFixture(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "acme", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"claude","name":"setings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"profile":"yes"}}]},
	   "guarded":{"config":[{"agent":"claude","name":"setings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"profile":"no"}}]}}]}`)}
}

// gatedOverlayFixture is the same pack with the profile's old patch in its new home: a
// `config-overlay` contribution gated on the profile, targeting the same typo'd identity.
// It exists to pin that THIS fold never sees it — the miss is packoverlay's orphan to
// report, not a note of this fold's.
func gatedOverlayFixture(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "acme", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"config-overlay","profile":"bedrock","surface":"claude/setings",
	   "config":{"managed":{"profile":"yes"}}}]}`)}
}

// The profile's old variant patch is gone from this fold (OQ-PT8): it moved to a
// `config-overlay` contribution, which composes where every other overlay does —
// packoverlay.Collect — and whose miss is an ORPHAN report there, not a note here. The
// pin is that the move is complete: this fold neither merges nor reports it.
func TestProfilePatchIsNoLongerThisFold(t *testing.T) {
	p := gatedOverlayFixture(t)

	surfaces, problems, notes := p.SurfacesForReport(true)
	if len(problems) != 0 {
		t.Fatalf("a gated overlay naming no base surface is INERT here, not a problem: %v", problems)
	}
	if len(surfaces) != 1 {
		t.Fatalf("the fold must not gain a surface, got %d: %+v", len(surfaces), surfaces)
	}
	if m := surfaces[0].ManagedMap(); m["profile"] != nil {
		t.Errorf("the overlay's keys must not merge into the owner's managed layer: %+v", m)
	}
	if len(notes) != 0 {
		t.Fatalf("a gated overlay is nobody's note to write here: %+v", notes)
	}
	// The same miss, at the fold that owns it: the orphan report, fired only when the
	// profile IS active — pinned in profileequivalence_test.go, the external test package
	// that can reach both folds at once (packoverlay imports this one).
}

// The autonomy posture rides the same fold, so it gets the same note — and it is the ONLY
// fold the host render can produce a miss from, because a host apply passes no profile
// table (a profile is a launch decision). A posture naming a surface its own pack does not
// declare is the same dead letter a profile's gated overlay is, and deadPatchFixture is
// its shape.
func TestDeadPosturePatchIsNamedToo(t *testing.T) {
	p := deadPatchFixture(t)

	for _, c := range []struct {
		autonomy bool
		posture  string
	}{
		{true, "autonomous"},
		{false, "guarded"},
	} {
		surfaces, problems, notes := p.SurfacesForReport(c.autonomy)
		if len(problems) != 0 {
			t.Fatalf("%s posture: a dead patch is not a problem: %v", c.posture, problems)
		}
		if len(surfaces) != 1 || surfaces[0].Key().String() != "claude/settings" {
			t.Fatalf("%s posture: the fold must not gain the patch's surface: %+v", c.posture, surfaces)
		}
		if len(notes) != 1 {
			t.Fatalf("%s posture: want the dead patch named, got %+v", c.posture, notes)
		}
		if !strings.Contains(notes[0].String(), c.posture) {
			t.Errorf("%s posture: the note must say which declaration the patch rode: %q",
				c.posture, notes[0].String())
		}
	}
}
