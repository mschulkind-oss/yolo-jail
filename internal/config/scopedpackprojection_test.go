package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func scopedFixtureEntries(t *testing.T, fields string, files map[string]string) []PackEntry {
	t.Helper()
	home := readablePackTestHome(t)
	root := filepath.Join(home, ".config/yolo-jail")
	corpus := filepath.Join(root, "packs/corpus")
	for name, data := range files {
		write(t, filepath.Join(corpus, filepath.FromSlash(name)), data)
	}
	write(t, filepath.Join(root, "config.toml"), "packs = [{ pack = \"local/corpus\", "+fields+" }]\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("producer: %v %v %+v", err, problems, entries)
	}
	return entries
}

func TestSelectedPackDeclarationsProjectsTypedBeforeClosure(t *testing.T) {
	entries := scopedFixtureEntries(t, `skills = { only = ["chosen", "remote"] }`, map[string]string{
		"pack.json":                  `{"needs":[{"pack":"wire-bridge"}],"contributes":[{"kind":"program","bin":"poison","via":"npm","package":"poison"},{"kind":"skills","from":"skills"},{"kind":"skills","from":"extra","agents":["fixture"]},{"kind":"skills","source":"git+https://example.invalid/own/remote//skills/original?ref=v1","as":"remote","agents":["other"]},{"kind":"skills","source":"git+https://example.invalid/own/unused//skills/unused?ref=v1","as":"unused"}]}`,
		"skills/chosen/SKILL.md":     "older\n",
		"skills/hidden/SKILL.md":     "hidden\n",
		"extra/chosen/notes.md":      "later folder\n",
		"home/.fixture/poison":       "home\n",
		"overlays/poison.json":       "{}\n",
		"briefing/poison.md":         "poison\n",
		".claude-plugin/plugin.json": `{"name":"root-poison"}`,
		"derive.lua":                 "error('omitted root code must never run')\n",
	})
	selected := SelectedPackDeclarationsDetailed(entries, UserScopeSelection(), nil, packload.EmbeddedFS())
	if !selected.Complete() || len(selected.Added) != 0 || len(selected.Configured) != 1 {
		t.Fatalf("projection before closure: %+v", selected)
	}
	p := selected.Configured[0]
	if p.Projection == nil || !reflect.DeepEqual(p.Projection.Selected, []string{"chosen", "remote"}) {
		t.Fatalf("selected: %+v", p.Projection)
	}
	if len(p.InstallBins()) != 0 || len(p.Plugins()) != 0 || len(p.Decl.DeclaredNeeds()) != 0 || len(p.OverlayFiles) != 0 || p.SourcedSkillsWithheld != "" {
		t.Fatalf("scope activated omitted content: %+v", p)
	}
	skills, problems := p.GovernedSources("skills")
	briefings, bproblems := p.GovernedSources("briefing")
	if len(skills)+len(briefings)+len(problems)+len(bproblems) != 0 {
		t.Fatalf("original conventions leaked: %v %v %v %v", skills, briefings, problems, bproblems)
	}
	rows := p.Projection.Rows
	if rows[0].Kind != "directory" || rows[0].MarkerState != "absent" || rows[0].Path != "extra/chosen" || !reflect.DeepEqual(rows[0].By.Agents, []string{"fixture"}) {
		t.Fatalf("later governor parity: %+v", rows[0])
	}
	if !rows[1].Sourced || rows[1].Kind != "marker" || rows[1].By.Source != "git+https://example.invalid/own/remote//skills/original?ref=v1" || !reflect.DeepEqual(rows[1].By.Agents, []string{"other"}) {
		t.Fatalf("manifest row changed: %+v", rows[1])
	}
	if script := packload.DeriveScript(p); script != "" {
		t.Fatalf("projected root retained derive code: %q", script)
	}
	if p.Projection.Omitted["program"] != 1 {
		t.Fatalf("omitted counts: %+v", p.Projection)
	}
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Fatalf("pure projection created a mirror: %v", err)
	}
}

func TestResolvePackScopedSelectorRefusals(t *testing.T) {
	for _, tc := range []struct{ fields, want string }{
		{`skills = { only = ["missing"] }`, `"missing" matches no candidate`},
		{`skills = { exclude = ["stale"] }`, `"stale" matches no candidate`},
		{`skills = { only = ["a*"], exclude = ["alpha"] }`, `"a*" is entirely excluded`},
		{`skills = { exclude = ["*"] }`, `selection is empty`},
		{`skills = { only = ["wrapper"] }`, `is a plugin; import the pack whole`},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			entries := scopedFixtureEntries(t, tc.fields, map[string]string{"skills/alpha/SKILL.md": "alpha\n", "skills/beta/SKILL.md": "beta\n", "skills/wrapper/.claude-plugin/plugin.json": `{"name":"wrapper"}`})
			_, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), "packs[0]") {
				t.Fatalf("wanted refusal %q and origin: %v", tc.want, err)
			}
		})
	}
}

func TestResolvePackRawRootAndNonrecursiveCandidates(t *testing.T) {
	for _, tc := range []struct {
		fields string
		files  map[string]string
		names  []string
	}{
		{`skills = { dir = "." }`, map[string]string{"SKILL.md": "root\n", "child/SKILL.md": "data\n"}, []string{"corpus"}},
		{`subdir = "nested/root", skills = { dir = "." }`, map[string]string{"nested/root/SKILL.md": "root\n"}, []string{"root"}},
		{`skills = { dir = "collection", only = ["*"], exclude = ["beta"] }`, map[string]string{"collection/alpha/SKILL.md": "a\n", "collection/beta/SKILL.md": "b\n", "collection/shared/notes.md": "data\n", "collection/deeper/nested/SKILL.md": "deeper\n"}, []string{"alpha"}},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			entries := scopedFixtureEntries(t, tc.fields, tc.files)
			res, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
			if err != nil || !reflect.DeepEqual(res.Pack.Projection.Selected, tc.names) {
				t.Fatalf("raw root selection: %+v %v", res, err)
			}
			for _, row := range res.Pack.Projection.Rows {
				if row.Kind != "marker" || row.MarkerState != "present" || len(row.By.Agents) != 0 {
					t.Fatalf("raw widened: %+v", row)
				}
			}
		})
	}
}

func TestSelectedPackDeclarationsProjectsEmbeddedWithoutMaterialization(t *testing.T) {
	home := readablePackTestHome(t)
	source := fstest.MapFS{
		"fixture/pack.json":              {Data: []byte(`{"contributes":[{"kind":"program","bin":"poison","via":"npm","package":"poison"}]}`)},
		"fixture/skills/chosen/notes.md": {Data: []byte("folder\n")},
		"fixture/skills/hidden/SKILL.md": {Data: []byte("hidden\n")},
	}
	entry := EmbeddedPackEntry("fixture")
	entry.Skills = &packsrc.SkillSelection{Only: []string{"chosen"}}
	entry.ConfigOrigin = "config.packs[0] (fixture.toml)"
	before, _ := os.ReadDir(filepath.Join(home, ".local/share/yolo-jail"))
	selected := SelectedPackDeclarationsDetailed([]PackEntry{entry}, UserScopeSelection(), nil, source)
	if !selected.Complete() || len(selected.Packs()) != 1 {
		t.Fatalf("FS projection failed: %+v", selected)
	}
	p := selected.Packs()[0]
	if p.Projection == nil || len(p.InstallBins()) != 0 || len(p.Projection.Rows) != 1 || p.Projection.Rows[0].MarkerState != "absent" {
		t.Fatalf("FS projection widened: %+v", p)
	}
	after, _ := os.ReadDir(filepath.Join(home, ".local/share/yolo-jail"))
	if len(before) != len(after) {
		t.Fatalf("declaration reader materialized embedded packs")
	}
}

func TestResolvePackScopedLocalRootConfinement(t *testing.T) {
	for _, field := range []string{`subdir = "escape"`, `skills = { dir = "escape" }`} {
		t.Run(field, func(t *testing.T) {
			entries := scopedFixtureEntries(t, field, map[string]string{"marker": "base\n"})
			outside := t.TempDir()
			write(t, filepath.Join(outside, "SKILL.md"), "outside\n")
			if err := os.Symlink(outside, filepath.Join(entries[0].LocalPath, "escape")); err != nil {
				t.Fatal(err)
			}
			_, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
			if err == nil || !strings.Contains(err.Error(), "escapes source root") {
				t.Fatalf("escaped root: %v", err)
			}
		})
	}
}

func TestResolvePackWholeSubdirKeepsWholeSemantics(t *testing.T) {
	entries := scopedFixtureEntries(t, `subdir = "nested"`, map[string]string{"nested/pack.json": `{"contributes":[{"kind":"program","bin":"fixture","via":"npm","package":"fixture"}]}`, "outside": "unrelated\n"})
	res, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
	if err != nil || res.Pack.Projection != nil || !reflect.DeepEqual(res.Pack.InstallBins(), []string{"fixture"}) || filepath.Base(res.Pack.Root) != "nested" {
		t.Fatalf("whole subdir lost semantics: %+v %v", res, err)
	}
}

func TestResolvePackScopedStageContainsOnlySelectedCarrier(t *testing.T) {
	entries := scopedFixtureEntries(t, `skills = { only = ["chosen"] }`, map[string]string{"skills/chosen/SKILL.md": "chosen\n", "skills/hidden/SKILL.md": "hidden\n"})
	dest := filepath.Join(t.TempDir(), "stage")
	resolved, err := ResolvePack(entries[0], ResolvePackSpec{Dest: dest})
	if err != nil || resolved.Pack == nil || len(resolved.Pack.SourcedSkillRows()) != 1 || resolved.Pack.SourcedSkillRows()[0].Plan == nil {
		t.Fatalf("scoped stage: %+v %v", resolved, err)
	}
	dest = resolved.Pack.Root
	for _, rel := range []string{"skills", "pack.toml", packload.ScopedCarrierDir + "/skills/hidden"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); !os.IsNotExist(err) {
			t.Fatalf("unselected/discoverable content staged at %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, packload.ScopedCarrierDir, "skills/chosen/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestScopedNeedsRefusesCanonicalShippedRoot(t *testing.T) {
	source := fstest.MapFS{
		"target/skills/chosen/SKILL.md": {Data: []byte("chosen\n")},
		"needy/pack.json":               {Data: []byte(`{"needs":[{"pack":"target"}]}`)},
	}
	scoped := EmbeddedPackEntry("target")
	scoped.Name = "yolo/target"
	scoped.EmbeddedName = "target"
	scoped.Skills = &packsrc.SkillSelection{Only: []string{"chosen"}}
	scoped.ConfigOrigin = "config.packs[0] (fixture.toml)"
	selected := SelectedPackDeclarationsDetailed([]PackEntry{scoped, EmbeddedPackEntry("needy")}, UserScopeSelection(), nil, source)
	text := fmt.Sprint(selected.ClosureErr)
	if selected.Complete() || !strings.Contains(text, "needy") || !strings.Contains(text, "scoped pack target") {
		t.Fatalf("scoped shipped dependency widened: %+v", selected)
	}
}
