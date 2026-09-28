package packload

// foldnote_test.go pins what the posture fold does with a config patch that names no surface
// its own pack declares: nothing. It used to drop such a patch silently (the OQ-Z5 shape,
// docs/reference/zai-plumbing.md: a `setings` typo produced no problem, no warning and no
// key), then to report it as a FoldNote. Since OQ-3 the patch is a POSTURE OVERLAY that
// packoverlay.Collect places on another pack's surface or reports as an orphan, so the fold
// only has to stay out of its way: no merge, no problem, no note.

import (
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

// A posture patch naming a surface its own pack does not declare is NOT this fold's any more
// (OQ-3, notch-scoped-config-contributions.md NS-D22). It used to merge into nothing here and
// come out as a FoldNote; since the ruling it is a POSTURE OVERLAY, packoverlay.Collect's to
// place on the owner's surface or report as an orphan, so this fold neither merges it, nor
// raises a problem, nor notes it. The typo'd identity is the OQ-Z5 case the note existed for;
// packoverlay's TestAnOwnerlessPostureOverlayIsAnOrphanOnlyWhereItsPostureIsSelected pins that
// it still reaches its author, as an orphan saying "check the identity".
func TestAForeignPosturePatchIsNotThisFoldsNote(t *testing.T) {
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
			t.Fatalf("%s posture: a foreign patch is not a problem of this fold: %v", c.posture, problems)
		}
		if len(surfaces) != 1 || surfaces[0].Key().String() != "claude/settings" {
			t.Fatalf("%s posture: the fold must not gain the patch's surface: %+v", c.posture, surfaces)
		}
		if m := surfaces[0].ManagedMap(); m["profile"] != nil {
			t.Errorf("%s posture: a foreign patch merged into the pack's own surface: %+v", c.posture, m)
		}
		if len(notes) != 0 {
			t.Errorf("%s posture: a foreign patch is packoverlay's to report, not a note here: %+v",
				c.posture, notes)
		}
	}
}

// PosturePatchesOwnSurface is the notch line's question about the OWN-SURFACE half of a posture's
// config (surveyNotchFacts): true only for a posture whose patch names a surface this pack
// declares, which always folds. A foreign patch alone answers false — whether it folds is the
// collector's answer (packoverlay.OverlaySet.PlacesPostureConfigFrom), not the manifest's.
func TestPosturePatchesOwnSurfaceAsksOnlyAboutTheOwnHalf(t *testing.T) {
	p := deadPatchFixture(t)
	for _, autonomy := range []bool{true, false} {
		if p.PosturePatchesOwnSurface(autonomy) {
			t.Errorf("autonomy=%v: a posture whose only patch is foreign reads as folding into "+
				"its own surface", autonomy)
		}
	}
	own := &Pack{Name: "acme", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"autonomy","guarded":{"config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"k":"v"}}]}}]}`)}
	if !own.PosturePatchesOwnSurface(false) || own.PosturePatchesOwnSurface(true) {
		t.Errorf("the guarded own-surface patch: guarded=%v autonomous=%v, want true and false",
			own.PosturePatchesOwnSurface(false), own.PosturePatchesOwnSurface(true))
	}
}
