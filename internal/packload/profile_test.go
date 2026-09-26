package packload

import (
	"strings"
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
	  {"kind":"autonomy",
	   "autonomous":{"launch":[{"bin":"claude","flags":["--auto"]}],
	    "config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
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

	env := EnvVarsFor([]*Pack{p}, bedrock, "claude")
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
	if got := EnvVarsFor([]*Pack{p}, nil, "claude"); got["SHARED"] != "from-env" || got["PROFILE"] != "" {
		t.Errorf("no profile selected: static env only, got %v", got)
	}
	if got := EnvVarsFor([]*Pack{p}, map[string]string{"claude": "nobody"}, "claude"); got["PROFILE"] != "" {
		t.Errorf("an undeclared profile selected must deliver nothing, got %v", got)
	}
}

// A gated env later-wins over the pack's own static `env` (providers.md#pv-oq-8): the gate is the more
// specific intent, declared after the baseline, and overriding it is not a collision.
// (The shrink also retired the body's null-means-unset half — both maps are plain string
// maps now, so the fold is assignments only.)
func TestProfileEnvFoldsOverStatic(t *testing.T) {
	p := profileFixture(t)

	got := EnvVarsFor([]*Pack{p}, map[string]string{"claude": "bedrock"}, "claude")
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

// A pack that installs no CLI (packs/zai, aws-auth) can never satisfy a gate keyed to its
// own bins, so its gated env reaches EVERY agent that selected its profile — and, since the
// per-agent gate (OQ-BR4), only those: the shared fold and an agent that selected nothing
// get none of it.
func TestProfileEnvGateReachesACLIlessPack(t *testing.T) {
	zai := &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"profile","name":"zai","provider":"zai"},
	  {"kind":"env","profile":"zai","vars":{"ZAI_DELIVERED":"1"}}]}`)}
	pi := &Pack{Name: "pi", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"@acme/pi"}]}`)}
	codex := &Pack{Name: "codex", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"codex","via":"npm","package":"@acme/codex"}]}`)}
	packs := []*Pack{zai, pi, codex}
	table := map[string]string{"pi": "zai"}

	if got := EnvVarsFor(packs, table, "pi"); got["ZAI_DELIVERED"] != "1" {
		t.Errorf("a CLI-less pack's gated env must reach the agent that selected its "+
			"profile, got %v", got)
	}
	if got := EnvVarsFor(packs, table, "codex"); got["ZAI_DELIVERED"] != "" {
		t.Errorf("an agent that did not select the profile must not receive it: %v", got)
	}
	if got := EnvVarsFor(packs, table, ""); got["ZAI_DELIVERED"] != "" {
		t.Errorf("the SHARED fold — what every process sees — must carry no gated env: %v", got)
	}
	// Active for a bin NOBODY installs: no activation, gated env stays out.
	if got := EnvVarsFor(packs, map[string]string{"claude": "zai"}, "claude"); got["ZAI_DELIVERED"] != "" {
		t.Errorf("a profile keyed to no installed CLI must not fold: %v", got)
	}
	// No table at all: the same.
	if got := EnvVarsFor(packs, nil, "pi"); got["ZAI_DELIVERED"] != "" {
		t.Errorf("no profile selected must fold no gated env: %v", got)
	}
}

// TRAP D2 (docs/design/provider-credential-scope.md §2.6): an AGENT pack's gated env reaches
// its own agent and nobody else. `-p codex=bedrock` in a jail that also installs claude used
// to fire claude's CLAUDE_CODE_USE_BEDROCK jail-wide through the launch-wide second pass;
// under the per-agent gate neither codex, claude nor the shared fold receives it, and claude
// receives it only when claude itself selected bedrock.
func TestAnAgentPacksGatedEnvReachesOnlyItsOwnAgent(t *testing.T) {
	claude := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},
	  {"kind":"profile","name":"bedrock","provider":"bedrock"},
	  {"kind":"env","profile":"bedrock","vars":{"CLAUDE_CODE_USE_BEDROCK":"1"}}]}`)}
	codex := &Pack{Name: "codex", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"codex","via":"npm","package":"@acme/codex"}]}`)}
	packs := []*Pack{claude, codex}

	codexOnly := map[string]string{"codex": "bedrock"}
	for _, agent := range []string{"codex", "claude", ""} {
		if got := EnvVarsFor(packs, codexOnly, agent); got["CLAUDE_CODE_USE_BEDROCK"] != "" {
			t.Errorf("-p codex=bedrock: fold for %q carries claude's gated flag — trap D2 is "+
				"open again: %v", agent, got)
		}
	}
	if got := EnvVarsFor(packs, map[string]string{"claude": "bedrock"}, "claude"); got["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude selecting bedrock must still receive its own gated flag, got %v", got)
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
	for _, e := range EnvFold([]*Pack{alpha, beta}, map[string]string{"claude": "p"}, "claude") {
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
	if v := EnvVarsFor([]*Pack{alpha, beta}, map[string]string{"claude": "p"}, "claude"); v["SHARED"] != "static" {
		t.Errorf("EnvVarsFor SHARED = %q, want beta's static (later pack) to beat alpha's gated entry", v["SHARED"])
	}
}

// A profile contributes no launch flag: since the OQ-PT8 shrink a profile is a SELECTION over
// a provider and carries no body at all, so the selected notch's autonomy posture is the whole
// answer — the same flags whichever profile is active.
func TestLaunchFlagsTakeNoProfileBody(t *testing.T) {
	p := profileFixture(t)

	for _, profiles := range []string{"no selection", "bedrock selected"} {
		if got := LaunchFlagsFor([]*Pack{p}, true)["claude"]; len(got) != 1 || got[0] != "--auto" {
			t.Errorf("%s: the posture's flags stand whatever is selected, got %v", profiles, got)
		}
	}
	// And the kind a variant's flags used to live on is gone rather than merely unconsumed:
	// `launch` is refused with its replacement named, on the authoring path. (The schema half
	// is packdecl's; this end keeps the two honest together.)
	_, probs := packdecl.Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"profile","name":"p","provider":"z"},
	  {"kind":"launch","profile":"p","bin":"claude","flags":["--bedrock"]}]}`))
	if len(probs) == 0 {
		t.Fatal("a `kind: \"launch\"` contribution must be refused — the kind is retired")
	}
	if !strings.Contains(strings.Join(probs, "\n"), "autonomy") {
		t.Errorf("the refusal must name the replacement kind, got %v", probs)
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
