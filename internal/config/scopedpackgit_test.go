package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestResolvePackDirectUserNamedGitRawProjection(t *testing.T) {
	home := readablePackTestHome(t)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPackGit(t, repo, "init", "-q", "--initial-branch=main")
	write(t, filepath.Join(repo, "corpus/pack.toml"), "poison TOML [\n")
	write(t, filepath.Join(repo, "corpus/skills/core-tools/skill-a/SKILL.md"), "chosen\n")
	write(t, filepath.Join(repo, "corpus/skills/core-tools/skill-a/run.sh"), "printf fixture\n")
	write(t, filepath.Join(repo, "corpus/skills/core-tools/skill-b/SKILL.md"), "chosen too\n")
	write(t, filepath.Join(repo, "corpus/skills/core-tools/hidden/SKILL.md"), "hidden\n")
	write(t, filepath.Join(repo, "corpus/skills/core-tools/shared/notes.md"), "support only\n")
	if err := os.Symlink("skills/core-tools", filepath.Join(repo, "corpus/alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/outside-fixture", filepath.Join(repo, "corpus/unused-unsafe")); err != nil {
		t.Fatal(err)
	}
	configPackGit(t, repo, "add", "--all")
	configPackGit(t, repo, "commit", "-qm", "synthetic raw upstream")
	commit := configPackGit(t, repo, "rev-parse", "HEAD")
	root := filepath.Join(home, ".config/yolo-jail")
	body := "packs = [{ pack = \"demo/corpus\", skills = { dir = \"skills/core-tools\", only = [\"skill-a\", \"skill-b\"] } }]\n[pack_sources.demo]\ngit = 'file://" + filepath.ToSlash(repo) + "'\nref = 'main'\n"
	write(t, filepath.Join(root, "config.toml"), body)
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("producer: %+v %v %v", entries, problems, err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	results, err := store.Refresh([]packsrc.RefreshPack{entries[0].RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("fixture pin: %+v %v", results, err)
	}
	resolved, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err != nil || resolved.Pack == nil || resolved.Pack.Projection == nil {
		t.Fatalf("raw projection interpreted poison or support: %+v %v", resolved, err)
	}
	p := resolved.Pack
	if p.Projection.Commit != commit || len(p.Projection.Rows) != 2 || p.Namespace != "demo" || p.SourcedSkillsWithheld != "" || len(p.Plugins())+len(p.InstallBins()) != 0 {
		t.Fatalf("Git projection widened/lost pin: %+v", p)
	}
	for _, row := range p.Projection.Rows {
		a, err := packsrc.Parse(row.By.Source)
		if err != nil || a.Ref != commit || a.Path != "" || row.SourceDirectory != "corpus/skills/core-tools/"+row.Name || row.Kind != "marker" {
			t.Fatalf("row did not bind pinned base/root path: %+v %+v %v", row, a, err)
		}
	}
	// A selected Git root cannot use even an in-repository directory symlink.
	alias := entries[0]
	alias.Skills = &packsrc.SkillSelection{Dir: "alias", Only: []string{"skill-a"}}
	_, err = ResolvePack(alias, ResolvePackSpec{ReadOnlyStore: true, Getenv: func(string) string { return "" }})
	if err == nil || !strings.Contains(err.Error(), "real directory ancestry") {
		t.Fatalf("Git directory alias was accepted: %v", err)
	}
}
