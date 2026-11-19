package run

import (
	"os"
	"path/filepath"
	"testing"
)

// A user-supplied integration, never part of the shipped catalog. Its root MCP
// content exercises native identity without a provider recipe.
func managedSignInFixture(t *testing.T, parent string, projectedSkill bool) string {
	t.Helper()
	root := filepath.Join(parent, "fixture-mcp")
	files := map[string]string{
		"pack.toml": `name="fixture-mcp"
skills_tier="namespaced"
[native_sign_in]
formats=["claude-plugin"]
[[skills]]
from="skills"
agents=["claude"]
[[mcp_sign_in_bundle]]
bundle="skills/fixture"
format="claude-plugin"
origin="https://example.invalid/fixture-plugin"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/fixture/LICENSE"
callback_route=true
`,
		"skills/fixture/LICENSE":                    "MIT License\nSynthetic fixture only\n",
		"skills/fixture/.claude-plugin/plugin.json": `{"name":"fixture"}`,
		"skills/fixture/.mcp.json":                  `{"mcpServers":{"fixture":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":31234}}}}`,
	}
	if projectedSkill {
		files["skills/fixture-api/SKILL.md"] = "synthetic projected skill\n"
	}
	for rel, body := range files {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
