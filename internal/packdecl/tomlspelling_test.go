package packdecl

import (
	"strings"
	"testing"
)

// A pack.toml problem speaks pack.toml: the body named the JSON kind (`kind "mcp"`), which a
// TOML author cannot find, since the manifest spells it as a `[[mcp]]` table.
func TestATOMLManifestProblemNamesTheTOMLGroup(t *testing.T) {
	_, problems := DecodeTOML([]byte("[[mcp]]\nconfig = { command = [\"x\"] }\n"))
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "[[mcp]] needs \"name\"") {
		t.Errorf("the problem does not name the TOML group:\n%s", joined)
	}
	if strings.Contains(joined, `kind "mcp"`) {
		t.Errorf("the problem still names the JSON kind:\n%s", joined)
	}
}

// The rewrite names a later contribution by its group key, and leaves a word with no TOML
// group as written.
func TestTOMLSpellingRewritesKindsAndIndexes(t *testing.T) {
	groups := []groupedContribution{{location: ManifestLocation{Key: "program[0]"}}, {location: ManifestLocation{Key: "blocked_tool[0]"}}}
	got := tomlSpelling(`pack.toml: kind "blocked-tool" clashes with contributes[1]; kind "config-overlay"; kind "nonsense"; contributes[7]`, groups)
	want := `pack.toml: [[blocked_tool]] clashes with blocked_tool[0]; [[overlay]]; kind "nonsense"; contributes[7]`
	if got != want {
		t.Errorf("tomlSpelling =\n %s\nwant\n %s", got, want)
	}
}
