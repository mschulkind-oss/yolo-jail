package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestDirectScopedSameHandleRootsReplayAndCompareSeparately(t *testing.T) {
	user := testconfig.User(t, `packs = [{pack="local/corpus", skills={dir="one"}}, {pack="local/corpus", skills={dir="two"}}]`+"\n")
	t.Setenv("HOME", user.Root)
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, dir := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(source, dir, "chosen"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, dir, "chosen/SKILL.md"), []byte(dir+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output}
	const cname = "yolo-test-scoped-two-roots"
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardPackTree(cname, root) })
	if !o.prepareFreshSkillPayloads(cname, "podman", stagedPacks{root: root, packs: packs, briefings: briefings}) {
		t.Fatal(output.String())
	}
	booted, err := loadPackTree(root)
	if err != nil {
		t.Fatal(err)
	}
	var scoped []*packload.Pack
	for _, p := range booted {
		if p.Projection != nil {
			scoped = append(scoped, p)
		}
	}
	if len(scoped) != 2 || scoped[0].Root == scoped[1].Root || scoped[0].Projection.ImportIdentity == scoped[1].Projection.ImportIdentity {
		t.Fatalf("collapsed roots: %+v", scoped)
	}
	if !diffPackSets(packs, booted).empty() {
		t.Fatal("equivalent scoped replay changed")
	}
	variants := []struct {
		name   string
		mutate func(*packload.SkillProjection)
	}{
		{"only", func(p *packload.SkillProjection) { p.Selection.Only = []string{"chosen"} }},
		{"exclude", func(p *packload.SkillProjection) { p.Selection.Exclude = []string{"omitted"} }},
		{"mode", func(p *packload.SkillProjection) { p.Mode = "typed" }},
		{"audience", func(p *packload.SkillProjection) { p.Rows[0].By.Agents = []string{"reader"} }},
		{"naming", func(p *packload.SkillProjection) { p.Namespace = "other" }},
		{"content", func(p *packload.SkillProjection) { p.CopiedContentDigest = "changed" }},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			changed := *scoped[0]
			projection := *changed.Projection
			projection.Rows = append([]packload.ProjectedSkillRow(nil), projection.Rows...)
			tc.mutate(&projection)
			if err := projection.RefreshDigest(); err != nil {
				t.Fatal(err)
			}
			changed.Projection = &projection
			// Changed entry is first; name-only maps incorrectly hide it behind second.
			d := diffPackSets([]*packload.Pack{&changed, scoped[1]}, scoped)
			if len(d.Changed) != 1 || len(d.Added) != 0 || len(d.Removed) != 0 {
				t.Fatalf("tuple/digest difference hidden: %+v", d)
			}
		})
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("entries: %v %v", err, problems)
	}
	var slugs []string
	for _, entry := range entries {
		if entry.Skills != nil {
			slugs = append(slugs, entry.Slug())
		}
	}
	if len(slugs) != 2 || slugs[0] == slugs[1] {
		t.Fatalf("tuple slug collision: %v", slugs)
	}
	if err := os.Remove(filepath.Join(root, packload.PackTreeRecordName)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPackTree(root); err == nil {
		t.Fatal("missing scoped record silently rediscovered")
	}
}
