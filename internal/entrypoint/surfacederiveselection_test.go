package entrypoint

// surfacederiveselection_test.go pins that the SURFACE derive path resolves the
// selection its ctx exposes, exactly as bootprofile_test.go pins that the same loop
// folds the profile table into its surfaces.
//
// Same trap, one layer further down: the resolution lives at the CALL SITE
// (ConfigurePackSurfaces computing packload.ProviderFor per surface), so nothing fails
// if the loop stops setting it — the derive still runs, and a ctx field left empty reads
// in Lua as nil, which every derive treats as "no selection" and writes nothing. The
// next steps' selection derives (codex/pi/opencode) would then be dead code on the
// surface that is supposed to carry them, with the suite green. So the assertion is on
// the loop the entrypoint actually runs, over a derive that reports what its ctx held.
//
// The fixture derive reports BOTH fields, and the active-profile case asserts the
// selected provider value rather than the profile's name: the Lua a derive could write
// for itself (index ctx.use_profiles by its own agent name) answers "aws" here, where
// the resolution rule answers "aws-bedrock" — so a revert to re-deriving in Lua fails
// this test instead of passing it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// selectionDeriveLua is the fixture's whole derive: report what the ctx held, nothing
// else. Any transform here would blur which half of the value came from the ctx.
// `profile_model` is the one option ctx.profile can carry — the key the shipped derives
// read (packs/*/derive.lua) — reported raw so the assertion is on what the SURFACE path
// handed over and not on anything a derive might compute from it.
const selectionDeriveLua = `yolo.derive("acme", "settings", function(ctx)
  return {
    selected_provider = ctx.selected_provider or "",
    profile_name = ctx.profile_name or "",
    profile_model = (ctx.profile and ctx.profile.model) or "",
  }
end)
`

// selectionAcmePack installs the acme bin and owns ONE computed surface, so the boot
// loop renders it and the derive's report is the file's whole content. It declares NO
// profile: the profile the test selects lives on the other pack, which is the shipped
// shape (packs/zai declares the profile, packs/claude owns the surface) and the reason
// the resolution cannot be done from one pack's view.
func selectionAcmePack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), selectionDeriveLua)
	p, problems := packload.LoadDir(dir, "acme", false)
	if p == nil {
		t.Fatalf("the acme fixture did not load: %v", problems)
	}
	return p
}

// selectionProviderPack declares the profile the test selects, installing no CLI — so
// the profile is reachable only through the cross-pack half of packload.ProviderFor.
func selectionProviderPack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme-prov","description":"d","contributes":[
	  {"kind":"profile","name":"aws","provider":"aws-bedrock"}]}`)
	p, problems := packload.LoadDir(dir, "acme-prov", false)
	if p == nil {
		t.Fatalf("the provider fixture did not load: %v", problems)
	}
	return p
}

// renderAcmeSelection drives the BOOT LOOP over the two fixture packs with the given
// jail profile table and returns the rendered settings file parsed. wireProfiles is the
// resolved profile table a real launch lowers in as YOLO_PROFILES — the table
// activeProfileOptions reads to fill ctx.profile — and "" leaves it unset, the state of
// a launch that composed no profiles.
func renderAcmeSelection(t *testing.T, profiles, wireProfiles string) map[string]any {
	t.Helper()
	var errw bytes.Buffer
	vars := map[string]string{"YOLO_USE_PROFILES": profiles}
	if wireProfiles != "" {
		vars["YOLO_PROFILES"] = wireProfiles
	}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")

	ConfigurePackSurfaces(e, []*packload.Pack{selectionAcmePack(t), selectionProviderPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	return got
}

// A profile active at the surface agent's CLI name: the derive sees the provider the
// profile DELIVERS, not the profile's name — the distinction the resolution rule exists
// for, and the one a derive re-deriving from ctx.use_profiles in Lua would get wrong.
func TestSurfaceDeriveSeesTheResolvedProvider(t *testing.T) {
	got := renderAcmeSelection(t, `{"acme":"aws"}`, "")
	if got["selected_provider"] != "aws-bedrock" {
		t.Errorf("selected_provider = %v, want the profile's provider aws-bedrock — the "+
			"derive must read the resolved selection, not re-derive it from "+
			"ctx.use_profiles (which names the profile, aws):\n%v", got["selected_provider"], got)
	}
	if got["profile_name"] != "aws" {
		t.Errorf("profile_name = %v, want the active profile's name aws:\n%v", got["profile_name"], got)
	}
}

// THE OPTION HALF of the same ctx, at the same call site. profilesctx_test.go owns the
// exposure mechanics of ctx.profile (the parse, the always-a-table rule, the empty cases);
// what belongs here is the UNION this file is about — one render handing a derive BOTH
// halves of a cross-pack selection, the provider resolved through ProviderFor and the
// options read off YOLO_PROFILES. Every shipped selection derive reads exactly
// ctx.profile.model, so a call site that stopped passing Profile would leave it empty,
// every shipped derive would silently answer with the provider's `default` alias, and
// nothing that tested the resolution alone would know.
func TestSurfaceDeriveSeesTheResolvedProviderAndItsOptions(t *testing.T) {
	got := renderAcmeSelection(t, `{"acme":"aws"}`,
		`{"aws": {"provider": "aws-bedrock", "model": "fast"}}`)
	if got["profile_model"] != "fast" {
		t.Errorf("profile_model = %v, want the profile's own option value fast — the "+
			"surface derive path must hand the resolved option table to ctx.profile:\n%v",
			got["profile_model"], got)
	}
	if got["selected_provider"] != "aws-bedrock" {
		t.Errorf("selected_provider = %v, want aws-bedrock beside the option above",
			got["selected_provider"])
	}
}

// No profile at this agent's CLI name — no table, an empty one, or a selection naming a
// DIFFERENT agent: the ctx fields are empty, which is the derive's signal to write
// nothing (OQ-CS2). Another agent's selection leaking in here would write acme a
// selection its own launch never made.
func TestSurfaceDeriveSeesNoSelectionWhenNoneIsActive(t *testing.T) {
	for _, table := range []string{``, `{}`, `{"claude":"aws"}`} {
		got := renderAcmeSelection(t, table, "")
		if got["selected_provider"] != "" {
			t.Errorf("table %q: selected_provider = %v, want empty — no profile is active "+
				"at acme's CLI name (OQ-CS2: the no-profile case is the agent's own)",
				table, got["selected_provider"])
		}
		if got["profile_name"] != "" {
			t.Errorf("table %q: profile_name = %v, want empty", table, got["profile_name"])
		}
	}
}

// A profile active but declared by NO pack: the resolution falls back to the bare name,
// the convention the composed providers table has always keyed on
// (use_profiles.acme = "mystery" reaching providers.mystery).
func TestSurfaceDeriveFallsBackToTheProfileName(t *testing.T) {
	got := renderAcmeSelection(t, `{"acme":"mystery"}`, "")
	if got["selected_provider"] != "mystery" {
		t.Errorf("selected_provider = %v, want the undeclared profile's own name mystery "+
			"— the convention ProviderFor keeps for a profile no pack declares:\n%v",
			got["selected_provider"], got)
	}
}
