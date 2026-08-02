package hostskills

// plugin_test.go pins what a WRAPPED PLUGIN delivery guarantees. Two claims carry the design,
// and each has a failure mode worth naming:
//
//   - VERBATIM. If the copy were selective, everything yolo does not model (agents/,
//     output-styles/, .mcp.json) would vanish and the plugin would half work in a way that
//     looks like the plugin's bug, not yolo's.
//   - NEVER SILENT. On a flat destination a plugin's hooks CANNOT arrive. Saying so is the
//     whole difference between "this tool cannot do that" and "this tool lost my hooks".
//
// Every home is a t.TempDir(). A test here must never touch a real $HOME.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// buildPlugin writes a plugin tree with a manifest, a nested skill, and — importantly — content
// in dirs yolo has NO kind for, which is what the verbatim claim is about.
func buildPlugin(t *testing.T, root, name, manifest string) *pluginpack.Plugin {
	t.Helper()
	dir := filepath.Join(root, name)
	md := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(dir, "skills"), "nested", "from the plugin")
	// Content yolo models NOTHING about. It must still arrive.
	for rel, body := range map[string]string{
		"agents/reviewer.md":      "# a sub-agent definition\n",
		"output-styles/terse.md":  "# an output style\n",
		".mcp.json":               `{"mcpServers":{"db":{"command":"x"}}}`,
		"hooks/pre-tool-use.json": `{"matcher":"*"}`,
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pl, ok := pluginpack.Load(dir)
	if !ok {
		t.Fatalf("test fixture at %s is not plugin-shaped", dir)
	}
	return pl
}

// testPluginReq builds a PluginRequest against temp dirs only.
func testPluginReq(t *testing.T, tier Tier, manifest string) (PluginRequest, *pluginpack.Plugin) {
	t.Helper()
	home := t.TempDir()
	pl := buildPlugin(t, t.TempDir(), "acme-tools", manifest)
	return PluginRequest{
		Pack:        "wrapper",
		Plugin:      pl,
		SkillsDir:   filepath.Join(home, ".claude", "skills"),
		Tier:        tier,
		Manifest:    &Manifest{Entries: map[string]string{}},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20260802-000000",
	}, pl
}

// THE headline tier-A claim: the tree lands verbatim — manifest included, and including every
// component yolo does not model. This is what makes "do not translate" true rather than a
// stated intention.
func TestPluginTreeIsDeliveredVerbatim(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced,
		`{"name":"acme-tools","skills":["./"],"agents":["./agents"],
		  "outputStyles":["./output-styles"],"mcpServers":"./.mcp.json"}`)

	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(req.SkillsDir, "acme-tools")

	// The manifest arrived — this is the file that makes the whole tree loadable.
	if _, err := os.Stat(filepath.Join(dest, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("the plugin MANIFEST must be delivered, or the tool loads nothing: %v", err)
	}
	// Non-skill content arrived. A translating delivery would have dropped all of these.
	for _, rel := range []string{
		"agents/reviewer.md", "output-styles/terse.md", ".mcp.json",
		"hooks/pre-tool-use.json", "skills/nested/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s did not survive delivery — yolo models nothing about it, which is "+
				"exactly why the tree must be copied rather than translated: %v", rel, err)
		}
	}
}

// The manifest is carried through with every field intact, plus yolo's ownership marker. A
// struct round-trip would have silently dropped the fields yolo does not model — the same
// translation bug, one level down.
func TestDeliveredManifestKeepsUnknownFieldsAndGainsTheMarker(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced,
		`{"name":"acme-tools","skills":["./"],"someFutureFeature":{"deeply":["nested"]}}`)
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(req.SkillsDir, "acme-tools",
		".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["someFutureFeature"] == nil {
		t.Errorf("an unmodeled manifest field was dropped in delivery: %s", data)
	}
	if got["x-yolo-managed-by"] != "yolo-jail" {
		t.Errorf("the delivered manifest must carry yolo's ownership marker, or a later apply "+
			"cannot tell it from a plugin the user installed by hand: %s", data)
	}
	// And the marker is what makes the dir recognizable as yolo's.
	if !IsYoloPluginDir(filepath.Join(req.SkillsDir, "acme-tools")) {
		t.Error("the delivered dir must be recognized as yolo-managed")
	}
}

// Delivering twice is a no-op. This is the test that catches an accumulating render.
func TestPluginDeliveryIsIdempotent(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced, `{"name":"acme-tools","skills":["./"]}`)
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	first := treeSnapshot(t, req.SkillsDir)
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	if second := treeSnapshot(t, req.SkillsDir); first != second {
		t.Errorf("second delivery changed the tree:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// The user's own siblings are untouched, which is the structural reason a namespaced delivery is
// safe to point at a real skills dir at all.
func TestPluginDeliveryLeavesSiblingsAlone(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced, `{"name":"acme-tools","skills":["./"]}`)
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, req.SkillsDir, "my-own", "MINE")
	writeSkill(t, req.SkillsDir, "nested", "ALSO MINE, same name as the plugin's")

	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"my-own", "nested"} {
		data, err := os.ReadFile(filepath.Join(req.SkillsDir, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("user skill %q must survive: %v", name, err)
		}
		if !strings.Contains(string(data), "MINE") {
			t.Errorf("user skill %q was overwritten: %s", name, data)
		}
	}
}

// THE headline tier-B claim: a component that cannot arrive is refused BY NAME. Never silently
// dropped — a plugin that looks installed and half works is the failure mode this rule exists
// to remove.
func TestFlatRefusesNonSkillComponentsByName(t *testing.T) {
	req, _ := testPluginReq(t, TierFlat, `{"name":"acme-tools","skills":["./"],
		"hooks":{"PreToolUse":[]},"mcpServers":"./.mcp.json","lspServers":{"gopls":{}},
		"agents":["./agents"],"outputStyles":["./output-styles"],"commands":"./cmds"}`)

	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	refused := map[string]string{}
	for _, r := range results {
		if r.Action == ActionRefused {
			refused[r.Name] = r.Detail
		}
	}
	for _, comp := range []string{"hooks", "mcpServers", "lspServers", "agents",
		"outputStyles", "commands"} {
		want := "acme-tools:" + comp
		if _, ok := refused[want]; !ok {
			t.Errorf("component %q was NOT refused by name on a flat destination — it cannot "+
				"arrive there, and a silent drop is the failure mode this rule removes. "+
				"Refusals: %v", want, refused)
		}
	}
	// The skills DO arrive: withholding them too would punish the user for a hook they
	// never asked for.
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "nested", "SKILL.md")); err != nil {
		t.Errorf("a flat delivery must still deliver the plugin's SKILLS: %v", err)
	}
	// And no plugin manifest is written where nothing reads it.
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "acme-tools")); !os.IsNotExist(err) {
		t.Error("a flat destination must not get a plugin directory — its tool cannot load one")
	}
}

// REGRESSION, and it took a real run against the real tool to find: when the plugin's ROOT is
// itself a skill (`skills: ["./"]` — the layout the actual scaffolder emits), a flat delivery
// copies that root as an ordinary skill dir. A plain recursive copy therefore dragged the
// ENTIRE plugin along — manifest, hooks/, agents/, .mcp.json — into a destination that had just
// refused every one of those by name, one line earlier, in the same output.
//
// That is strictly worse than either honest answer: the user reads "your hooks cannot arrive"
// while the hooks arrive. The refusal has to be real, so the copy excludes the plugin machinery.
func TestFlatDeliveryDoesNotSmuggleTheWholePluginIn(t *testing.T) {
	req, _ := testPluginReq(t, TierFlat, `{"name":"acme-tools","skills":["./"],
		"hooks":{"PreToolUse":[]},"agents":["./agents"],"mcpServers":"./.mcp.json"}`)
	// The root IS a skill, which is what triggers the whole problem.
	if err := os.WriteFile(filepath.Join(req.Plugin.Dir, "SKILL.md"),
		[]byte("---\nname: acme-tools\ndescription: d\n---\nroot skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(req.SkillsDir, "acme-tools")
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatalf("the root skill itself must still be delivered: %v", err)
	}
	// None of the refused machinery may have come with it.
	for _, rel := range []string{
		".claude-plugin/plugin.json", "hooks/pre-tool-use.json", "agents/reviewer.md",
		".mcp.json", "output-styles/terse.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s arrived at a FLAT destination that refused it by name — the refusal "+
				"must be real, not a message printed beside the thing it refused", rel)
		}
	}
}

// A flat delivery still respects the user's own entries: the plugin's skill is skipped, not
// overwritten, when a hand-written skill already holds the name.
func TestFlatPluginSkillYieldsToTheUser(t *testing.T) {
	req, _ := testPluginReq(t, TierFlat, `{"name":"acme-tools","skills":["./"]}`)
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, req.SkillsDir, "nested", "MINE")

	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	var skipped bool
	for _, r := range results {
		if r.Name == "nested" && r.Action == ActionSkippedUser {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("a user-owned entry must be skipped and reported, got %+v", results)
	}
	data, _ := os.ReadFile(filepath.Join(req.SkillsDir, "nested", "SKILL.md"))
	if !strings.Contains(string(data), "MINE") {
		t.Errorf("the user's skill was overwritten: %s", data)
	}
}

// A namespaced delivery onto a foreign directory of the plugin's name DOWNGRADES and says why,
// rather than absorbing whatever the user put there (most likely a plugin they installed).
func TestPluginDeliveryRefusesForeignDir(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced, `{"name":"acme-tools","skills":["./"]}`)
	foreign := filepath.Join(req.SkillsDir, "acme-tools")
	writeSkill(t, foreign, "handwritten", "MINE")

	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	var downgraded bool
	for _, r := range results {
		if r.Action == ActionRefused && strings.Contains(r.Detail, "not written by yolo") {
			downgraded = true
		}
	}
	if !downgraded {
		t.Errorf("a foreign dir at the plugin's name must downgrade and say so: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(foreign, "handwritten", "SKILL.md")); err != nil {
		t.Errorf("the user's content must survive: %v", err)
	}
}

// Two packs wrapping same-named plugins is refused, naming the other pack. Delivery is one
// directory per plugin name, so without this the later apply silently wins every time.
func TestTwoPacksCannotWrapOnePluginName(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced, `{"name":"acme-tools","skills":["./"]}`)
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	// A SECOND pack wrapping a plugin of the same name, into the same dir.
	other := req
	other.Pack = "other-wrapper"
	results, err := DeliverPlugin(other)
	if err != nil {
		t.Fatal(err)
	}
	var refused bool
	for _, r := range results {
		if r.Action == ActionRefused && strings.Contains(r.Detail, "wrapper") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("a second pack claiming one plugin name must be refused, naming the first: %+v",
			results)
	}
}

// Observe writes nothing at either tier, while still reporting everything. A dry run that
// diverges from the real thing is worse than no dry run.
func TestPluginObserveWritesNothing(t *testing.T) {
	for _, tier := range []Tier{TierNamespaced, TierFlat} {
		t.Run(tier.String(), func(t *testing.T) {
			req, _ := testPluginReq(t, tier,
				`{"name":"acme-tools","skills":["./"],"hooks":{"x":[]}}`)
			req.Observe = true
			results, err := DeliverPlugin(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 {
				t.Fatal("observe must still report what it would do")
			}
			if _, err := os.Stat(req.SkillsDir); !os.IsNotExist(err) {
				t.Errorf("observe created %s — it must write nothing", req.SkillsDir)
			}
		})
	}
}

// A code-running component that IS delivered is still reported, at the tier where it works.
// "A pack put a hook in my real home" must not be something a user discovers by reading a
// lockfile.
func TestDeliveredCodeComponentsAreReported(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced,
		`{"name":"acme-tools","skills":["./"],"hooks":{"PreToolUse":[]},"mcpServers":"./.mcp.json"}`)
	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]bool{}
	for _, r := range results {
		reported[r.Name] = true
	}
	for _, want := range []string{"acme-tools:hooks", "acme-tools:mcpServers"} {
		if !reported[want] {
			t.Errorf("%q was delivered without a line of its own — a component that RUNS must "+
				"be visible at the moment it lands: %+v", want, results)
		}
	}
}

// A wrapped plugin's OWN manifest must survive the ordinary skills delivery that runs at the
// same destination afterwards.
//
// The bug: DeliverPlugin copies the plugin tree verbatim, manifest included, and then Deliver
// runs on the same pack dir for the pack's loose skills — and unconditionally wrote yolo's
// synthetic manifest over it. The plugin's `hooks/` and `.mcp.json` stayed on disk while the
// manifest that POINTS at them was replaced, so the tool loaded a plugin with none of its
// components and no error anywhere. Caught by reading the delivered file, not by a unit test:
// both halves were individually correct.
func TestPluginManifestSurvivesOrdinarySkillsDelivery(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".claude", "skills")
	packRoot := t.TempDir()

	// A plugin with components yolo does not model, wrapped as a pack that ALSO ships a
	// loose skill (so both delivery paths run at one destination).
	plugDir := filepath.Join(packRoot, "skills", "acme-tools")
	manifestDir := filepath.Join(plugDir, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `{"name":"acme-tools","description":"third-party","skills":["./"],` +
		`"hooks":"hooks/hooks.json","mcpServers":".mcp.json"}`
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(packRoot, "skills"), "loose", "a loose skill")

	pl := pluginpack.Discover(packRoot)
	if len(pl) != 1 {
		t.Fatalf("Discover = %v; want exactly one plugin", pl)
	}
	man := &Manifest{Entries: map[string]string{}}
	req := PluginRequest{
		Pack: "acme-tools", Plugin: pl[0], SkillsDir: skillsDir,
		Tier: TierNamespaced, Manifest: man,
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "a")), Stamp: "20260802-000000",
	}
	if _, err := DeliverPlugin(req); err != nil {
		t.Fatal(err)
	}
	// The ordinary skills pass, at the SAME destination — this is what used to clobber it.
	if _, err := Deliver(Request{
		Pack: "acme-tools", Description: "wrapper pack",
		Sources: []string{filepath.Join(packRoot, "skills")}, SkipSources: []string{pl[0].Dir},
		SkillsDir: skillsDir, Tier: TierNamespaced, Manifest: man,
		ArchiveRoot: req.ArchiveRoot, Stamp: req.Stamp,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(skillsDir, "acme-tools", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hooks", "mcpServers", "third-party"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the plugin's own %q declaration was destroyed by the skills pass — the "+
				"components stay on disk while the manifest pointing at them is replaced, so "+
				"the tool loads a plugin with nothing in it:\n%s", want, got)
		}
	}
}
