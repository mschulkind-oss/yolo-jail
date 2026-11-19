package integration

import (
	"strings"
	"testing"
)

func TestDirectUserScopedRawRootNamesInJail(t *testing.T) {
	requireJail(t)
	for _, tc := range []struct {
		name, naming, delivered string
	}{
		{"bare", "bare = true", "corpus"},
		{"namespace", `namespace = "team"`, "team-corpus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scopedPackHome(t)
			scopedPackFile(t, root, "packs/corpus/SKILL.md", "ROOT-MARKER\n")
			scopedPackFile(t, root, "packs/corpus/child/SKILL.md", "support, not another registration\n")
			scopedPackFile(t, root, "packs/corpus/pack.toml", "malformed raw root [\n")
			scopedPackFile(t, root, "config.d/scoped.toml", "agent_updates = false\npacks = [\"local/reader\", {pack = \"local/corpus\", "+tc.naming+", skills = {dir = \".\"}}]\n")
			r := scopedPackRun(t, `set -eu
cat "$HOME/.fixture/skills/`+tc.delivered+`/SKILL.md"
test ! -e "$HOME/.fixture/skills/corpus-child"
test ! -e "$HOME/.fixture/skills/team-child"
printf 'RAW-ROOT-ONLY\n'
`)
			if !strings.Contains(r.stdout, "ROOT-MARKER") || !strings.Contains(r.stdout, "RAW-ROOT-ONLY") {
				t.Fatal(r.combined())
			}
		})
	}
}
