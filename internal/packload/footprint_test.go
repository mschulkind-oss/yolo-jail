package packload

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// pk builds a *Pack with a manifest for testing the footprint shim.
func pk(name string, mayHost bool, m *packdecl.Manifest) *Pack {
	return &Pack{Name: name, Decl: m, MayAccessHost: mayHost}
}

// claimSet flattens a footprint to a set of "kind target" strings for assertion.
func claimSet(fp Footprint) map[string]Claim {
	out := map[string]Claim{}
	for _, c := range fp.Claims {
		out[string(c.Kind)+" "+c.Target] = c
	}
	return out
}

// FootprintOf maps each contribution kind to the right claim.
func TestFootprintMapsCurrentFields(t *testing.T) {
	surface, _ := json.Marshal([]map[string]any{
		{"agent": "claude", "name": "settings", "path": "~/.claude/settings.json", "codec": "json"},
	})
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindProgram, Bin: "claude", Via: "installer", URL: "https://x/install.sh"},
		{Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"},
		{Kind: packdecl.KindBriefing, From: "AGENTS.md", Into: ".claude/CLAUDE.md", After: "host:.claude/CLAUDE.md"},
		{Kind: packdecl.KindFiles, From: "prompts", Into: ".claude/prompts"},
		{Kind: packdecl.KindState, At: ".claude", Scope: "workspace"},
		{Kind: packdecl.KindState, At: ".claude-shared-credentials", Scope: "machine", Why: "shared creds"},
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
		{Kind: packdecl.KindLaunch, Bin: "claude", Flags: []string{"--dangerously-skip-permissions"}},
		{Kind: packdecl.KindHook, Hook: "shared_credentials"},
		{Kind: packdecl.KindConfig, Raw: surface},
	}}
	cs := claimSet(FootprintOf(pk("claude", true, m)))

	// program from install, review-worthy (installer URL).
	if c, ok := cs["program claude"]; !ok || !c.ReviewWorthy {
		t.Errorf("program claim missing or not review-worthy: %+v", c)
	}
	// mounts split by the magic string: skills → merge, AGENTS.md → briefing, else → files.
	if _, ok := cs["skills .claude/skills"]; !ok {
		t.Error("skills mount not mapped to KindSkills")
	}
	if c, ok := cs["briefing .claude/CLAUDE.md"]; !ok || !c.ReviewWorthy {
		t.Errorf("AGENTS.md mount not mapped to review-worthy KindBriefing (hostOverlay): %+v", c)
	}
	if _, ok := cs["files .claude/prompts"]; !ok {
		t.Error("opaque mount not mapped to KindFiles")
	}
	// surfaces → config keyed by agent/name.
	if _, ok := cs["config claude/settings"]; !ok {
		t.Error("surface not mapped to KindConfig 'claude/settings'")
	}
	// writableDirs → workspace state; sharedDirs → machine state (review-worthy).
	if c, ok := cs["state .claude"]; !ok || c.ReviewWorthy {
		t.Errorf("writableDirs should be non-review workspace state: %+v", c)
	}
	if c, ok := cs["state .claude-shared-credentials"]; !ok || !c.ReviewWorthy {
		t.Errorf("sharedDirs should be review-worthy machine state: %+v", c)
	}
	// hostFiles → reads-host (review-worthy) only because MayAccessHost.
	if c, ok := cs["reads-host .claude/settings.json"]; !ok || !c.ReviewWorthy {
		t.Errorf("hostFiles not mapped to review-worthy reads-host: %+v", c)
	}
	if _, ok := cs["launch claude"]; !ok {
		t.Error("launchFlags not mapped to KindLaunch")
	}
	if _, ok := cs["hook shared_credentials"]; !ok {
		t.Error("hook not mapped to KindHook")
	}
}

// A mount reads the host home, so it is origin-gated exactly like reads-host: an
// embedded/local pack's mount is HONORED, a fetched pack's is REFUSED with a
// reported message. The env kind is static and never gated.
func TestMountOriginGateAndEnvUngated(t *testing.T) {
	decl := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindMount, Host: "datasets/acme", Into: "acme-data"},
		{Kind: packdecl.KindEnv, Vars: map[string]string{"ACME_MODE": "fast"}},
	}}

	local := pk("local", true, decl)
	granted, refused := local.HonoredMounts()
	if len(granted) != 1 || len(refused) != 0 {
		t.Errorf("local pack: want its mount granted, got %d granted / %d refused", len(granted), len(refused))
	}

	fetched := pk("fetched", false, decl)
	granted, refused = fetched.HonoredMounts()
	if len(granted) != 0 || len(refused) != 1 {
		t.Errorf("fetched pack: want its mount refused, got %d granted / %d refused", len(granted), len(refused))
	}

	// env is honored regardless of origin (static values, no host read).
	if v := EnvVars([]*Pack{fetched}); v["ACME_MODE"] != "fast" {
		t.Errorf("env must be honored even for a fetched pack: %v", v)
	}

	// The mount claim is counted in the footprint only when host access is permitted
	// (matching what actually mounts).
	if _, ok := claimSet(FootprintOf(local))["mount datasets/acme"]; !ok {
		t.Error("local pack's mount should appear as a footprint claim")
	}
	if _, ok := claimSet(FootprintOf(fetched))["mount datasets/acme"]; ok {
		t.Error("fetched pack's mount must NOT appear as an honored footprint claim")
	}
}

// A pack whose origin does NOT permit host access does not get its reads-host
// claim counted (matches what actually gets mounted — a fetched pack's grant is
// refused upstream).
func TestFootprintOmitsHostReadsWhenOriginForbids(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
	}}
	cs := claimSet(FootprintOf(pk("fetched", false, m)))
	if _, ok := cs["reads-host .claude/settings.json"]; ok {
		t.Error("a non-host-permitted pack's hostFiles must not appear as an honored reads-host claim")
	}
}

// Two packs claiming the same sole-owned target collide; a merge/concat target
// does not (that is the feature).
func TestCollisionsExclusiveOnly(t *testing.T) {
	a := pk("a", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "a"},
		{Kind: packdecl.KindSkills, From: "skills", Into: ".x/skills"},
		{Kind: packdecl.KindFiles, From: "prompts", Into: ".x/data"},
	}})
	b := pk("b", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "b"}, // same bin → collision
		{Kind: packdecl.KindSkills, From: "skills", Into: ".x/skills"},      // same skills → fine
		{Kind: packdecl.KindFiles, From: "prompts", Into: ".x/data"},        // same files-target → collision
	}})
	cols := Collisions([]*Pack{a, b})

	got := map[string]Collision{}
	for _, c := range cols {
		got[string(c.Kind)+" "+c.Target] = c
	}
	if _, ok := got["program tool"]; !ok {
		t.Error("two packs claiming bin 'tool' should collide")
	}
	if _, ok := got["files .x/data"]; !ok {
		t.Error("two packs owning the same files path should collide")
	}
	if _, ok := got["skills .x/skills"]; ok {
		t.Error("two packs merging into one skills dir must NOT collide (that is the feature)")
	}
}

// State claimed at two different scopes (one workspace, one machine) collides;
// the same scope does not.
func TestCollisionsStateScope(t *testing.T) {
	ws := pk("ws", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindState, At: ".shared", Scope: "workspace"},
	}})
	mc := pk("mc", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindState, At: ".shared", Scope: "machine", Why: "x"},
	}})
	cols := Collisions([]*Pack{ws, mc})
	found := false
	for _, c := range cols {
		if c.Kind == packdecl.KindState && c.Target == ".shared" {
			found = true
		}
	}
	if !found {
		t.Error(".shared claimed as workspace in one pack and machine in another must collide")
	}

	// Same scope in both → no collision.
	a := pk("a", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindState, At: ".dup", Scope: "workspace"}}})
	b := pk("b", false, &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindState, At: ".dup", Scope: "workspace"}}})
	for _, c := range Collisions([]*Pack{a, b}) {
		if c.Target == ".dup" {
			t.Error("same-scope state on one path must not collide (it unions)")
		}
	}
}

// A single pack repeating a target (e.g. reads-host and the same path elsewhere)
// is not a cross-pack collision.
func TestCollisionsIgnoreSinglePack(t *testing.T) {
	a := pk("solo", false, &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "a"}},
	})
	if cols := Collisions([]*Pack{a}); len(cols) != 0 {
		t.Errorf("one pack cannot collide with itself, got %+v", cols)
	}
}

// The shipped embedded packs must have NO collisions — a regression guard on the
// real set (the good-citizen guarantee holds for what yolo ships).
func TestEmbeddedPacksNoCollisions(t *testing.T) {
	packs := Embedded()
	if len(packs) == 0 {
		t.Skip("no embedded packs registered in this test binary")
	}
	if cols := Collisions(packs); len(cols) != 0 {
		t.Errorf("shipped packs collide (they must not):\n%+v", cols)
	}
}
