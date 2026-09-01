package packload

// configexclusive_test.go covers Option 1 of docs/design/pack-config-collaboration.md:
// `config` is CombineExclusive by surface IDENTITY, which the footprint model documented and
// nothing enforced.
//
// The hazard being closed (ruling R1, "very harmful … this is a general mechanism"):
// manifest.Merge resolves two declarations of one identity last-writer-wins, WHOLE — so the
// survivor brings its own mode/path/codec/defaults, and a second pack can flip a surface from
// `stateful` to `rmw`, silently disabling in-jail edit capture for a file it does not own.
// The tests below pin that this is now a named collision, that the two legitimate shapes are
// NOT (an `autonomy` patch of the pack's own surface, and an overlay alongside a config), and
// that the shipped six stay clean.

import (
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// surfacePack builds a pack declaring one config surface, with mode left to the caller
// (empty inherits stateful, as a real pack.json does).
func surfacePack(t *testing.T, name, agent, surface, mode, managed string) *Pack {
	t.Helper()
	modeField := ""
	if mode != "" {
		modeField = `"mode":"` + mode + `",`
	}
	return &Pack{Name: name, Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"`+agent+`","name":"`+surface+`","codec":"json",
	     "path":"~/.`+agent+`/`+surface+`.json",`+modeField+`"managed":`+managed+`}]}]}`)}
}

// collisionFor returns the collision reported for one target, or "" when there is none.
func collisionFor(cols []Collision, target string) string {
	for _, c := range cols {
		if c.Target == target {
			return c.Reason
		}
	}
	return ""
}

// TWO PACKS, ONE IDENTITY is the case R1 is about: refused, naming both packs, with the
// remedy (`config-overlay`) in the message — because the remedy now exists, so a refusal
// that does not teach it just blocks a setup that used to work.
func TestTwoPacksOneSurfaceCollidesNamingBoth(t *testing.T) {
	owner := surfacePack(t, "claude", "claude", "settings", "", `{"preferences":{"x":1}}`)
	// mode:"rmw" is the concrete damage — it would replace the owner's stateful surface.
	fzf := surfacePack(t, "claude-fzf", "claude", "settings", "rmw", `{"fileSuggestion":"x"}`)

	cols := ConfigSurfaceCollisions([]*Pack{owner, fzf})
	if len(cols) != 1 {
		t.Fatalf("want exactly one collision for one shared identity, got %d: %+v", len(cols), cols)
	}
	got := cols[0]
	if strings.Join(got.Packs, ",") != "claude,claude-fzf" {
		t.Errorf("both packs must be named, sorted: %v", got.Packs)
	}
	for _, want := range []string{
		"claude", "claude-fzf", // both culprits
		"config-overlay",    // the correct expression
		"\"surface\":",      // …shown as a shape the author can copy
		"mode",              // what silently changed
		"\"stateful\"",      // from
		"\"rmw\"",           // to
		"yolo config diff",  // where the provenance shows up afterwards
		"exactly ONE owner", // the rule
	} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("collision message missing %q — it has to name both packs, the damage, and "+
				"the conversion, since a user hitting this should not need the design doc; got:\n%s",
				want, got.Reason)
		}
	}
	// And it reaches the general footprint report, which is what `yolo pack footprint` and
	// `yolo check` print.
	if collisionFor(Collisions([]*Pack{owner, fzf}), "claude/settings") == "" {
		t.Error("the identity clash must appear in Collisions(), not only in the dedicated pass")
	}
}

// ONE PACK, ONE IDENTITY TWICE is a self-collision, and it is reported for a reason the
// `files` equivalent shares (packDestConflicts reports a single pack claiming one path
// twice): the resolution is just as silent within one manifest as across two. The wording
// must not invent a second pack.
func TestOnePackDeclaringOneSurfaceTwiceCollides(t *testing.T) {
	selfish := &Pack{Name: "selfish", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	     "path":"~/.acme/settings.json","managed":{"a":1}}]},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	     "path":"~/.acme/settings.json","mode":"rmw","managed":{"b":2}}]}]}`)}

	cols := ConfigSurfaceCollisions([]*Pack{selfish})
	if len(cols) != 1 {
		t.Fatalf("a pack declaring one identity twice must collide with itself, got %d: %+v",
			len(cols), cols)
	}
	reason := cols[0].Reason
	if !strings.Contains(reason, "selfish") {
		t.Errorf("the self-collision must name the pack: %s", reason)
	}
	if strings.Contains(reason, "packs selfish and") || strings.Contains(reason, "the other's") {
		t.Errorf("a one-pack collision must not imply a second pack; got:\n%s", reason)
	}
	// The divergence report cannot label both sides "selfish" — it distinguishes by
	// declaration index instead, or the reader has no way to tell them apart.
	if !strings.Contains(reason, "declaration 1") || !strings.Contains(reason, "declaration 2") {
		t.Errorf("a self-collision's divergence must be labelled by declaration, not by a "+
			"pack name that is the same on both sides; got:\n%s", reason)
	}
}

// AUTONOMY IS NOT A SECOND DECLARATION. A posture patches the managed layer of a surface the
// SAME pack owns (foldPostureManaged merges into the base rather than appending), which is
// exactly the shape all five shipped agent packs use — so mistaking it for a second `config`
// would refuse every launch.
func TestAutonomyPatchingOwnSurfaceIsNotACollision(t *testing.T) {
	p := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"preferences":{"x":1}}}]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"claude","name":"settings","codec":"json",
	      "path":"~/.claude/settings.json","managed":{"skipDangerousModePermissionPrompt":true}}]},
	   "guarded":{"config":[{"agent":"claude","name":"settings","codec":"json",
	      "path":"~/.claude/settings.json","managed":{"skipDangerousModePermissionPrompt":false}}]}}]}`)}

	if cols := ConfigSurfaceCollisions([]*Pack{p}); len(cols) != 0 {
		t.Fatalf("an autonomy posture patching the pack's OWN surface is a notch-gated managed "+
			"patch, not a second writer — it must not collide; got: %+v", cols)
	}
	// True at either notch: the posture only ever patches surfaces the pack already declares,
	// so the identity SET is notch-independent even though the keys are not.
	if s, _ := p.SurfacesFor(false, nil); len(s) != 1 {
		t.Errorf("the guarded posture must not add a surface identity, got %d", len(s))
	}
}

// AN OVERLAY ALONGSIDE A CONFIG IS THE SUPPORTED SHAPE — Layout C. It must not collide, or
// Option 1 would refuse the very expression Option 2 shipped to make correct.
func TestOverlayPlusConfigOnOneIdentityIsFine(t *testing.T) {
	owner := surfacePack(t, "claude", "claude", "settings", "", `{"preferences":{"x":1}}`)
	contributor := &Pack{Name: "claude-fzf", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config-overlay","surface":"claude/settings",
	   "config":{"managed":{"fileSuggestion":"x"}}}]}`)}

	if cols := ConfigSurfaceCollisions([]*Pack{owner, contributor}); len(cols) != 0 {
		t.Fatalf("config + config-overlay on one identity is the DESIGNED shape and must not "+
			"collide; got: %+v", cols)
	}
	if cols := Collisions([]*Pack{owner, contributor}); len(cols) != 0 {
		t.Fatalf("the same holds in the general footprint report; got: %+v", cols)
	}
	// The overlay's claim is REPORTED though — a footprint that omitted it would leave the
	// good-citizen report unable to show what a pack contributes to someone else's file.
	found := false
	for _, c := range FootprintOf(contributor).Claims {
		if c.Kind == "config-overlay" && c.Target == "claude/settings" {
			found = true
		}
	}
	if !found {
		t.Error("a config-overlay contribution must appear as a footprint claim (it was omitted " +
			"while the kind was inert; now that it applies, the omission is a real gap)")
	}
}

// DISTINCT IDENTITIES DO NOT COLLIDE, including two surfaces of one agent — the ordinary
// case (claude owns both claude/config and claude/settings), so the check has to stay keyed
// on the full identity rather than the agent.
func TestDistinctSurfaceIdentitiesDoNotCollide(t *testing.T) {
	a := surfacePack(t, "claude", "claude", "settings", "", `{"x":1}`)
	b := surfacePack(t, "other", "claude", "config", "rmw", `{"y":2}`)
	if cols := ConfigSurfaceCollisions([]*Pack{a, b}); len(cols) != 0 {
		t.Errorf("two DIFFERENT surfaces of one agent must not collide; got: %+v", cols)
	}
}

// THE SHIPPED SIX must not collide with each other, or the new pre-flight refuses every
// launch. This is the finding-vs-route-around gate: if it ever fails, the answer is to fix
// the packs, not to weaken the check.
//
// Materialized from packs.FS directly rather than via Embedded(), which needs the
// packreg side-effect import this test binary cannot have (that import is the cycle
// packreg exists to break — see embeddrift_test.go, which reads packs.FS for the same
// reason). Skipping instead would make this the one test that cannot fail.
func TestShippedPacksHaveNoConfigSurfaceCollision(t *testing.T) {
	shipped, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	if len(shipped) == 0 {
		t.Fatal("no shipped packs found — this test would otherwise pass vacuously")
	}
	if cols := ConfigSurfaceCollisions(shipped); len(cols) != 0 {
		t.Fatalf("the packs yolo SHIPS collide on a config surface identity, so every launch "+
			"selecting them would now be refused:\n%+v", cols)
	}
	// The whole footprint too, since that is what `yolo check` refuses on.
	if cols := Collisions(shipped); len(cols) != 0 {
		t.Fatalf("shipped packs collide in the footprint report:\n%+v", cols)
	}
}
