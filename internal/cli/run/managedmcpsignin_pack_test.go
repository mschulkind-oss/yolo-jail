package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestManagedMCPJailLayerUsesDeclaredIdentityGuard(t *testing.T) {
	oldSources, oldTargets := jailcontent.PackSkillDirs(), jailcontent.PackSkillTargets()
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(oldSources); jailcontent.SetPackSkillTargets(oldTargets) })
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	manifest := `name="fixture"
skills_tier="namespaced"
[[mcp_sign_in_bundle]]
bundle="skills/original"
format="claude-plugin"
origin="https://example.invalid/original"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/original/LICENSE"
[[skills]]
from="skills"
`
	for rel, body := range map[string]string{"pack.toml": manifest, "skills/original/LICENSE": "MIT License", "skills/original/.claude-plugin/plugin.json": `{"name":"original"}`, "skills/original/.mcp.json": `{"server":{"url":"https://mcp.fixture.invalid/mcp"}}`} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	pack, ps := packload.LoadDirForUse(root, "fixture")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	sources, ps := pack.SkillsSources()
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	file := filepath.Join(root, "skills/original/.mcp.json")
	if err := os.Rename(file, file+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".mcp.json.real", file); err != nil {
		t.Fatal(err)
	}
	jailcontent.SetPackSkillDirs(jailSkillSources(pack, sources))
	jailcontent.SetPackSkillTargets([]jailcontent.SkillTarget{{Staging: "fixture", Dest: ".native/skills", Agent: "fixture"}})
	_, report, err := jailcontent.PrepareSkillsWith("fixture-launch", nil)
	if err != nil {
		t.Fatal(err)
	}
	refused := false
	for _, notice := range report.PackNotices {
		refused = refused || strings.Contains(notice.Detail, "sign-in bundle not delivered")
	}
	if !refused {
		t.Fatalf("real jail layer converter lost identity guard: %+v", report.PackNotices)
	}
}

func TestUserSuppliedMCPBundleIsAPluginNotAServerOrInstaller(t *testing.T) {
	root := managedSignInFixture(t, t.TempDir(), false)
	pack, problems := packload.LoadDirForUse(root, "fixture-mcp")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if len(pack.InstallBins()) != 0 {
		t.Fatal("integration pack installs a program")
	}
	composed := packload.ComposeMCPServers(jsonx.NewOrderedMap(), []*packload.Pack{pack}, "/fixture/home")
	if len(composed.Keys()) != 0 {
		t.Fatal("integration pack contributed a server")
	}
	body, err := os.ReadFile(filepath.Join(pack.Root, "skills", "fixture", "LICENSE"))
	if err != nil || !strings.Contains(string(body), "MIT License") {
		t.Fatalf("fixture license absent: %v", err)
	}
	points, problems := pack.HonoredPlugins()
	if len(problems) != 0 || len(points) != 1 || points[0].Name() != "fixture" {
		t.Fatalf("whole user-supplied plugin not recognized: %v %v", points, problems)
	}
}
