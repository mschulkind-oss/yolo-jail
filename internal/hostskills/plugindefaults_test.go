package hostskills

// plugindefaults_test.go pins delivery for a wrapped plugin whose components sit at Claude Code's
// DEFAULT locations with no manifest entry (hooks/hooks.json, .mcp.json, monitors/monitors.json,
// bin/, workflows/, and the rest of pluginpacktest.EveryDefaultLocation). The verbatim copy
// brings every one of them, so a namespaced delivery must name each that runs code, and a flat
// one must refuse each by name AND keep it out of the copy. It also pins which manifest values
// that copy reads as paths: every path a component field declares, and no setting's value.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

var defaultLocationCode = []string{"hooks", "mcpServers", "monitors", "bin", "workflows"}

// defaultsPluginReq is testPluginReq over the every-default-location fixture, with manifest
// written over the fixture's when non-empty.
func defaultsPluginReq(t *testing.T, tier Tier, manifest string) PluginRequest {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteEveryDefaultLocationPlugin(t, dir, "acme-tools")
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"),
			[]byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pl, ok := pluginpack.Load(dir)
	if !ok {
		t.Fatal("the fixture is not plugin-shaped")
	}
	return PluginRequest{
		Pack: "wrapper", Plugin: pl, Tier: tier,
		SkillsDir:   filepath.Join(t.TempDir(), ".claude", "skills"),
		Composed:    &Manifest{Entries: map[string]string{}},
		Claimed:     map[string]string{},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20261003-000000",
	}
}

// A namespaced delivery names each component that runs, in both postures, and points at the
// file or directory that carries it.
func TestDefaultLocationCodeIsReportedOnDelivery(t *testing.T) {
	for _, observe := range []bool{true, false} {
		req := defaultsPluginReq(t, TierNamespaced, "")
		req.Observe = observe
		results, err := DeliverPlugin(req)
		if err != nil {
			t.Fatal(err)
		}
		byName := map[string]Result{}
		for _, r := range results {
			byName[r.Name] = r
		}
		for _, comp := range defaultLocationCode {
			r, ok := byName["acme-tools:"+comp]
			if !ok {
				t.Errorf("observe=%v: %s at its default location was delivered with no line of "+
					"its own: %+v", observe, comp, results)
				continue
			}
			if !pluginpack.Contains(req.Plugin.Dir, r.Path) || r.Path == req.Plugin.ManifestPath {
				t.Errorf("observe=%v: the %s line points at %s, not at the default location "+
					"that carries it", observe, comp, r.Path)
			}
		}
	}
}

// A flat destination refuses EVERY component Claude Code loads from a default location by name,
// prose and code alike, and the refusal is real: when the plugin's root is itself a skill, the
// copy of that root leaves every one of them behind. workflows/ and themes/ used to arrive in
// that copy, refused by no line, and Claude Code ignored them there.
func TestFlatDeliveryRefusesAndExcludesEveryDefaultLocation(t *testing.T) {
	req := defaultsPluginReq(t, TierFlat, `{"name":"acme-tools","skills":["./"]}`)
	if err := os.WriteFile(filepath.Join(req.Plugin.Dir, "SKILL.md"),
		[]byte("---\nname: acme-tools\ndescription: d\n---\nroot skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	refused := map[string]bool{}
	for _, r := range results {
		if r.Action == ActionRefused {
			refused[r.Name] = true
		}
	}
	root := filepath.Join(req.SkillsDir, "acme-tools")
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatalf("the root skill itself must still be delivered: %v", err)
	}
	for comp, where := range pluginpacktest.EveryDefaultLocation {
		if !refused["acme-tools:"+comp] {
			t.Errorf("%s at its default location (%s) was not refused by name on a flat "+
				"destination: %+v", comp, where, results)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(where))); err == nil {
			t.Errorf("%s arrived at a FLAT destination that refused %s by name", where, comp)
		}
	}
}

// A SETTING'S VALUE IS A NAME, NOT A PATH. The flat copy of a root skill leaves out every path a
// component field names, and `settings.agent` names an agent, so reading it as a path would drop
// the skill's own folder of that name from the copy.
func TestFlatRootSkillKeepsTheFolderASettingNames(t *testing.T) {
	req := defaultsPluginReq(t, TierFlat,
		`{"name":"acme-tools","skills":["./"],"settings":{"agent":"notes"}}`)
	for rel, body := range map[string]string{
		"SKILL.md":       "---\nname: acme-tools\ndescription: d\n---\nroot skill\n",
		"notes/guide.md": "the root skill's own notes\n",
	} {
		p := filepath.Join(req.Plugin.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "acme-tools", "notes", "guide.md")); err != nil {
		t.Errorf("the root skill's notes/ folder was left out of its flat copy because the "+
			"plugin's agent setting is called notes: %v", err)
	}
}

// A FIELD'S DECLARED PATH IS PLUGIN MACHINERY: the other side of the setting test above. A flat
// delivery refuses each component by name, so a file at a path its manifest field names (`flows/`
// for `workflows`, `looks/` for `themes`) must not ride along in the root skill's copy either.
func TestFlatRootSkillLeavesOutEveryDeclaredComponentPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteEveryDeclaredPathPlugin(t, dir, "acme-tools")
	pl, ok := pluginpack.Load(dir)
	if !ok {
		t.Fatal("the fixture is not plugin-shaped")
	}
	req := PluginRequest{
		Pack: "wrapper", Plugin: pl, Tier: TierFlat,
		SkillsDir:   filepath.Join(t.TempDir(), ".claude", "skills"),
		Composed:    &Manifest{Entries: map[string]string{}},
		Claimed:     map[string]string{},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20261003-000000",
	}
	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	refused := map[string]bool{}
	for _, r := range results {
		if r.Action == ActionRefused {
			refused[r.Name] = true
		}
	}
	root := filepath.Join(req.SkillsDir, "acme-tools")
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatalf("the root skill itself must still be delivered: %v", err)
	}
	for comp, rel := range pluginpacktest.EveryDeclaredPath {
		if !refused["acme-tools:"+comp] {
			t.Errorf("%s declared at %s was not refused by name: %+v", comp, rel, results)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s arrived in the root skill's flat copy, though the same delivery refused "+
				"%s by name", rel, comp)
		}
	}
}

// Copilot's own hooks locations take the same flat-delivery path: a root hooks.json, which
// Copilot reads before hooks/hooks.json, and com.github.copilot/hooks/hooks.json, where it reads
// an Agent Plugins spec plugin's hooks. Each is refused by name AND kept out of the copy of a
// root that is itself a skill.
func TestFlatDeliveryRefusesAndExcludesCopilotsHooksFiles(t *testing.T) {
	for _, rel := range []string{"hooks.json", "com.github.copilot/hooks/hooks.json"} {
		t.Run(rel, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "acme-tools")
			for name, body := range map[string]string{
				".claude-plugin/plugin.json": `{"name":"acme-tools","skills":["./"]}`,
				"SKILL.md":                   "---\nname: acme-tools\ndescription: d\n---\nroot skill\n",
				rel: `{"version":1,"hooks":{"sessionStart":[` +
					`{"type":"command","bash":"echo hi"}]}}`,
			} {
				p := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			pl, ok := pluginpack.Load(dir)
			if !ok {
				t.Fatal("the fixture is not plugin-shaped")
			}
			req := PluginRequest{
				Pack: "wrapper", Plugin: pl, Tier: TierFlat,
				SkillsDir:   filepath.Join(t.TempDir(), ".claude", "skills"),
				Composed:    &Manifest{Entries: map[string]string{}},
				Claimed:     map[string]string{},
				ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
				Stamp:       "20261003-000000",
			}
			results, err := DeliverPlugin(req)
			if err != nil {
				t.Fatal(err)
			}
			refused := false
			for _, r := range results {
				refused = refused || (r.Action == ActionRefused && r.Name == "acme-tools:hooks")
			}
			if !refused {
				t.Errorf("hooks at %s were not refused by name on a flat destination: %+v", rel, results)
			}
			root := filepath.Join(req.SkillsDir, "acme-tools")
			if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
				t.Fatalf("the root skill itself must still be delivered: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				t.Errorf("%s arrived at a FLAT destination that refused it by name", rel)
			}
		})
	}
}

// A manifest that starts with a UTF-8 byte order mark is a plugin (Claude Code strips the mark
// and loads it), so the delivered copy must carry yolo's marker like any other. Unmarked, the
// next apply reads its own output as a plugin the user wrote and downgrades the destination.
func TestByteOrderMarkManifestIsMarkedOnDelivery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteDefaultLocationPlugin(t, dir, "acme-tools")
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"),
		[]byte("\ufeff"+`{"name":"acme-tools"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pl, ok := pluginpack.Load(dir)
	if !ok {
		t.Fatal("a manifest with a byte order mark is not plugin-shaped")
	}
	req := PluginRequest{
		Pack: "wrapper", Plugin: pl, Tier: TierNamespaced,
		SkillsDir:   filepath.Join(t.TempDir(), ".claude", "skills"),
		Composed:    &Manifest{Entries: map[string]string{}},
		Claimed:     map[string]string{},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20261003-000000",
	}
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	if dest := filepath.Join(req.SkillsDir, "acme-tools"); !IsYoloPluginDir(dest) {
		t.Errorf("the delivered copy of a byte-order-marked manifest carries no yolo marker, so "+
			"the next apply would not recognize %s as its own", dest)
	}
}
