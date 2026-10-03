package hostskills

// plugindefaults_test.go pins delivery for a wrapped plugin whose code sits at Claude Code's
// DEFAULT locations with no manifest entry (hooks/hooks.json, .mcp.json,
// monitors/monitors.json, bin/). The verbatim copy brings every one of them, so a namespaced
// delivery must name each, and a flat one must refuse each by name AND keep it out of the copy.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

var defaultLocationCode = []string{"hooks", "mcpServers", "monitors", "bin"}

// defaultsPluginReq is testPluginReq over the default-location fixture, with manifest written
// over the fixture's when non-empty (still declaring no component).
func defaultsPluginReq(t *testing.T, tier Tier, manifest string) PluginRequest {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteDefaultLocationPlugin(t, dir, "acme-tools")
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

// A flat destination refuses each by name, and the refusal is real: when the plugin's root is
// itself a skill, the copy of that root leaves every one of them behind.
func TestFlatDeliveryRefusesAndExcludesDefaultLocationCode(t *testing.T) {
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
	for _, comp := range defaultLocationCode {
		if !refused["acme-tools:"+comp] {
			t.Errorf("%s at its default location was not refused by name on a flat "+
				"destination: %+v", comp, results)
		}
	}
	root := filepath.Join(req.SkillsDir, "acme-tools")
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatalf("the root skill itself must still be delivered: %v", err)
	}
	for _, rel := range []string{"hooks/hooks.json", ".mcp.json", "monitors/monitors.json", "bin/acme-tool"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s arrived at a FLAT destination that refused it by name", rel)
		}
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
