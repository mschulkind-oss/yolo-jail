package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// profileFixture is a pack that installs `claude`, declares an autonomy posture and one
// named profile selection, and carries the profile's OLD BODY as `profile`-modified
// contributions of the kinds that own it — the shape OQ-PT8's shrink left behind. The env
// and the config patch touch the same keys the posture and the pack's own static env do,
// which is the shape the fold order (autonomy, then gated, then the pack's own static)
// exists to resolve.
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
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"env","profile":"bedrock","vars":{"PROFILE":"from-profile","SHARED":"from-profile"}},
	  {"kind":"config-overlay","profile":"bedrock","surface":"claude/settings",
	   "config":{"managed":{"profile":"yes","auto":"profile"}}}]}`)}
}

// The gated env delivers when the profile is active, and the fixture's gated overlay
// contribution is present in the declaration set for the same selection — the two channels
// the old kind:profile body served. Their END-TO-END delivery is pinned in
// profileequivalence_test.go, which is an external test package precisely so it can reach
// both the env fold and the overlay collector at once.
func TestProfileEnvDeliversAndDeclarationCarriesTheOverlay(t *testing.T) {
	p := profileFixture(t)
	bedrock := map[string]string{"claude": "bedrock"}

	env := EnvVarsFor([]*Pack{p}, bedrock)
	if env["PROFILE"] != "from-profile" {
		t.Errorf("the gated env must deliver when the profile is active, got %v", env)
	}
	if env["STATIC"] != "from-env" {
		t.Errorf("a key the gated contribution does not name keeps the static value: %v", env)
	}

	ovs := p.Decl.ConfigOverlayContributions()
	if len(ovs) != 1 || ovs[0].Profile != "bedrock" || ovs[0].Surface != "claude/settings" {
		t.Fatalf("the fixture's gated overlay must decode with its gate and target, got %+v", ovs)
	}

	// The gate actually gates: no profile selected, nothing delivers.
	if got := EnvVarsFor([]*Pack{p}, nil); got["SHARED"] != "from-env" || got["PROFILE"] != "" {
		t.Errorf("no profile selected: static env only, got %v", got)
	}
	if got := EnvVarsFor([]*Pack{p}, map[string]string{"claude": "nobody"}); got["PROFILE"] != "" {
		t.Errorf("an undeclared profile selected must deliver nothing, got %v", got)
	}
}

// A gated env later-wins over the pack's own static `env` (OQ-8): the gate is the more
// specific intent, declared after the baseline, and overriding it is not a collision.
// (The shrink also retired the body's null-means-unset half — both maps are plain string
// maps now, so the fold is assignments only.)
func TestProfileEnvFoldsOverStatic(t *testing.T) {
	p := profileFixture(t)

	got := EnvVarsFor([]*Pack{p}, map[string]string{"claude": "bedrock"})
	if got["PROFILE"] != "from-profile" {
		t.Errorf("the profile's env must be folded in: %v", got)
	}
	if got["SHARED"] != "from-profile" {
		t.Errorf("the gated value must override the pack's own static value for the same key: %v", got)
	}
	if got["STATIC"] != "from-env" {
		t.Errorf("a key the gated contribution does not name keeps the static value: %v", got)
	}
}

// The env fold's GATE is two-pass, and the second pass is the fix: a pack that installs
// no CLI (packs/zai is the shipped one) can never satisfy a gate keyed to its own bins,
// so the wide pass asks whether the profile is active for ANY bin the launch installs.
// The first pass is why the narrow answer still wins when the pack DOES install the CLI.
func TestProfileEnvGateReachesACLIlessPack(t *testing.T) {
	zai := &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"zai","provider":"zai"},
	  {"kind":"env","profile":"zai","vars":{"ZAI_DELIVERED":"1"}}]}`)}
	pi := &Pack{Name: "pi", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"@acme/pi"}]}`)}

	// zai installs nothing, so only the wide pass can fire — keyed to the bin pi installs.
	got := EnvVarsFor([]*Pack{zai, pi}, map[string]string{"pi": "zai"})
	if got["ZAI_DELIVERED"] != "1" {
		t.Errorf("a CLI-less pack's gated env must fold when its profile is active for ANY "+
			"installed bin (the wide pass), got %v", got)
	}
	// Active for a bin NOBODY installs: no activation, gated env stays out.
	if got := EnvVarsFor([]*Pack{zai, pi}, map[string]string{"claude": "zai"}); got["ZAI_DELIVERED"] != "" {
		t.Errorf("a profile keyed to no installed CLI must not fold: %v", got)
	}
	// No table at all: the same.
	if got := EnvVarsFor([]*Pack{zai, pi}, nil); got["ZAI_DELIVERED"] != "" {
		t.Errorf("no profile selected must fold no gated env: %v", got)
	}
}

// EnvFold is the ordered sequence the HOST notch composes a process env from, so the
// order is the contract and not an implementation detail: static then gated PER PACK,
// keys sorted within each half. The winner that order produces across two packs is pinned
// at the consuming end (internal/cli hostfoldparity_test.go runs this fold and the host's
// over one fixture), because a fold tested only here is a fold whose caller can still
// apply it in a different order.
func TestEnvFoldIsPerPack(t *testing.T) {
	alpha := &Pack{Name: "alpha", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"env","vars":{"A2":"static","A1":"static"}},
	  {"kind":"env","profile":"p","vars":{"A1":"gated","SHARED":"gated"}}]}`)}
	beta := &Pack{Name: "beta", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"@acme/pi"},
	  {"kind":"env","vars":{"SHARED":"static","B1":"static"}}]}`)}

	type step struct {
		key, val string
	}
	var got []step
	for _, e := range EnvFold([]*Pack{alpha, beta}, map[string]string{"claude": "p"}) {
		got = append(got, step{e.Key, e.Value})
	}
	want := []step{
		{"A1", "static"}, {"A2", "static"}, // alpha's static, sorted
		{"A1", "gated"}, {"SHARED", "gated"}, // then alpha's own gated, sorted
		{"B1", "static"}, {"SHARED", "static"}, // then beta, static only
	}
	if len(got) != len(want) {
		t.Fatalf("EnvFold = %v, want %d steps", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("EnvFold[%d] = %v, want %v — the OQ-8 order is per pack, so the later "+
				"pack's static must come after the earlier pack's gated entry", i, got[i], want[i])
		}
	}
	if got[0].val != "static" {
		t.Errorf("no step may carry an unset operation: %+v", got[0])
	}

	// The reduction the jail notch consumes answers the same winner the sequence implies.
	if v := EnvVarsFor([]*Pack{alpha, beta}, map[string]string{"claude": "p"}); v["SHARED"] != "static" {
		t.Errorf("EnvVarsFor SHARED = %q, want beta's static (later pack) to beat alpha's gated entry", v["SHARED"])
	}
}

// A profile contributes no launch flag, and the shrink is why: the variant flags this
// fold used to take from a selected profile moved to a `profile`-modified kind:launch
// contribution, which the schema refuses today because nothing consumes it. The static
// flags and the autonomy posture's are the whole answer.
func TestLaunchFlagsTakeNoProfileBody(t *testing.T) {
	p := profileFixture(t)

	if got := LaunchFlagsFor([]*Pack{p}, true)["claude"]; len(got) != 1 || got[0] != "--static" {
		t.Errorf("the static flags stand whatever is selected, got %v", got)
	}
	// And the schema really does refuse the modifier on this kind — the sentence the
	// comment above rests on. (The schema half is packdecl's; this end keeps the two
	// honest together.)
	if _, probs := packdecl.Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"p","provider":"z"},
	  {"kind":"launch","profile":"p","bin":"claude","flags":["--bedrock"]}]}`)); len(probs) == 0 {
		t.Errorf("a profile-gated launch contribution must be refused until a consumer exists")
	}
}

// The footprint claims one row per declared profile, and the target carries BOTH the pack
// and the name — which is what keeps two packs answering to the same name from colliding
// (across packs there is nothing to combine). The claim says what the kind now IS: a
// selection of a provider, not a variant body.
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
		t.Errorf("a selection widens nothing the pack does not already ship; it is not review-worthy: %+v", got)
	}

	// Two packs shipping the same NAME are unrelated declarations, not a collision.
	other := &Pack{Name: "pi", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"pi"},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"}]}`)}
	if cols := Collisions([]*Pack{p, other}); len(cols) != 0 {
		t.Errorf("two packs sharing a profile NAME must not collide, got %v", cols)
	}
}
