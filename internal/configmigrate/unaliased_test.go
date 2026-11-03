package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"
)

// RC-D62: a conversion that writes variants for a provider and no alias says, once per provider,
// how those variants are selected and the one command that gives a short name back. The owner's
// `profiles.or` and `profiles.or-300k` became openrouter/or and openrouter/or-300k, and nothing
// named the next step.
func TestTheTranscriptNamesTheAliasCommandForAVariantWithoutOne(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude", "bedrock"],
  "profiles": {"or": {"provider": "bedrock", "model": "opus"}, "or-300k": {"provider": "bedrock", "model": "opus"}}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("plan unequal: %+v", p.Differences)
	}
	want := "bedrock: its variants are selected as bedrock/or and bedrock/or-300k; for one short name, `yolo config set providers.bedrock.alias or` (an alias starts on the provider's own `model`)."
	got := p.Transcript()
	if strings.Count(got, want) != 1 {
		t.Fatalf("transcript does not name the alias command once (want %q):\n%s", want, got)
	}
	if lines := UnaliasedLines(p); len(lines) != 1 || lines[0] != want {
		t.Fatalf("UnaliasedLines = %q, want the one line", lines)
	}

	// A profile named for its provider is the provider's own configuration, not a variant: no line.
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["claude", "bedrock"], "profiles": {"bedrock": {"provider": "bedrock", "model": "opus"}}}`+"\n")
	if got := plan(t, Request{UserConfigDir: dir}).Transcript(); strings.Contains(got, "for one short name") {
		t.Fatalf("a provider given no variant got the alias line:\n%s", got)
	}
}
