package packload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestResolveDestinationsSourcedSkillsKeepAliasesAndConvention(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills/local"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills/local/SKILL.md"), []byte("local"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`[[skills]]
source = "git+https://example.invalid/repo//nested/chosen?ref=main"
as = "first"
agents = ["fixture"]
[[skills]]
source = "git+https://example.invalid/repo//nested/chosen?ref=main"
as = "second"
agents = ["fixture"]
describes = ["skills"]
`)
	decl, problems := packdecl.DecodeTOML(manifest)
	if len(problems) != 0 {
		t.Fatalf("decode: %v", problems)
	}
	p := &Pack{Name: "content", Root: root, Decl: decl}
	dest := &Pack{Name: "destination", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}}}}
	resolved := p.ResolveDestinations([]*Pack{p, dest})
	if len(resolved.Inferred) != 3 {
		t.Fatalf("want two aliases and local broadcast, got %#v", resolved.Inferred)
	}
	aliases := map[string]bool{}
	for _, c := range resolved.Inferred {
		if c.Source != "" {
			aliases[c.As] = true
		}
	}
	if !aliases["first"] || !aliases["second"] {
		t.Fatalf("lost aliases: %#v", aliases)
	}
	sources, problems := resolved.Pack.SkillsSources()
	if len(problems) != 0 || len(sources) != 1 || sources[0].Dir != filepath.Join(root, "skills") || len(sources[0].Agents) != 0 {
		t.Fatalf("convention altered: %#v %v", sources, problems)
	}
	if len(resolved.Pack.GovernedSkillsFor(resolved.Inferred[0])) != 0 {
		t.Fatal("sourced row read conventional local tree")
	}
}
