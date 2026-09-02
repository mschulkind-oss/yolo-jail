package packload

import "testing"

// providerfor_test.go pins the one provider-naming rule both notches and (later) the
// credential preflight resolve through: given the profile active at a CLI name, WHICH
// provider does the launch deliver?

// zaiProviderPack ships the zai provider and a profile NAMED SOMETHING ELSE that selects
// it, and installs no CLI at all. The name mismatch is load-bearing: with the profile
// named "zai" the profile-name fallback would return the same string and the assertion
// could not tell a resolved selection from a lucky fallback.
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

// TestProviderForNamesTheSelectedProvider pins the whole rule: the provider a declared
// profile selects wins, the bin owner's own declaration winning over another pack's; and
// with no declaration anywhere the profile's own name is the answer, because that is the
// convention the composed table has always keyed on (use_profiles.claude = "bedrock"
// reaching providers.bedrock).
func TestProviderForNamesTheRequiredProvider(t *testing.T) {
	zai := zaiProviderPack(t)
	claude := claudePack(t)

	if got := ProviderFor([]*Pack{claude, zai}, "claude", "glm"); got != "zai" {
		t.Errorf("a provider pack's selection must reach the agent pack's CLI: got %q", got)
	}
	// The profile name carries when nothing requires anything — the bedrock case.
	if got := ProviderFor([]*Pack{claude}, "claude", "bedrock"); got != "bedrock" {
		t.Errorf("an undeclared profile names its own provider: got %q", got)
	}
	// No variant active, no provider at all.
	if got := ProviderFor([]*Pack{claude, zai}, "claude", ""); got != "" {
		t.Errorf("no active variant means no provider, got %q", got)
	}
	// A CLI no selected pack installs: the declaration still carries, because the name is
	// global and the provider pack is a receiver of the table even with no CLI to key on.
	if got := ProviderFor([]*Pack{zai}, "pi", "glm"); got != "zai" {
		t.Errorf("a declaration on a CLI-less pack still resolves: got %q", got)
	}
}

// TestProviderForPrefersTheBinOwnersOwnDeclaration: when the agent pack declares a
// profile of the same name, ITS selection is the more specific intent and must win over a
// provider pack's.
func TestProviderForPrefersTheBinOwnersOwnDeclaration(t *testing.T) {
	claude := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},
	  {"kind":"profile","name":"glm","provider":"own-zai"}]}`)}
	if got := ProviderFor([]*Pack{claude, zaiProviderPack(t)}, "claude", "glm"); got != "own-zai" {
		t.Errorf("the bin owner's own selection should win: got %q", got)
	}
	// Order of the pack list must not decide it.
	if got := ProviderFor([]*Pack{zaiProviderPack(t), claude}, "claude", "glm"); got != "own-zai" {
		t.Errorf("the bin owner's own selection should win regardless of pack order: got %q", got)
	}
}
