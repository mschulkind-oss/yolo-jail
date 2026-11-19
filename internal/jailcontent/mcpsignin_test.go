package jailcontent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestManagedMCPBundleThroughPrepareSkillsPreservesIdentityAndRefusals(t *testing.T) {
	oldSources, oldTargets := PackSkillDirs(), PackSkillTargets()
	t.Cleanup(func() { SetPackSkillDirs(oldSources); SetPackSkillTargets(oldTargets) })
	for _, tc := range []struct {
		name, namespace string
		flat, linked    bool
	}{{name: "bare"}, {name: "namespaced", namespace: "team"}, {name: "flat", flat: true}, {name: "linked identity", linked: true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			packRoot := t.TempDir()
			manifest := `name="fixture"
skills_tier="namespaced"
[[mcp_sign_in_bundle]]
bundle="skills/original"
format="claude-plugin"
origin="https://example.invalid/original"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/original/LICENSE"
`
			native := `{"mcpServers":{"server":{"url":"https://mcp.fixture.invalid/mcp"}}}`
			files := map[string]string{"pack.toml": manifest, "skills/original/LICENSE": "MIT License", "skills/original/.claude-plugin/plugin.json": `{"name":"original","mcpServers":".mcp.json","future":{"retained":true}}`, "skills/original/.mcp.json": native}
			for rel, body := range files {
				file := filepath.Join(packRoot, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			pack, ps := packload.LoadDirForUse(packRoot, "fixture")
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			pack.Namespace = tc.namespace
			layer := hostskills.PackLayer(pack)
			if tc.flat {
				layer.Tier = hostskills.TierFlat
			}
			if tc.linked {
				file := filepath.Join(packRoot, "skills/original/.mcp.json")
				if err := os.Rename(file, file+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(".mcp.json.real", file); err != nil {
					t.Fatal(err)
				}
			}
			SetPackSkillDirs([]PackSkillSource{{Pack: layer.Pack, Namespace: layer.Namespace, Tier: layer.Tier, Plugins: layer.Plugins, SignInFormats: layer.SignInFormats}})
			SetPackSkillTargets([]SkillTarget{{Staging: "fixture", Dest: ".native/skills", Agent: "fixture"}})
			staging, report, err := PrepareSkillsWith("fixture-launch", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.flat || tc.linked {
				refused := false
				for _, notice := range report.PackNotices {
					refused = refused || strings.Contains(notice.Detail, "sign-in bundle not delivered")
				}
				if !refused {
					t.Fatalf("caller lost refusal: %+v", report.PackNotices)
				}
				return
			}
			name := "original"
			if tc.namespace != "" {
				name = tc.namespace + "-" + name
			}
			delivered := filepath.Join(staging, "fixture", name)
			data, err := os.ReadFile(filepath.Join(delivered, ".mcp.json"))
			if err != nil || string(data) != native {
				t.Fatalf("root native bytes changed: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(delivered, ".claude-plugin/plugin.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got["name"] != name || got["x-yolo-managed-by"] != "yolo-jail" || got["x-yolo-pack"] != "fixture" || got["mcpServers"] != ".mcp.json" || got["future"] == nil {
				t.Fatalf("native manifest changed: %s", body)
			}
		})
	}
}
