package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestResolvePackRawScopeDoesNotLoadPoisonManifest(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, ".config", "yolo-jail")
	pack := filepath.Join(root, "packs", "corpus")
	if err := os.MkdirAll(filepath.Join(pack, "skills/core-tools/skill-a"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pack, "skills/core-tools/skill-a/SKILL.md"), "selected\n")
	write(t, filepath.Join(pack, "pack.toml"), "this is not valid TOML [\n")
	write(t, filepath.Join(root, paths.UserConfigTOMLName), `packs = [{ pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a"] } }]`+"\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("producer: %v %v %+v", err, problems, entries)
	}
	resolved, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
	if err != nil || resolved.Pack == nil || len(resolved.Problems) != 0 {
		t.Fatalf("raw scope interpreted manifest: %v %+v", err, resolved)
	}
	if len(resolved.Pack.InstallBins()) != 0 || len(resolved.Pack.Plugins()) != 0 || len(resolved.Pack.Decl.DeclaredNeeds()) != 0 || resolved.Pack.SourcedSkillsWithheld != "" {
		t.Fatalf("scope widened or withheld: %+v", resolved.Pack)
	}
}
