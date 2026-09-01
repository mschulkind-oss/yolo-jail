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
// profile variant whose config patch targets `claude/setings` — the typo, letter for letter.
func deadPatchFixture(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "acme", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"profile","name":"bedrock",
	   "config":[{"agent":"claude","name":"setings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"profile":"yes"}}]}]}`)}
}

func TestDeadProfilePatchIsNamedNotSilent(t *testing.T) {
	p := deadPatchFixture(t)

	surfaces, problems, notes := p.SurfacesForReport(true, map[string]string{"claude": "bedrock"})
	if len(problems) != 0 {
		t.Fatalf("a patch naming no base surface is INERT, not a problem: %v", problems)
	}
	if len(surfaces) != 1 {
		t.Fatalf("the fold must not gain a surface, got %d: %+v", len(surfaces), surfaces)
	}
	if m := surfaces[0].ManagedMap(); m["profile"] != nil {
		t.Errorf("the fold's disposition stays `ignored` — nothing merges, got %+v", m)
	}
	if len(notes) != 1 {
		t.Fatalf("want exactly one note for the one dead patch, got %+v", notes)
	}
	if got := notes[0].Target.String(); got != "claude/setings" {
		t.Errorf("the note must carry the patch's own (agent,name), got %q", got)
	}
	msg := notes[0].String()
	for _, want := range []string{"acme", "bedrock", "claude/setings", "claude/settings"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the note is missing %q — the reader needs the pack, the declaration the "+
				"patch rode, what was named, and what the pack actually declares: %q", want, msg)
		}
	}
}

// The other half of the pin: a patch that DOES match raises nothing, so the note stays a
// signal rather than a line every profile prints.
func TestLiveProfilePatchRaisesNoNote(t *testing.T) {
	p := profileFixture(t)

	_, problems, notes := p.SurfacesForReport(true, map[string]string{"claude": "bedrock"})
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(notes) != 0 {
		t.Fatalf("a patch that folded must not be reported inert: %+v", notes)
	}
}

// An UNSELECTED variant folds nothing at all, so it cannot miss anything either — the note
// is about a fold that happened and found no base, not about a declaration somebody might
// select later. (`yolo check`'s manifest lint is where a never-selected variant's dead patch
// belongs, not the render path.)
func TestUnselectedProfileRaisesNoNote(t *testing.T) {
	p := deadPatchFixture(t)

	_, _, notes := p.SurfacesForReport(true, nil)
	if len(notes) != 0 {
		t.Fatalf("no variant was selected, so nothing folded and nothing is inert: %+v", notes)
	}
}

// The autonomy posture rides the same fold, so it gets the same note — and it is the ONLY
// fold the host render can produce a miss from, because a host apply passes no profile table
// (a variant is a launch decision). A posture naming a surface its own pack does not declare
// is the same dead letter a profile's is.
func TestDeadPosturePatchIsNamedToo(t *testing.T) {
	p := &Pack{Name: "acme", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"pi","name":"settings","codec":"json",
	     "path":"~/.pi/agent/settings.json","managed":{"auto":"yes"}}]},
	   "guarded":{"config":[{"agent":"pi","name":"settings","codec":"json",
	     "path":"~/.pi/agent/settings.json","managed":{"auto":"no"}}]}}]}`)}

	for _, c := range []struct {
		autonomy bool
		posture  string
	}{
		{true, "autonomous"},
		{false, "guarded"},
	} {
		surfaces, problems, notes := p.SurfacesForReport(c.autonomy, nil)
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
