package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// profileFixture is a pack that installs `claude`, declares an autonomy posture and one
// named profile variant, all touching the same surface / bin / env keys — which is the
// shape the fold order (autonomy, then profile, then the pack's own static) exists to
// resolve.
func profileFixture(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"base":"surface"}}]},
	  {"kind":"env","vars":{"STATIC":"from-env","SHARED":"from-env"}},
	  {"kind":"launch","bin":"claude","flags":["--static"]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
	     "managed":{"auto":"yes"}}]},
	   "guarded":{"config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
	     "managed":{"auto":"no"}}]}},
	  {"kind":"profile","name":"bedrock",
	   "config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
	     "managed":{"profile":"yes","auto":"profile"}}],
	   "env":{"PROFILE":"from-profile","SHARED":null},
	   "launch":[{"bin":"claude","flags":["--bedrock"]}]}]}`)}
}

// The profile fold rides SurfacesFor and lands AFTER the autonomy posture's, so a
// later-wins on a key both touch goes to the profile (§3.4, OQ-8). A key the profile does
// not name keeps the posture's value, and no profile selected leaves the posture alone.
func TestProfileConfigFoldsAfterAutonomy(t *testing.T) {
	p := profileFixture(t)
	bedrock := map[string]string{"claude": "bedrock"}

	got, probs := p.SurfacesFor(true, bedrock)
	if len(probs) != 0 {
		t.Fatalf("SurfacesFor(true, bedrock) problems: %v", probs)
	}
	m := got[0].ManagedMap()
	if m["base"] != "surface" {
		t.Errorf("the surface's own managed key must survive both folds: %+v", m)
	}
	if m["profile"] != "yes" {
		t.Errorf("the selected profile's key must be folded in: %+v", m)
	}
	// The load-bearing assertion: the SAME key, touched by both the posture and the
	// variant, reads the profile's value.
	if m["auto"] != "profile" {
		t.Errorf("the profile must fold AFTER autonomy (later-wins), got auto=%v: %+v", m["auto"], m)
	}

	// No profile selected (nil table, or a name this pack does not declare): the posture
	// is the last word, exactly as before the kind existed.
	for _, table := range []map[string]string{nil, {"claude": "nobody"}} {
		got, probs := p.SurfacesFor(true, table)
		if len(probs) != 0 {
			t.Fatalf("SurfacesFor(true, %v) problems: %v", table, probs)
		}
		if m := got[0].ManagedMap(); m["auto"] != "yes" || m["profile"] != nil {
			t.Errorf("an unselected profile must fold nothing, got %+v", m)
		}
	}

	// The host notch: the guarded posture, then the profile on top.
	got, _ = p.SurfacesFor(false, bedrock)
	if m := got[0].ManagedMap(); m["auto"] != "profile" || m["profile"] != "yes" {
		t.Errorf("guarded posture + profile should compose, got %+v", m)
	}
}

// A profile's env later-wins over the pack's own static `env` (OQ-8), and a null in it
// REMOVES the key rather than setting it empty — which is why the fold is not a plain map
// merge: a null that arrived as "" would have been indistinguishable from a real value.
func TestProfileEnvFoldsOverStatic(t *testing.T) {
	p := profileFixture(t)

	if got := EnvVars([]*Pack{p}); got["SHARED"] != "from-env" || got["PROFILE"] != "" {
		t.Errorf("no profile selected: static env only, got %v", got)
	}
	got := EnvVarsFor([]*Pack{p}, map[string]string{"claude": "bedrock"})
	if got["PROFILE"] != "from-profile" {
		t.Errorf("the profile's env must be folded in: %v", got)
	}
	// OQ-8: the variant overrides its own default — and here the override is a null, so
	// the override REMOVES the key rather than setting it empty (OQ-7).
	if _, present := got["SHARED"]; present {
		t.Errorf("a null in the profile UNSETS the key (OQ-7) — it must not survive as \"\" "+
			"or as a present key: %v", got)
	}
	if got["STATIC"] != "from-env" {
		t.Errorf("a key the profile does not name keeps the static value: %v", got)
	}
}

// A profile's launch flags later-wins over the pack's own static `launch` for the same
// bin (OQ-8) — the same rule the env fold applies, so the two halves cannot disagree.
func TestProfileLaunchFoldsOverStatic(t *testing.T) {
	p := profileFixture(t)
	bedrock := map[string]string{"claude": "bedrock"}

	if got := LaunchFlagsFor([]*Pack{p}, true, nil)["claude"]; len(got) != 1 || got[0] != "--static" {
		t.Errorf("no profile selected: the static flags stand, got %v", got)
	}
	got := LaunchFlagsFor([]*Pack{p}, true, bedrock)["claude"]
	if len(got) != 1 || got[0] != "--bedrock" {
		t.Errorf("the profile's flags must replace the static ones for the same bin, got %v", got)
	}
}

// The selector is the CLI name, so a profile keyed to a bin the pack does not install
// gates nothing — the pack owning the CLI is what makes the variant ITS variant (§3.3).
func TestProfileIsGatedByThePackOwnCLI(t *testing.T) {
	p := profileFixture(t)
	// `pi` is not a bin this pack installs.
	got := EnvVarsFor([]*Pack{p}, map[string]string{"pi": "bedrock"})
	if got["PROFILE"] != "" {
		t.Errorf("a profile keyed to another pack's CLI must not fold: %v", got)
	}
	if _, probs := p.SurfacesFor(true, map[string]string{"pi": "bedrock"}); len(probs) != 0 {
		t.Errorf("unexpected problems: %v", probs)
	} else if m, _ := p.SurfacesFor(true, map[string]string{"pi": "bedrock"}); m[0].ManagedMap()["profile"] != nil {
		t.Errorf("a profile keyed to another pack's CLI must not fold: %+v", m[0].ManagedMap())
	}
}

// The footprint claims one row per declared variant, and the target carries BOTH the pack
// and the name — which is what keeps two packs answering to the same name from colliding
// (§3.4: across packs there is nothing to combine).
func TestProfileFootprintClaim(t *testing.T) {
	p := profileFixture(t)
	var got *Claim
	for _, c := range FootprintOf(p).Claims {
		if c.Kind == packdecl.KindProfile {
			got = &c
		}
	}
	if got == nil {
		t.Fatal("a profile declaration produced no footprint claim at all")
	}
	if got.Target != "claude/bedrock" || got.Pack != "claude" {
		t.Errorf("the claim target must be (pack, name), got %+v", got)
	}
	if got.ReviewWorthy {
		t.Errorf("a variant widens nothing the pack does not already ship; it is not review-worthy: %+v", got)
	}

	// Two packs shipping the same NAME are unrelated declarations, not a collision.
	other := &Pack{Name: "pi", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"pi"},
	  {"kind":"profile","name":"bedrock","env":{"X":"1"}}]}`)}
	if cols := Collisions([]*Pack{p, other}); len(cols) != 0 {
		t.Errorf("two packs sharing a profile NAME must not collide, got %v", cols)
	}
}
