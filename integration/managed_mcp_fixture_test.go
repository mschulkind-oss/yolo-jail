package integration

import (
	"os"
	"path/filepath"
	"testing"
)

const managedMCPFixtureManifest = `name="fixture-mcp"
skills_tier="namespaced"
[[mcp_sign_in_bundle]]
bundle="skills/fixture"
format="claude-plugin"
origin="https://example.invalid/fixture-plugin"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/fixture/LICENSE"
callback_route=true
[[skills]]
from="skills"
agents=["claude"]
`

var managedMCPFixtureFiles = map[string]string{
	"LICENSE":                    "MIT License\nSynthetic integration fixture only\n",
	".claude-plugin/plugin.json": `{"name":"fixture"}`,
	".mcp.json":                  `{"mcpServers":{"fixture":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":31234}}}}`,
}

func managedMCPIntegrationFixture(t *testing.T, parent string) string {
	t.Helper()
	root := filepath.Join(parent, "fixture-mcp")
	files := map[string]string{"pack.toml": managedMCPFixtureManifest}
	for rel, body := range managedMCPFixtureFiles {
		files["skills/fixture/"+rel] = body
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
