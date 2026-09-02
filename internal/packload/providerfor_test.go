package packload

import "testing"

// providerfor_test.go pins the one provider-naming rule both derive paths resolve
// through: given a profile name and the launch's RESOLVED table, which provider does the
// selection deliver?
//
// The table is the WHOLE input. The pack manifests are what fed the lowering
// (ResolveProfiles) and are not what this reads, so every case here is a case about a
// table — one ResolveProfiles produced where the lowering is under test, a hand-built one
// standing in for it where only the lookup is. The distinction is load-bearing: the
// manifest walk this lookup replaced answered only names a PACK declared, so a
// user-declared profile launched cleanly and still selected nothing.

// zaiProviderPack ships the zai provider and a profile NAMED SOMETHING ELSE that selects
// it, and installs no CLI at all — the shipped packs/zai shape, and the reason the
// manifest walk could never be the answer for a name the user coined.
func zaiProviderPack(t *testing.T) *Pack {
	return &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}}},
	  {"kind":"profile","name":"glm","provider":"zai"}]}`)}
}

// claudePack installs the claude CLI and declares no variant of its own.
func claudePack(t *testing.T) *Pack {
	return &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"}]}`)}
}

// TestProviderForReadsTheResolvedTableNotTheManifests is the regression pin: a name only
// the USER declared is in no manifest, so the manifest walk this replaced fell back to the
// bare name — "zai-fast", a provider that does not exist — and a profile the launch
// accepted selected nothing. The table's entry is the answer, and an absent entry is EMPTY
// rather than the name back: an empty selection is the derive's signal to write nothing,
// while the bare name would index a provider the table does not hold.
func TestProviderForReadsTheResolvedTableNotTheManifests(t *testing.T) {
	userDeclared := map[string]ResolvedProfile{"zai-fast": {Provider: "zai"}}
	if got := ProviderFor(userDeclared, "zai-fast"); got != "zai" {
		t.Errorf("a user-declared profile must resolve to the provider it names: got %q", got)
	}
	if got := ProviderFor(userDeclared, "ghost"); got != "" {
		t.Errorf("a name the table does not hold must resolve to nothing, got %q", got)
	}
	if got := ProviderFor(nil, "zai-fast"); got != "" {
		t.Errorf("no table at all is no selection, got %q", got)
	}
	if got := ProviderFor(userDeclared, ""); got != "" {
		t.Errorf("no active profile means no provider, got %q", got)
	}
}

// TestProviderForAnswersWhatResolveProfilesResolved drives the real lowering over a mix of
// a pack-declared name and a user-declared one, so the lookup and the resolution cannot
// grow two ideas of what a name means — and so a caller cannot satisfy the pin with a
// table that drops either half of the declared set.
func TestProviderForAnswersWhatResolveProfilesResolved(t *testing.T) {
	packs := []*Pack{claudePack(t), zaiProviderPack(t)}
	resolved, err := ResolveProfiles(packs, map[string]UserProfile{
		// The user's own name over the pack's provider — the case the manifest walk
		// answered with "zai-fast".
		"zai-fast": {Provider: "zai"},
		// The user RE-POINTING the pack's own profile at another provider — the case the
		// manifest walk answered with the PACK's selection, "zai", over the user's head.
		"glm": {Provider: "zai-eu"},
	})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got := ProviderFor(resolved, "zai-fast"); got != "zai" {
		t.Errorf("the user's own name should resolve to its provider: got %q", got)
	}
	if got := ProviderFor(resolved, "glm"); got != "zai-eu" {
		t.Errorf("the user's re-pointing of a pack-declared name must win: got %q", got)
	}

	// With no user entry at all, the pack's own declaration is what lands in the table —
	// the shipped `-p glm` case, which must keep working through the table.
	resolved, err = ResolveProfiles(packs, nil)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got := ProviderFor(resolved, "glm"); got != "zai" {
		t.Errorf("a pack-declared profile should resolve to its provider: got %q", got)
	}
}
