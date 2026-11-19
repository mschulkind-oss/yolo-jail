package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestLoadJailPacksUsesRecordedScopedProjection(t *testing.T) {
	user := testconfig.User(t, `packs = [{pack = "local/corpus", skills = {only = ["chosen"]}}]`+"\n")
	t.Setenv("HOME", user.Root)
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	if err := os.MkdirAll(filepath.Join(source, "skills/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skills/chosen/notes.md"), []byte("folder\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	root := t.TempDir()
	var selected *packload.Pack
	for _, entry := range entries {
		if entry.Name == "local/corpus" {
			resolved, err := config.ResolvePack(entry, config.ResolvePackSpec{Dest: filepath.Join(root, entry.Slug())})
			if err != nil {
				t.Fatal(err)
			}
			selected = resolved.Pack
		}
	}
	if selected == nil {
		t.Fatal("missing scoped producer")
	}
	if err := packload.StageSkillPayloads([]*packload.Pack{selected}, root, root); err != nil {
		t.Fatal(err)
	}
	if err := packload.WritePackTreeRecord(root, []*packload.Pack{selected}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	packs, err := loadPackRoot(&Env{Home: user.Root, Vars: map[string]string{}}, root)
	if err != nil || len(packs) != 1 {
		t.Fatalf("source-free jail load: %v %v", packs, err)
	}
	rows := packs[0].SourcedSkillRows()
	if len(rows) != 1 || rows[0].Plan == nil || rows[0].Plan.MarkerState != "absent" {
		t.Fatalf("not recorded folder: %+v", rows)
	}
	if packs[0].Projection == nil || len(packs[0].Plugins()) != 0 || len(packs[0].InstallBins()) != 0 {
		t.Fatal("not inert scoped record")
	}
}
