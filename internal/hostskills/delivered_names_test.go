package hostskills

// delivered_names_test.go pins the names an agent sees (docs/design/pack-naming.md §3.4) and the
// skill-name clashes (§3.5) on the design's worked example, §3.4.1. Every layer is built by
// PackLayer from a pack carrying the namespace its config entry gave it, the constructor both
// notches read a pack through, and composed by ComposeInto, the writer both notches share.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// exampleLayer is one §3.4.1 entry: its pack, the namespace the entry gives it, and its skills.
func exampleLayer(t *testing.T, pack, namespace string, shipped bool, skills ...string) Layer {
	t.Helper()
	l := PackLayer(&packload.Pack{Name: pack, Namespace: namespace, Official: shipped, Root: t.TempDir()})
	l.Sources = []string{intoSource(t, skills...)}
	return l
}

// workedExample is §3.4.1's selection with each entry's skills.
func workedExample(t *testing.T) []Layer {
	return []Layer{
		exampleLayer(t, "dotfiles/matt", "", false, "review", "commit"),
		exampleLayer(t, "dotfiles/matt-fzf", "", false, "fzf-pick"),
		exampleLayer(t, "work/deploy", "work", false, "deploy", "review"),
		exampleLayer(t, "acme/tools", "acme", false, "review", "configure"),
		exampleLayer(t, "acme/lint", "acme", false, "check"),
		exampleLayer(t, "local/scratch", "", false, "notes"),
		exampleLayer(t, "git:alice/tools", "tools", false, "fmt"),
		exampleLayer(t, "git:bob/tools", "bob", false, "fmt"),
	}
}

func composedNames(t *testing.T, layers []Layer) []string {
	t.Helper()
	dir := t.TempDir()
	if _, err := ComposeInto(Destination{Dir: "~/.claude/skills", Layers: layers}, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestTheWorkedExampleDeliversEachSkillUnderItsNamespace(t *testing.T) {
	dir := t.TempDir()
	if _, err := ComposeInto(Destination{Dir: "~/.claude/skills", Layers: workedExample(t)}, dir); err != nil {
		t.Fatal(err)
	}
	// The "Agents see" column: three packs ship `review`, and the agent sees three names.
	want := map[string]string{
		"review": "review", "commit": "commit", "fzf-pick": "fzf-pick",
		"work-deploy": "work-deploy", "work-review": "work-review",
		"acme-review": "acme-review", "acme-configure": "acme-configure", "acme-check": "acme-check",
		"notes": "notes", "tools-fmt": "tools-fmt", "bob-fmt": "bob-fmt",
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(want) {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Errorf("delivered %v, want exactly the %d names of §3.4.1", got, len(want))
	}
	for name := range want {
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			t.Errorf("%s was not delivered: %v", name, err)
			continue
		}
		// A namespaced copy carries its delivered name in `name` too (pi and Copilot key on it);
		// a bare one is the author's file exactly.
		namespaced := strings.Contains(name, "-") && name != "fzf-pick"
		if namespaced && !strings.Contains(string(data), "name: "+name+"\n") {
			t.Errorf("%s's SKILL.md does not carry its delivered name:\n%s", name, data)
		}
		if !namespaced && strings.Contains(string(data), "name:") {
			t.Errorf("a bare skill's SKILL.md was rewritten:\n%s", data)
		}
	}
}

// Removing acme/lint and adding acme/extra changes no other entry's names (PN-D3).
func TestAnotherPacksArrivalRenamesNothing(t *testing.T) {
	before := composedNames(t, workedExample(t))
	layers := workedExample(t)
	layers = append(layers[:4], layers[5:]...)
	layers = append(layers, exampleLayer(t, "acme/extra", "acme", false, "extra"))
	after := composedNames(t, layers)
	beforeSet := map[string]bool{}
	for _, n := range before {
		beforeSet[n] = true
	}
	for _, n := range after {
		if !beforeSet[n] && n != "acme-extra" {
			t.Errorf("adding acme/extra delivered the new name %q", n)
		}
	}
	if len(after) != len(before) {
		t.Errorf("before %v, after %v: want acme-check swapped for acme-extra and nothing else", before, after)
	}
}

// Each skill-name row of §3.4.1's second table is refused before anything is written, naming both
// entries and the step.
func TestEveryWorkedExampleSkillClashIsRefusedWithItsStep(t *testing.T) {
	for _, tc := range []struct {
		name  string
		add   Layer
		edit  func([]Layer) []Layer
		names []string
		step  string
	}{
		{name: "two bare entries ship notes",
			add:   exampleLayer(t, "local/notes", "", false, "notes"),
			names: []string{"local/scratch", "local/notes"}, step: "bare = false"},
		{name: "a bare source's new pack ships review",
			add:   exampleLayer(t, "dotfiles/matt-review", "", false, "review"),
			names: []string{"dotfiles/matt", "dotfiles/matt-review"}, step: "bare = false"},
		{name: "a release ships commit",
			add:   exampleLayer(t, "git", "", true, "commit"),
			names: []string{"dotfiles/matt", "shipped pack git"}, step: "bare = false"},
		{name: "acme marked bare",
			edit: func(ls []Layer) []Layer {
				ls[3] = exampleLayer(t, "acme/tools", "", false, "review", "configure")
				return ls
			},
			names: []string{"dotfiles/matt", "acme/tools"}, step: "bare = false"},
		{name: "a bare skill spells a namespaced one",
			edit: func(ls []Layer) []Layer {
				ls[0] = exampleLayer(t, "dotfiles/matt", "", false, "review", "commit", "work-review")
				return ls
			},
			names: []string{"dotfiles/matt", "work/deploy"}, step: "different namespace"},
		{name: "one namespace ships check twice",
			edit: func(ls []Layer) []Layer {
				ls[3] = exampleLayer(t, "acme/tools", "acme", false, "review", "configure", "check")
				return ls
			},
			names: []string{"acme/tools", "acme/lint"}, step: "yolo pack lint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layers := workedExample(t)
			if tc.edit != nil {
				layers = tc.edit(layers)
			} else {
				layers = append(layers, tc.add)
			}
			dir := t.TempDir()
			_, err := ComposeInto(Destination{Dir: "~/.claude/skills", Layers: layers}, dir)
			if err == nil {
				t.Fatal("composed past the clash")
			}
			for _, want := range append(tc.names, tc.step) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("a refused composition wrote %d entries", len(entries))
			}
		})
	}
}

// A namespaced skill's copy is in sync after it is written: the rewritten `name` is compared as
// written, not against the author's bytes, so a second apply is a no-op.
func TestANamespacedSkillIsUnchangedOnTheSecondApply(t *testing.T) {
	l := exampleLayer(t, "work/deploy", "work", false, "deploy")
	skillsDir := t.TempDir()
	d := []Destination{{Dir: skillsDir, Layers: []Layer{l}}}
	req := ComposeRequest{Composed: &Manifest{}, ArchiveRoot: ArchiveRoot(t.TempDir()), Stamp: "s1",
		Configured: map[string]bool{"work/deploy": true}}
	if _, err := RenderHostSkills(d, req, false); err != nil {
		t.Fatal(err)
	}
	req.Stamp = "s2"
	res, err := RenderHostSkills(d, req, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		if r.Name != "work-deploy" {
			continue
		}
		found = true
		if r.Action != ActionUnchanged || r.WouldChange {
			t.Errorf("the second apply of an unchanged namespaced skill: %+v", r)
		}
	}
	if !found {
		t.Errorf("the second apply reported nothing for work-deploy: %+v", res)
	}
}

// A wrapped plugin lands at `<namespace>-<manifest name>`, and its copied manifest says so and
// names the pack that delivered it; the pack's own manifest is untouched.
func TestAWrappedPluginIsDeliveredUnderItsNamespace(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "skills", "Kit")
	writePluginFile(t, filepath.Join(plug, ".claude-plugin", "plugin.json"), `{"name":"Kit"}`)
	writePluginFile(t, filepath.Join(plug, "skills", "go", "SKILL.md"), "# go")
	p, problems := packload.LoadDirForUse(root, "acme/kit")
	if len(problems) > 0 && p == nil {
		t.Fatal(problems)
	}
	p.Namespace = "acme"
	l := PackLayer(p)
	if len(l.Plugins) != 1 {
		t.Fatalf("the pack carries %d plugins, want 1", len(l.Plugins))
	}
	dir := t.TempDir()
	if _, err := ComposeInto(Destination{Dir: "~/.claude/skills", Layers: []Layer{l}}, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "acme-kit", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("the plugin did not land at acme-kit: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "acme-kit" || m["x-yolo-pack"] != "acme/kit" {
		t.Errorf("the copied manifest = %s, want name acme-kit and x-yolo-pack acme/kit", raw)
	}
	if own, _ := os.ReadFile(filepath.Join(plug, ".claude-plugin", "plugin.json")); string(own) != `{"name":"Kit"}` {
		t.Errorf("the pack's own manifest was edited: %s", own)
	}
}

func writePluginFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenamedSkillSetsOnlyTheFrontmatterName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"---\nname: review\ndescription: x\n---\nbody name: y\n", "---\nname: acme-review\ndescription: x\n---\nbody name: y\n"},
		{"---\ndescription: x\n---\nbody\n", "---\nname: acme-review\ndescription: x\n---\nbody\n"},
		{"# just a body\n", "---\nname: acme-review\n---\n# just a body\n"},
	} {
		if got := string(renamedSkill([]byte(tc.in), "acme-review")); got != tc.want {
			t.Errorf("renamedSkill(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
