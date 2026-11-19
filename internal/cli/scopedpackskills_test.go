package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestDirectScopedTypedFolderDoesNotSynthesizeMarker(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, `packs = [{ pack = "local/corpus", skills = { only = ["chosen"] } }]`+"\n")
	t.Setenv("HOME", user.Root)
	source := filepath.Join(filepath.Dir(user.Path), "packs", "corpus")
	for _, name := range []string{"chosen", "hidden"} {
		dir := filepath.Join(source, "skills", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) != 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	var selected *packload.Pack
	for _, entry := range entries {
		if entry.Name != "local/corpus" {
			continue
		}
		resolved, err := config.ResolvePackForProcess(entry, config.ResolvePackSpec{})
		if err != nil || len(resolved.Problems) != 0 {
			t.Fatalf("resolve: %v %+v", err, resolved)
		}
		selected = resolved.Pack
	}
	if selected == nil {
		t.Fatal("producer lost scoped entry")
	}
	agent := &packload.Pack{Name: "reader", Root: project.Root, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}},
	}}
	selected = selected.ResolveDestinations([]*packload.Pack{selected, agent}).Pack
	home := t.TempDir()
	dests := hostskills.ComposeHostSkillsFor([]*packload.Pack{selected, agent}, home, func(string, packdecl.Kind) bool { return true })
	req := hostskills.ComposeRequest{Composed: &hostskills.Manifest{}, ArchiveRoot: hostskills.ArchiveRoot(t.TempDir()), Stamp: "fixture", PackSetComplete: true, Configured: map[string]bool{"local/corpus": true, "reader": true}}
	if _, err := hostskills.RenderHostSkills(dests, req, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".fixture/skills")
	items, err := os.ReadDir(dest)
	if err != nil || len(items) != 1 || items[0].Name() != "corpus-chosen" {
		t.Fatalf("scoped directories: %v %v", items, err)
	}
	folder := filepath.Join(dest, "corpus-chosen")
	if _, err := os.Lstat(filepath.Join(folder, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("marker synthesized: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(folder, "notes.md"))
	if err != nil || !bytes.Equal(got, []byte("chosen")) {
		t.Fatalf("support: %q %v", got, err)
	}
}
